// Copyright 2017 fatedier, fatedier@gmail.com
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package client

import (
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"math/rand"
	"net"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/fatedier/frp/assets"
	"github.com/fatedier/frp/pkg/auth"
	"github.com/fatedier/frp/pkg/config"
	"github.com/fatedier/frp/pkg/msg"
	"github.com/fatedier/frp/pkg/transport"
	"github.com/fatedier/frp/pkg/util/log"
	frpNet "github.com/fatedier/frp/pkg/util/net"
	"github.com/fatedier/frp/pkg/util/util"
	"github.com/fatedier/frp/pkg/util/version"
	"github.com/fatedier/frp/pkg/util/xlog"
	"github.com/fatedier/golib/crypto"
	libdial "github.com/fatedier/golib/net/dial"

	fmux "github.com/hashicorp/yamux"
)

func init() {
	crypto.DefaultSalt = "frp"
	rand.Seed(time.Now().UnixNano())
}

// Service 是 frpc 客户端服务的顶层对象。
// 调用入口：cmd/frpc/sub/root.go 会解析配置后创建 client.Service，并调用 Run() 启动客户端。
// 主要职责：维护客户端全局配置、登录 frps、创建 Control 控制连接、管理重连和本地 Admin 服务。
type Service struct {
	// uniq id got from frps, attach it in loginMsg
	runID string

	// manager control connection with server
	ctl   *Control
	ctlMu sync.RWMutex

	// Sets authentication based on selected method
	authSetter auth.Setter

	cfg         config.ClientCommonConf
	pxyCfgs     map[string]config.ProxyConf
	visitorCfgs map[string]config.VisitorConf
	cfgMu       sync.RWMutex

	// The configuration file used to initialize this client, or an empty
	// string if no configuration file was used.
	cfgFile string

	// This is configured by the login response from frps
	serverUDPPort int

	exit uint32 // 0 means not exit

	// service context
	ctx context.Context
	// call cancel to stop service
	cancel context.CancelFunc
}

func NewService(cfg config.ClientCommonConf, pxyCfgs map[string]config.ProxyConf, visitorCfgs map[string]config.VisitorConf, cfgFile string) (svr *Service, err error) {

	ctx, cancel := context.WithCancel(context.Background())
	svr = &Service{
		authSetter:  auth.NewAuthSetter(cfg.ClientConfig),
		cfg:         cfg,
		cfgFile:     cfgFile,
		pxyCfgs:     pxyCfgs,
		visitorCfgs: visitorCfgs,
		exit:        0,
		ctx:         xlog.NewContext(ctx, xlog.New()),
		cancel:      cancel,
	}
	return
}

func (svr *Service) GetController() *Control {
	svr.ctlMu.RLock()
	defer svr.ctlMu.RUnlock()
	return svr.ctl
}

func (svr *Service) Run() error {
	xl := xlog.FromContextSafe(svr.ctx)

	// 如果配置了 dns_server，这里替换 Go 默认 DNS 解析器。
	// 后续 client/service.go:login()、client/control.go:connectServer() 发起连接时会使用该解析器。
	// set custom DNSServer
	if svr.cfg.DNSServer != "" {
		dnsAddr := svr.cfg.DNSServer
		if !strings.Contains(dnsAddr, ":") {
			dnsAddr += ":53"
		}
		// Change default dns server for frpc
		net.DefaultResolver = &net.Resolver{
			PreferGo: true,
			Dial: func(ctx context.Context, network, address string) (net.Conn, error) {
				return net.Dial("udp", dnsAddr)
			},
		}
	}

	// 登录 frps，成功后创建 client/control.go:NewControl()。
	// Control 会继续负责代理注册、心跳、工作连接创建和访问者 visitor 管理。
	// login to frps
	for {
		conn, session, err := svr.login()
		if err != nil {
			xl.Warn("login to server failed: %v", err)

			// if login_fail_exit is true, just exit this program
			// otherwise sleep a while and try again to connect to server
			if svr.cfg.LoginFailExit {
				return err
			}
			util.RandomSleep(10*time.Second, 0.9, 1.1)
		} else {
			// login success
			ctl := NewControl(svr.ctx, svr.runID, conn, session, svr.cfg, svr.pxyCfgs, svr.visitorCfgs, svr.serverUDPPort, svr.authSetter)
			ctl.Run()
			svr.ctlMu.Lock()
			svr.ctl = ctl
			svr.ctlMu.Unlock()
			break
		}
	}

	// 控制连接断开后，由 keepControllerWorking() 负责自动重连 frps。
	go svr.keepControllerWorking()

	if svr.cfg.AdminPort != 0 {
		// Init admin server assets
		assets.Load(svr.cfg.AssetsDir)

		address := net.JoinHostPort(svr.cfg.AdminAddr, strconv.Itoa(svr.cfg.AdminPort))
		err := svr.RunAdminServer(address)
		if err != nil {
			log.Warn("run admin server error: %v", err)
		}
		log.Info("admin server listen on %s:%d", svr.cfg.AdminAddr, svr.cfg.AdminPort)
	}
	<-svr.ctx.Done()
	return nil
}

