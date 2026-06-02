// Copyright 2018 fatedier, fatedier@gmail.com
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

package msg

import (
	"io"

	jsonMsg "github.com/fatedier/golib/msg/json"
)

type Message = jsonMsg.Message

var (
	msgCtl *jsonMsg.MsgCtl
)

func init() {
	// 注册所有 frp 消息类型与类型字节的映射。
	// msg.ReadMsg()/WriteMsg() 依赖这里完成控制连接和工作连接上的序列化/反序列化。
	msgCtl = jsonMsg.NewMsgCtl()
	for typeByte, msg := range msgTypeMap {
		msgCtl.RegisterMsg(typeByte, msg)
	}
}

func ReadMsg(c io.Reader) (msg Message, err error) {
	// 调用位置包括 client/control.go:reader()、server/control.go:reader()、server/service.go:handleConnection()。
	return msgCtl.ReadMsg(c)
}

func ReadMsgInto(c io.Reader, msg Message) (err error) {
	// 用于已知消息类型的读取，例如 client/control.go:HandleReqWorkConn() 读取 StartWorkConn。
	return msgCtl.ReadMsgInto(c, msg)
}

func WriteMsg(c io.Writer, msg interface{}) (err error) {
	// 调用位置包括 client/control.go:writer()、server/control.go:writer() 和各类工作连接握手逻辑。
	return msgCtl.WriteMsg(c, msg)
}
