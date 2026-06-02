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
	"io"
	"net"
	"runtime/debug"
	"strconv"
	"sync"
	"time"

	"github.com/fatedier/frp/client/proxy"
	"github.com/fatedier/frp/pkg/auth"
	"github.com/fatedier/frp/pkg/config"
	"github.com/fatedier/frp/pkg/msg"
	"github.com/fatedier/frp/pkg/transport"
	frpNet "github.com/fatedier/frp/pkg/util/net"
	"github.com/fatedier/frp/pkg/util/xlog"

	"github.com/fatedier/golib/control/shutdown"
	"github.com/fatedier/golib/crypto"
	libdial "github.com/fatedier/golib/net/dial"
	fmux "github.com/hashicorp/yamux"
)

type Control struct {
	// uniq id got from frps, attach it in loginMsg
	runID string

	// manage all proxies
	pxyCfgs map[string]config.ProxyConf
	pm      *proxy.Manager

	// manage all visitors
	vm *VisitorManager

	// control connection
	conn net.Conn

	// tcp stream multiplexing, if enabled
	session *fmux.Session

	// put a message in this channel to send it over control connection to server
	sendCh chan (msg.Message)

	// read from this channel to get the next message sent by server
	readCh chan (msg.Message)

	// goroutines can block by reading from this channel, it will be closed only in reader() when control connection is closed
	closedCh chan struct{}

	closedDoneCh chan struct{}

	// last time got the Pong message
	lastPong time.Time

	// The client configuration
	clientCfg config.ClientCommonConf

	readerShutdown     *shutdown.Shutdown
	writerShutdown     *shutdown.Shutdown
	msgHandlerShutdown *shutdown.Shutdown

	// The UDP port that the server is listening on
	serverUDPPort int

	mu sync.RWMutex

	xl *xlog.Logger

	// service context
	ctx context.Context

	// sets authentication based on selected method
	authSetter auth.Setter
}

func NewControl(ctx context.Context, runID string, conn net.Conn, session *fmux.Session,
	clientCfg config.ClientCommonConf,
	pxyCfgs map[string]config.ProxyConf,
	visitorCfgs map[string]config.VisitorConf,
	serverUDPPort int,
	authSetter auth.Setter) *Control {

	// new xlog instance
	ctl := &Control{
		runID:              runID,
		conn:               conn,
		session:            session,
		pxyCfgs:            pxyCfgs,
		sendCh:             make(chan msg.Message, 100),
		readCh:             make(chan msg.Message, 100),
		closedCh:           make(chan struct{}),
		closedDoneCh:       make(chan struct{}),
		clientCfg:          clientCfg,
		readerShutdown:     shutdown.New(),
		writerShutdown:     shutdown.New(),
		msgHandlerShutdown: shutdown.New(),
		serverUDPPort:      serverUDPPort,
		xl:                 xlog.FromContextSafe(ctx),
		ctx:                ctx,
		authSetter:         authSetter,
	}
	// 代理管理器负责把本地代理配置转换为 NewProxy/CloseProxy 消息。
	// 具体实现见 client/proxy/proxy_manager.go:NewManager()。
	ctl.pm = proxy.NewManager(ctl.ctx, ctl.sendCh, clientCfg, serverUDPPort)

	// visitor 用于 stcp/xtcp/sudp 等私密访问模式，配置加载见 client/visitor_manager.go:Reload()。
	ctl.vm = NewVisitorManager(ctl.ctx, ctl)
	ctl.vm.Reload(visitorCfgs)
	return ctl
}

func (ctl *Control) Run() {
	// worker 启动 reader/writer/msgHandler 三个协程，负责控制连接上的消息收发。
	go ctl.worker()

	// start all proxies
	// 代理配置由 client/proxy/proxy_manager.go:Reload() 转换为 Wrapper，并触发 NewProxy 注册。
	ctl.pm.Reload(ctl.pxyCfgs)

	// start all visitors
	// visitor 配置由 client/visitor_manager.go:Run() 监听本地访问请求。
	go ctl.vm.Run()
	return
}

func (ctl *Control) HandleReqWorkConn(inMsg *msg.ReqWorkConn) {
	xl := ctl.xl
	// frps 在 server/control.go:GetWorkConn() 中需要工作连接时，会通过控制连接发送 ReqWorkConn。
	// frpc 收到后调用 connectServer() 新建一条连接，并发送 msg.NewWorkConn 绑定到当前 runID。
	workConn, err := ctl.connectServer()
	if err != nil {
		return
	}

	m := &msg.NewWorkConn{
		RunID: ctl.runID,
	}
	if err = ctl.authSetter.SetNewWorkConn(m); err != nil {
		xl.Warn("error during NewWorkConn authentication: %v", err)
		return
	}
	if err = msg.WriteMsg(workConn, m); err != nil {
		xl.Warn("work connection write to server error: %v", err)
		workConn.Close()
		return
	}

	var startMsg msg.StartWorkConn
	if err = msg.ReadMsgInto(workConn, &startMsg); err != nil {
		xl.Error("work connection closed before response StartWorkConn message: %v", err)
		workConn.Close()
		return
	}
	if startMsg.Error != "" {
		xl.Error("StartWorkConn contains error: %s", startMsg.Error)
		workConn.Close()
		return
	}

	// dispatch this work connection to related proxy
	// StartWorkConn.ProxyName 决定这条工作连接交给哪个本地代理处理。
	// 分发入口：client/proxy/proxy_manager.go:HandleWorkConn()。
	ctl.pm.HandleWorkConn(startMsg.ProxyName, workConn, &startMsg)
}

