package ports

import (
	"errors"
	"net"
	"strconv"
	"sync"
	"time"
)

const (
	MinPort                    = 1
	MaxPort                    = 65535
	MaxPortReservedDuration    = time.Duration(24) * time.Hour
	CleanReservedPortsInterval = time.Hour
)

var (
	ErrPortAlreadyUsed = errors.New("port already used")
	ErrPortNotAllowed  = errors.New("port not allowed")
	ErrPortUnAvailable = errors.New("port unavailable")
	ErrNoAvailablePort = errors.New("no available port")
)

type PortCtx struct {
	ProxyName  string
	Port       int
	Closed     bool
	UpdateTime time.Time
}

type Manager struct {
	// Manager 负责 frps 侧 TCP/UDP 代理端口的申请、释放和保留。
	// 创建位置：server/service.go:NewService()，分别创建 TCPPortManager 和 UDPPortManager。
	// 使用位置：server/proxy/tcp.go、server/proxy/udp.go 以及 server/group/tcp.go。
	reservedPorts map[string]*PortCtx
	usedPorts     map[int]*PortCtx
	freePorts     map[int]struct{}

	bindAddr string
	netType  string
	mu       sync.Mutex
}

func NewManager(netType string, bindAddr string, allowPorts map[int]struct{}) *Manager {
	// allowPorts 为空时允许 1-65535；非空时只允许配置中的端口。
	// cleanReservedPortsWorker() 会定期清理长时间未使用的保留端口。
	pm := &Manager{
		reservedPorts: make(map[string]*PortCtx),
		usedPorts:     make(map[int]*PortCtx),
		freePorts:     make(map[int]struct{}),
		bindAddr:      bindAddr,
		netType:       netType,
	}
	if len(allowPorts) > 0 {
		for port := range allowPorts {
			pm.freePorts[port] = struct{}{}
		}
	} else {
		for i := MinPort; i <= MaxPort; i++ {
			pm.freePorts[i] = struct{}{}
		}
	}
	go pm.cleanReservedPortsWorker()
	return pm
}

func (pm *Manager) Acquire(name string, port int) (realPort int, err error) {
	// 调用位置：服务端具体代理 Run() 方法申请公网端口。
	// port=0 表示随机端口；指定端口时会检查 allow_ports、是否已占用以及系统是否可监听。
	portCtx := &PortCtx{
		ProxyName:  name,
		Closed:     false,
		UpdateTime: time.Now(),
	}

	var ok bool

	pm.mu.Lock()
	defer func() {
		if err == nil {
			portCtx.Port = realPort
		}
		pm.mu.Unlock()
	}()

	// check reserved ports first
	if port == 0 {
		if ctx, ok := pm.reservedPorts[name]; ok {
			if pm.isPortAvailable(ctx.Port) {
				realPort = ctx.Port
				pm.usedPorts[realPort] = portCtx
				pm.reservedPorts[name] = portCtx
				delete(pm.freePorts, realPort)
				return
			}
		}
	}

	if port == 0 {
		// get random port
		count := 0
		maxTryTimes := 5
		for k := range pm.freePorts {
			count++
			if count > maxTryTimes {
				break
			}
			if pm.isPortAvailable(k) {
				realPort = k
				pm.usedPorts[realPort] = portCtx
				pm.reservedPorts[name] = portCtx
				delete(pm.freePorts, realPort)
				break
			}
		}
		if realPort == 0 {
			err = ErrNoAvailablePort
		}
	} else {
		// specified port
		if _, ok = pm.freePorts[port]; ok {
			if pm.isPortAvailable(port) {
				realPort = port
				pm.usedPorts[realPort] = portCtx
				pm.reservedPorts[name] = portCtx
				delete(pm.freePorts, realPort)
			} else {
				err = ErrPortUnAvailable
			}
		} else {
			if _, ok = pm.usedPorts[port]; ok {
				err = ErrPortAlreadyUsed
			} else {
				err = ErrPortNotAllowed
			}
		}
	}
	return
}

func (pm *Manager) isPortAvailable(port int) bool {
	if pm.netType == "udp" {
		addr, err := net.ResolveUDPAddr("udp", net.JoinHostPort(pm.bindAddr, strconv.Itoa(port)))
		if err != nil {
			return false
		}
		l, err := net.ListenUDP("udp", addr)
		if err != nil {
			return false
		}
		l.Close()
		return true
	}

	l, err := net.Listen(pm.netType, net.JoinHostPort(pm.bindAddr, strconv.Itoa(port)))
	if err != nil {
		return false
	}
	l.Close()
	return true
}

func (pm *Manager) Release(port int) {
	// 调用位置：服务端代理关闭时释放端口，例如 server/proxy/tcp.go 的 Close 流程。
	// 释放后端口回到 freePorts，同时记录 reservedPorts，便于同名代理短时间内复用随机端口。
	pm.mu.Lock()
	defer pm.mu.Unlock()
	if ctx, ok := pm.usedPorts[port]; ok {
		pm.freePorts[port] = struct{}{}
		delete(pm.usedPorts, port)
		ctx.Closed = true
		ctx.UpdateTime = time.Now()
	}
}

// Release reserved port if it isn't used in last 24 hours.
func (pm *Manager) cleanReservedPortsWorker() {
	for {
		time.Sleep(CleanReservedPortsInterval)
		pm.mu.Lock()
		for name, ctx := range pm.reservedPorts {
			if ctx.Closed && time.Since(ctx.UpdateTime) > MaxPortReservedDuration {
				delete(pm.reservedPorts, name)
			}
		}
		pm.mu.Unlock()
	}
}
