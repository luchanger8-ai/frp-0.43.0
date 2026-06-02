// Copyright 2019 fatedier, fatedier@gmail.com
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

package controller

import (
	"github.com/fatedier/frp/pkg/nathole"
	plugin "github.com/fatedier/frp/pkg/plugin/server"
	"github.com/fatedier/frp/pkg/util/tcpmux"
	"github.com/fatedier/frp/pkg/util/vhost"
	"github.com/fatedier/frp/server/group"
	"github.com/fatedier/frp/server/ports"
	"github.com/fatedier/frp/server/visitor"
)

// All resource managers and controllers
// ResourceController 汇总 frps 运行期共享资源。
// 创建位置：server/service.go:NewService()。
// 使用位置：server/proxy/* 的各类代理 Run() 方法，用于申请端口、注册 vhost、管理 visitor 和插件回调。
type ResourceController struct {
	// Manage all visitor listeners
	VisitorManager *visitor.Manager

	// TCP Group Controller
	TCPGroupCtl *group.TCPGroupCtl

	// HTTP Group Controller
	HTTPGroupCtl *group.HTTPGroupController

	// TCP Mux Group Controller
	TCPMuxGroupCtl *group.TCPMuxGroupCtl

	// Manage all TCP ports
	TCPPortManager *ports.Manager

	// Manage all UDP ports
	UDPPortManager *ports.Manager

	// For HTTP proxies, forwarding HTTP requests
	HTTPReverseProxy *vhost.HTTPReverseProxy

	// For HTTPS proxies, route requests to different clients by hostname and other information
	VhostHTTPSMuxer *vhost.HTTPSMuxer

	// Controller for nat hole connections
	NatHoleController *nathole.Controller

	// TCPMux HTTP CONNECT multiplexer
	TCPMuxHTTPConnectMuxer *tcpmux.HTTPConnectTCPMuxer

	// All server manager plugin
	PluginManager *plugin.Manager
}