func (ctl *Control) HandleNewProxyResp(inMsg *msg.NewProxyResp) {
	xl := ctl.xl
	// Server will return NewProxyResp message to each NewProxy message.
	// Start a new proxy handler if no error got
	// 对应服务端返回位置：server/control.go:manager() 注册代理后写入 msg.NewProxyResp。
	err := ctl.pm.StartProxy(inMsg.ProxyName, inMsg.RemoteAddr, inMsg.Error)
	if err != nil {
		xl.Warn("[%s] start error: %v", inMsg.ProxyName, err)
	} else {
		xl.Info("[%s] start proxy success", inMsg.ProxyName)
	}
}

func (ctl *Control) Close() error {
	return ctl.GracefulClose(0)
}

func (ctl *Control) GracefulClose(d time.Duration) error {
	ctl.pm.Close()
	ctl.vm.Close()

	time.Sleep(d)

	ctl.conn.Close()
	if ctl.session != nil {
		ctl.session.Close()
	}
	return nil
}

// ClosedDoneCh returns a channel which will be closed after all resources are released
func (ctl *Control) ClosedDoneCh() <-chan struct{} {
	return ctl.closedDoneCh
}

// connectServer return a new connection to frps
// 调用位置：client/control.go:HandleReqWorkConn() 创建工作连接。
// 当 tcp_mux=true 时复用 login() 建立的 yamux session；否则重新拨号到 frps。
func (ctl *Control) connectServer() (conn net.Conn, err error) {
	xl := ctl.xl
	if ctl.clientCfg.TCPMux {
		stream, errRet := ctl.session.OpenStream()
		if errRet != nil {
			err = errRet
			xl.Warn("start new connection to server error: %v", err)
			return
		}
		conn = stream
	} else {
		var tlsConfig *tls.Config
		sn := ctl.clientCfg.TLSServerName
		if sn == "" {
			sn = ctl.clientCfg.ServerAddr
		}

		if ctl.clientCfg.TLSEnable {
			tlsConfig, err = transport.NewClientTLSConfig(
				ctl.clientCfg.TLSCertFile,
				ctl.clientCfg.TLSKeyFile,
				ctl.clientCfg.TLSTrustedCaFile,
				sn)

			if err != nil {
				xl.Warn("fail to build tls configuration when connecting to server, err: %v", err)
				return
			}
		}

		proxyType, addr, auth, err := libdial.ParseProxyURL(ctl.clientCfg.HTTPProxy)
		if err != nil {
			xl.Error("fail to parse proxy url")
			return nil, err
		}
		dialOptions := []libdial.DialOption{}
		protocol := ctl.clientCfg.Protocol
		if protocol == "websocket" {
			protocol = "tcp"
			dialOptions = append(dialOptions, libdial.WithAfterHook(libdial.AfterHook{Hook: frpNet.DialHookWebsocket()}))
		}
		if ctl.clientCfg.ConnectServerLocalIP != "" {
			dialOptions = append(dialOptions, libdial.WithLocalAddr(ctl.clientCfg.ConnectServerLocalIP))
		}
		dialOptions = append(dialOptions,
			libdial.WithProtocol(protocol),
			libdial.WithTimeout(time.Duration(ctl.clientCfg.DialServerTimeout)*time.Second),
			libdial.WithKeepAlive(time.Duration(ctl.clientCfg.DialServerKeepAlive)*time.Second),
			libdial.WithProxy(proxyType, addr),
			libdial.WithProxyAuth(auth),
			libdial.WithTLSConfig(tlsConfig),
			libdial.WithAfterHook(libdial.AfterHook{
				Hook: frpNet.DialHookCustomTLSHeadByte(tlsConfig != nil, ctl.clientCfg.DisableCustomTLSFirstByte),
			}),
		)
		conn, err = libdial.Dial(
			net.JoinHostPort(ctl.clientCfg.ServerAddr, strconv.Itoa(ctl.clientCfg.ServerPort)),
			dialOptions...,
		)
		if err != nil {
			xl.Warn("start new connection to server error: %v", err)
			return nil, err
		}
	}
	return
}

// reader read all messages from frps and send to readCh
// 数据来源：server/control.go:writer() 写出的控制消息。
// 读取到的消息统一进入 readCh，由 client/control.go:msgHandler() 按类型处理。
func (ctl *Control) reader() {
	xl := ctl.xl
	defer func() {
		if err := recover(); err != nil {
			xl.Error("panic error: %v", err)
			xl.Error(string(debug.Stack()))
		}
	}()
	defer ctl.readerShutdown.Done()
	defer close(ctl.closedCh)

	encReader := crypto.NewReader(ctl.conn, []byte(ctl.clientCfg.Token))
	for {
		m, err := msg.ReadMsg(encReader)
		if err != nil {
			if err == io.EOF {
				xl.Debug("read from control connection EOF")
				return
			}
			xl.Warn("read error: %v", err)
			ctl.conn.Close()
			return
		}
		ctl.readCh <- m
	}
}

