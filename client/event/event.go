package event

import (
	"errors"

	"github.com/fatedier/frp/pkg/msg"
)

type Type int

const (
	EvStartProxy Type = iota
	EvCloseProxy
)

var (
	ErrPayloadType = errors.New("error payload type")
)

// Handler 的实现位置：client/proxy/proxy_manager.go:HandleEvent()。
// Wrapper 通过该回调把“启动代理/关闭代理”事件转换为控制连接消息。
type Handler func(evType Type, payload interface{}) error

type StartProxyPayload struct {
	NewProxyMsg *msg.NewProxy
}

type CloseProxyPayload struct {
	CloseProxyMsg *msg.CloseProxy
}
