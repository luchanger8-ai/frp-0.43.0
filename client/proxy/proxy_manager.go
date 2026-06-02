package proxy

import (
	"context"
	"fmt"
	"net"
	"sync"

	"github.com/fatedier/frp/client/event"
	"github.com/fatedier/frp/pkg/config"
	"github.com/fatedier/frp/pkg/msg"
	"github.com/fatedier/frp/pkg/util/xlog"

	"github.com/fatedier/golib/errors"
)

type Manager struct {
	sendCh  chan (msg.Message)
	proxies map[string]*Wrapper

	closed bool
	mu     sync.RWMutex

	clientCfg config.ClientCommonConf

	// The UDP port that the server is listening on
	serverUDPPort int

	ctx context.Context
}

func NewManager(ctx context.Context, msgSendCh chan (msg.Message), clientCfg config.ClientCommonConf, serverUDPPort int) *Manager {
	return &Manager{
		sendCh:        msgSendCh,
		proxies:       make(map[string]*Wrapper),
		closed:        false,
		clientCfg:     clientCfg,
		serverUDPPort: serverUDPPort,
		ctx:           ctx,
	}
}

func (pm *Manager) StartProxy(name string, remoteAddr string, serverRespErr string) error {
	// 调用位置：client/control.go:HandleNewProxyResp()。
	// 作用：根据 frps 返回的 NewProxyResp，把 Wrapper 状态从 wait start 推进到 running 或 start error。
	pm.mu.RLock()
	pxy, ok := pm.proxies[name]
	pm.mu.RUnlock()
	if !ok {
		return fmt.Errorf("proxy [%s] not found", name)
	}

	err := pxy.SetRunningStatus(remoteAddr, serverRespErr)
	if err != nil {
		return err
	}
	return nil
}

func (pm *Manager) Close() {
	pm.mu.Lock()
	defer pm.mu.Unlock()
	for _, pxy := range pm.proxies {
		pxy.Stop()
	}
	pm.proxies = make(map[string]*Wrapper)
}

func (pm *Manager) HandleWorkConn(name string, workConn net.Conn, m *msg.StartWorkConn) {
	// 调用位置：client/control.go:HandleReqWorkConn()。
	// 作用：把服务端分配好的工作连接交给对应代理的 Wrapper，再由 Wrapper 调用具体 Proxy.InWorkConn()。
	pm.mu.RLock()
	pw, ok := pm.proxies[name]
	pm.mu.RUnlock()
	if ok {
		pw.InWorkConn(workConn, m)
	} else {
		workConn.Close()
	}
}

func (pm *Manager) HandleEvent(evType event.Type, payload interface{}) error {
	// 调用位置：client/proxy/proxy_wrapper.go:checkWorker() 和 close()。
	// 作用：把代理生命周期事件转换为控制连接消息，写入 client/control.go:writer() 消费的 sendCh。
	var m msg.Message
	switch e := payload.(type) {
	case *event.StartProxyPayload:
		m = e.NewProxyMsg
	case *event.CloseProxyPayload:
		m = e.CloseProxyMsg
	default:
		return event.ErrPayloadType
	}

	err := errors.PanicToError(func() {
		pm.sendCh <- m
	})
	return err
}

func (pm *Manager) GetAllProxyStatus() []*WorkingStatus {
	ps := make([]*WorkingStatus, 0)
	pm.mu.RLock()
	defer pm.mu.RUnlock()
	for _, pxy := range pm.proxies {
		ps = append(ps, pxy.GetStatus())
	}
	return ps
}

func (pm *Manager) Reload(pxyCfgs map[string]config.ProxyConf) {
	// 调用位置：client/control.go:Run() 初次加载代理；client/control.go:ReloadConf() 热加载代理。
	// 作用：对比新旧配置，删除变化或不存在的代理，新增未启动的代理 Wrapper。
	xl := xlog.FromContextSafe(pm.ctx)
	pm.mu.Lock()
	defer pm.mu.Unlock()

	delPxyNames := make([]string, 0)
	for name, pxy := range pm.proxies {
		del := false
		cfg, ok := pxyCfgs[name]
		if !ok {
			del = true
		} else {
			if !pxy.Cfg.Compare(cfg) {
				del = true
			}
		}

		if del {
			delPxyNames = append(delPxyNames, name)
			delete(pm.proxies, name)

			pxy.Stop()
		}
	}
	if len(delPxyNames) > 0 {
		xl.Info("proxy removed: %v", delPxyNames)
	}

	addPxyNames := make([]string, 0)
	for name, cfg := range pxyCfgs {
		if _, ok := pm.proxies[name]; !ok {
			pxy := NewWrapper(pm.ctx, cfg, pm.clientCfg, pm.HandleEvent, pm.serverUDPPort)
			pm.proxies[name] = pxy
			addPxyNames = append(addPxyNames, name)

			pxy.Start()
		}
	}
	if len(addPxyNames) > 0 {
		xl.Info("proxy added: %v", addPxyNames)
	}
}