// writer writes messages got from sendCh to frps
// sendCh 的写入者包括 client/proxy/proxy_manager.go:HandleEvent() 和 msgHandler() 心跳逻辑。
// 服务端读取位置：server/control.go:reader()。
func (ctl *Control) writer() {
	xl := ctl.xl
	defer ctl.writerShutdown.Done()
	encWriter, err := crypto.NewWriter(ctl.conn, []byte(ctl.clientCfg.Token))
	if err != nil {
		xl.Error("crypto new writer error: %v", err)
		ctl.conn.Close()
		return
	}
	for {
		m, ok := <-ctl.sendCh
		if !ok {
			xl.Info("control writer is closing")
			return
		}

		if err := msg.WriteMsg(encWriter, m); err != nil {
			xl.Warn("write message to control connection error: %v", err)
			return
		}
	}
}

// msgHandler handles all channel events and do corresponding operations.
// 这里是 frpc 控制消息分发中心：
// - ReqWorkConn：服务端需要工作连接，调用 HandleReqWorkConn()
// - NewProxyResp：服务端代理注册结果，调用 HandleNewProxyResp()
// - Pong：服务端心跳响应，刷新 lastPong
func (ctl *Control) msgHandler() {
	xl := ctl.xl
	defer func() {
		if err := recover(); err != nil {
			xl.Error("panic error: %v", err)
			xl.Error(string(debug.Stack()))
		}
	}()
	defer ctl.msgHandlerShutdown.Done()

	var hbSendCh <-chan time.Time
	// TODO(fatedier): disable heartbeat if TCPMux is enabled.
	// Just keep it here to keep compatible with old version frps.
	if ctl.clientCfg.HeartbeatInterval > 0 {
		hbSend := time.NewTicker(time.Duration(ctl.clientCfg.HeartbeatInterval) * time.Second)
		defer hbSend.Stop()
		hbSendCh = hbSend.C
	}

	var hbCheckCh <-chan time.Time
	// Check heartbeat timeout only if TCPMux is not enabled and users don't disable heartbeat feature.
	if ctl.clientCfg.HeartbeatInterval > 0 && ctl.clientCfg.HeartbeatTimeout > 0 && !ctl.clientCfg.TCPMux {
		hbCheck := time.NewTicker(time.Second)
		defer hbCheck.Stop()
		hbCheckCh = hbCheck.C
	}

	ctl.lastPong = time.Now()
	for {
		select {
		case <-hbSendCh:
			// send heartbeat to server
			xl.Debug("send heartbeat to server")
			pingMsg := &msg.Ping{}
			if err := ctl.authSetter.SetPing(pingMsg); err != nil {
				xl.Warn("error during ping authentication: %v", err)
				return
			}
			ctl.sendCh <- pingMsg
		case <-hbCheckCh:
			if time.Since(ctl.lastPong) > time.Duration(ctl.clientCfg.HeartbeatTimeout)*time.Second {
				xl.Warn("heartbeat timeout")
				// let reader() stop
				ctl.conn.Close()
				return
			}
		case rawMsg, ok := <-ctl.readCh:
			if !ok {
				return
			}

			switch m := rawMsg.(type) {
			case *msg.ReqWorkConn:
				go ctl.HandleReqWorkConn(m)
			case *msg.NewProxyResp:
				ctl.HandleNewProxyResp(m)
			case *msg.Pong:
				if m.Error != "" {
					xl.Error("Pong contains error: %s", m.Error)
					ctl.conn.Close()
					return
				}
				ctl.lastPong = time.Now()
				xl.Debug("receive heartbeat from server")
			}
		}
	}
}

// If controler is notified by closedCh, reader and writer and handler will exit
// closedCh 由 reader() 在控制连接断开时关闭；worker 随后关闭代理、visitor 和 yamux session。
// client/service.go:keepControllerWorking() 会等待 ClosedDoneCh() 后发起重连。
func (ctl *Control) worker() {
	go ctl.msgHandler()
	go ctl.reader()
	go ctl.writer()

	select {
	case <-ctl.closedCh:
		// close related channels and wait until other goroutines done
		close(ctl.readCh)
		ctl.readerShutdown.WaitDone()
		ctl.msgHandlerShutdown.WaitDone()

		close(ctl.sendCh)
		ctl.writerShutdown.WaitDone()

		ctl.pm.Close()
		ctl.vm.Close()

		close(ctl.closedDoneCh)
		if ctl.session != nil {
			ctl.session.Close()
		}
		return
	}
}

func (ctl *Control) ReloadConf(pxyCfgs map[string]config.ProxyConf, visitorCfgs map[string]config.VisitorConf) error {
	ctl.vm.Reload(visitorCfgs)
	ctl.pm.Reload(pxyCfgs)
	return nil
}