func (svr *Service) keepControllerWorking() {
	xl := xlog.FromContextSafe(svr.ctx)
	maxDelayTime := 20 * time.Second
	delayTime := time.Second

	// if frpc reconnect frps, we need to limit retry times in 1min
	// current retry logic is sleep 0s, 0s, 0s, 1s, 2s, 4s, 8s, ...
	// when exceed 1min, we will reset delay and counts
	cutoffTime := time.Now().Add(time.Minute)
	reconnectDelay := time.Second
	reconnectCounts := 1

	for {
		// 等待 client/control.go:worker() 完成资源释放后再进行重连，避免新旧控制连接交叉使用。
		<-svr.ctl.ClosedDoneCh()
		if atomic.LoadUint32(&svr.exit) != 0 {
			return
		}

		// the first three retry with no delay
		if reconnectCounts > 3 {
			util.RandomSleep(reconnectDelay, 0.9, 1.1)
			xl.Info("wait %v to reconnect", reconnectDelay)
			reconnectDelay *= 2
		} else {
			util.RandomSleep(time.Second, 0, 0.5)
		}
		reconnectCounts++

		now := time.Now()
		if now.After(cutoffTime) {
			// reset
			cutoffTime = now.Add(time.Minute)
			reconnectDelay = time.Second
			reconnectCounts = 1
		}

		for {
			xl.Info("try to reconnect to server...")
			conn, session, err := svr.login()
			if err != nil {
				xl.Warn("reconnect to server error: %v, wait %v for another retry", err, delayTime)
				util.RandomSleep(delayTime, 0.9, 1.1)

				delayTime = delayTime * 2
				if delayTime > maxDelayTime {
					delayTime = maxDelayTime
				}
				continue
			}
			// reconnect success, init delayTime
			delayTime = time.Second

			// 重连成功后重新创建 Control，并重新加载代理配置和 visitor 配置。
			ctl := NewControl(svr.ctx, svr.runID, conn, session, svr.cfg, svr.pxyCfgs, svr.visitorCfgs, svr.serverUDPPort, svr.authSetter)
			ctl.Run()
			svr.ctlMu.Lock()
			if svr.ctl != nil {
				svr.ctl.Close()
			}
			svr.ctl = ctl
			svr.ctlMu.Unlock()
			break
		}
	}
}

