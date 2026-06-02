# frp 0.43.0 学习与使用说明

frp 是一个高性能的反向代理应用，常用于内网穿透。它可以把处在 NAT 或防火墙后的本地服务，通过一台拥有公网 IP 的服务器暴露到公网，支持 TCP、UDP、HTTP、HTTPS、STCP、XTCP 等多种代理方式。

本仓库基于 `frp 0.43.0`，适合用于学习 Go 语言网络编程、反向代理、配置解析、客户端/服务端通信和内网穿透实现原理。

## 项目结构

```text
.
+-- cmd/              # frps、frpc 等命令入口
+-- client/           # 客户端核心逻辑
+-- server/           # 服务端核心逻辑
+-- pkg/              # 公共包、配置、工具、协议、认证等
+-- conf/             # 示例配置文件
+-- doc/              # 文档和图片资源
+-- test/             # 测试相关代码
+-- web/              # 管理界面相关资源
+-- go.mod            # Go 模块依赖
+-- Makefile          # 构建脚本
```

## 快速理解 frp

frp 由两个主要程序组成：

- `frps`：服务端，部署在有公网 IP 的机器上，负责接收外部访问并转发流量。
- `frpc`：客户端，部署在内网机器上，负责连接 `frps`，并把本地服务映射出去。

典型流程：

1. 公网服务器启动 `frps`。
2. 内网机器启动 `frpc`，主动连接 `frps`。
3. 外部用户访问 `frps` 暴露的端口或域名。
4. `frps` 将流量通过已建立的连接转发给 `frpc`。
5. `frpc` 再把流量转发到本地真实服务。

## 环境准备

建议使用 Go 1.16 或兼容版本。本项目的 `go.mod` 中声明：

```text
go 1.16
```

查看 Go 版本：

```bash
go version
```

下载依赖：

```bash
go mod download
```

## 编译项目

在项目根目录执行：

```bash
go build -o bin/frps ./cmd/frps
go build -o bin/frpc ./cmd/frpc
```

Windows 下也可以编译为：

```powershell
go build -o bin/frps.exe ./cmd/frps
go build -o bin/frpc.exe ./cmd/frpc
```

如果使用 Makefile：

```bash
make
```

## 基础使用示例

### 1. 服务端配置

在公网服务器上创建 `frps.ini`：

```ini
[common]
bind_port = 7000
```

启动服务端：

```bash
./frps -c ./frps.ini
```

Windows：

```powershell
.\frps.exe -c .\frps.ini
```

### 2. 客户端配置

假设内网机器上有一个 SSH 服务监听 `127.0.0.1:22`，创建 `frpc.ini`：

```ini
[common]
server_addr = 公网服务器IP
server_port = 7000

[ssh]
type = tcp
local_ip = 127.0.0.1
local_port = 22
remote_port = 6000
```

启动客户端：

```bash
./frpc -c ./frpc.ini
```

Windows：

```powershell
.\frpc.exe -c .\frpc.ini
```

外部机器即可通过下面的方式访问内网 SSH：

```bash
ssh -p 6000 user@公网服务器IP
```

## 常见配置说明

### TCP 转发

```ini
[tcp-demo]
type = tcp
local_ip = 127.0.0.1
local_port = 8080
remote_port = 8088
```

表示把内网 `127.0.0.1:8080` 映射到服务端的 `8088` 端口。

### HTTP 域名转发

服务端配置：

```ini
[common]
bind_port = 7000
vhost_http_port = 80
```

客户端配置：

```ini
[web]
type = http
local_port = 8080
custom_domains = example.com
```

访问 `http://example.com` 时，流量会转发到客户端本地的 `8080` 服务。

### Token 认证

服务端和客户端配置相同的 `token`，可以避免未授权客户端接入：

```ini
[common]
bind_port = 7000
token = your_token_here
```

客户端：

```ini
[common]
server_addr = 公网服务器IP
server_port = 7000
token = your_token_here
```

## 源码学习路线

建议按下面顺序阅读代码：

1. `cmd/frps` 和 `cmd/frpc`

   先看命令行入口，理解程序如何读取参数、加载配置并启动服务。

2. `pkg/config`

   学习配置文件解析逻辑，理解 `frps.ini` 和 `frpc.ini` 如何转换为 Go 结构体。

3. `server`

   阅读服务端启动流程、监听端口、客户端连接管理、代理注册和流量转发逻辑。

4. `client`

   阅读客户端如何连接服务端、创建代理、维护控制连接和工作连接。

5. `pkg/msg`、`pkg/proto`、`pkg/util`

   理解客户端和服务端之间的消息协议、网络封装和通用工具函数。

6. `pkg/auth`

   学习 token、OIDC 等认证相关实现。

## 推荐调试方式

可以准备两份最小配置文件，一份启动 `frps`，一份启动 `frpc`，然后在本机或两台机器上分别运行。

服务端：

```bash
go run ./cmd/frps -c ./conf/frps.ini
```

客户端：

```bash
go run ./cmd/frpc -c ./conf/frpc.ini
```

如果需要观察调用链，可以在关键位置添加日志，例如：

- 服务端接受连接的位置
- 客户端登录服务端的位置
- 代理注册的位置
- 工作连接创建的位置
- 流量转发的位置

## 常用命令

```bash
# 查看依赖
go list -m all

# 运行测试
go test ./...

# 编译服务端
go build -o bin/frps ./cmd/frps

# 编译客户端
go build -o bin/frpc ./cmd/frpc
```

## 学习重点

- Go 命令行程序组织方式
- INI 配置解析
- TCP/UDP 网络通信
- 反向代理模型
- 客户端和服务端长连接维护
- 多路复用和连接池
- 认证与安全配置
- HTTP/HTTPS 虚拟主机转发
- 插件化能力扩展

## 注意事项

- 不要把真实服务器 IP、密码、token、证书私钥提交到仓库。
- 在公网服务器开放端口前，请确认安全组、防火墙和认证配置。
- 学习调试时建议先使用测试端口，避免影响线上服务。
- 若用于生产环境，请优先阅读官方文档并使用稳定发布版本。

## 相关资料

- 官方项目：https://github.com/fatedier/frp
- 官方文档：https://gofrp.org/docs/
- 当前仓库：https://github.com/luchanger8-ai/frp-0.43.0.git