// login 创建到 frps 的控制连接，并发送 msg.Login 完成客户端登录。
// 调用位置：client/service.go:Run() 首次登录；client/service.go:keepControllerWorking() 重连时再次调用。
// 服务端接收位置：server/service.go:handleConnection() 读取 msg.Login 后调用 RegisterControl()。
// conn: control connection
// session: if it's not nil, using tcp mux
func (svr *Service) login() (conn net.Conn, session *fmux.Session, err error) {
	xl := xlog.FromContextSafe(svr.ctx)
	var tlsConfig *tls.Config
	if svr.cfg.TLSEnable {
		sn := svr.cfg.TLSServerName
		if sn == "" {
			sn = svr.cfg.ServerAddr
		}

		tlsConfig, err = transport.NewClientTLSConfig(
			svr.cfg.TLSCertFile,
			svr.cfg.TLSKeyFile,
			svr.cfg.TLSTrustedCaFile,
			sn)
		if err != nil {
			xl.Warn("fail to build tls configuration when service login, err: %v", err)
			return
		}
	}

	proxyType, addr, auth, err := libdial.ParseProxyURL(svr.cfg.HTTPProxy)
	if err != nil {
		xl.Error("fail to parse proxy url")
		return
	}
	dialOptions := []libdial.DialOption{}
	protocol := svr.cfg.Protocol
	if protocol == "websocket" {
		protocol = "tcp"
		dialOptions = append(dialOptions, libdial.WithAfterHook(libdial.AfterHook{Hook: frpNet.DialHookWebsocket()}))
	}
	if svr.cfg.ConnectServerLocalIP != "" {
		dialOptions = append(dialOptions, libdial.WithLocalAddr(svr.cfg.ConnectServerLocalIP))
	}
	dialOptions = append(dialOptions,
		libdial.WithProtocol(protocol),
		libdial.WithTimeout(time.Duration(svr.cfg.DialServerTimeout)*time.Second),
		libdial.WithKeepAlive(time.Duration(svr.cfg.DialServerKeepAlive)*time.Second),
		libdial.WithProxy(proxyType, addr),
		libdial.WithProxyAuth(auth),
		libdial.WithTLSConfig(tlsConfig),
		libdial.WithAfterHook(libdial.AfterHook{
			Hook: frpNet.DialHookCustomTLSHeadByte(tlsConfig != nil, svr.cfg.DisableCustomTLSFirstByte),
		}),
	)
	conn, err = libdial.Dial(
		net.JoinHostPort(svr.cfg.ServerAddr, strconv.Itoa(svr.cfg.ServerPort)),
		dialOptions...,
	)
	if err != nil {
		return
	}

	defer func() {
		if err != nil {
			conn.Close()
			if session != nil {
				session.Close()
			}
		}
	}()

	if svr.cfg.TCPMux {
		fmuxCfg := fmux.DefaultConfig()
		fmuxCfg.KeepAliveInterval = time.Duration(svr.cfg.TCPMuxKeepaliveInterval) * time.Second
		fmuxCfg.LogOutput = io.Discard
		session, err = fmux.Client(conn, fmuxCfg)
		if err != nil {
			return
		}
		stream, errRet := session.OpenStream()
		if errRet != nil {
			session.Close()
			err = errRet
			return
		}
		conn = stream
	}

	loginMsg := &msg.Login{
		Arch:      runtime.GOARCH,
		Os:        runtime.GOOS,
		PoolCount: svr.cfg.PoolCount,
		User:      svr.cfg.User,
		Version:   version.Full(),
		Timestamp: time.Now().Unix(),
		RunID:     svr.runID,
		Metas:     svr.cfg.Metas,
	}

	// Add auth
	if err = svr.authSetter.SetLogin(loginMsg); err != nil {
		return
	}

	if err = msg.WriteMsg(conn, loginMsg); err != nil {
		return
	}

	var loginRespMsg msg.LoginResp
	conn.SetReadDeadline(time.Now().Add(10 * time.Second))
	if err = msg.ReadMsgInto(conn, &loginRespMsg); err != nil {
		return
	}
	conn.SetReadDeadline(time.Time{})

	if loginRespMsg.Error != "" {
		err = fmt.Errorf("%s", loginRespMsg.Error)
		xl.Error("%s", loginRespMsg.Error)
		return
	}

	svr.runID = loginRespMsg.RunID
	xl.ResetPrefixes()
	xl.AppendPrefix(svr.runID)

	svr.serverUDPPort = loginRespMsg.ServerUDPPort
	xl.Info("login to server success, get run id [%s], server udp port [%d]", loginRespMsg.RunID, loginRespMsg.ServerUDPPort)
	return
}

func (svr *Service) ReloadConf(pxyCfgs map[string]config.ProxyConf, visitorCfgs map[string]config.VisitorConf) error {
	svr.cfgMu.Lock()
	svr.pxyCfgs = pxyCfgs
	svr.visitorCfgs = visitorCfgs
	svr.cfgMu.Unlock()

	svr.ctlMu.RLock()
	ctl := svr.ctl
	svr.ctlMu.RUnlock()

	if ctl != nil {
		return svr.ctl.ReloadConf(pxyCfgs, visitorCfgs)
	}
	return nil
}

func (svr *Service) Close() {
	svr.GracefulClose(time.Duration(0))
}

func (svr *Service) GracefulClose(d time.Duration) {
	atomic.StoreUint32(&svr.exit, 1)

	svr.ctlMu.RLock()
	if svr.ctl != nil {
		svr.ctl.GracefulClose(d)
	}
	svr.ctlMu.RUnlock()

	svr.cancel()
}
