# NSSH - SSH 反向隧道客户端

NSSH 是一个用 Go 编写的轻量级 SSH 反向隧道客户端。它能够将位于 NAT 或防火墙后的本地服务，通过 SSH 服务器暴露到公网。

## 功能特性

- **SSH 反向隧道**（`ssh -R` 等价实现）
- **自动重连** — 可配置延迟，断线自动重连
- **心跳保活** — 定期发送心跳检测连接状态
- **守护进程模式** — 后台运行，管理多个隧道
- **多认证方式** — 密码认证和 SSH 密钥认证
- **跨平台** — 支持 Linux、macOS、Windows、FreeBSD
- **轻量高效** — 体积小、内存占用低

## 快速开始

### 编译

```bash
go build -o nssh main.go
```

### 启动隧道

```bash
# 密码认证
nssh -R 8080:localhost:80 user@your-server.com -p 22 --passwd your_password

# SSH 密钥认证
nssh -R 8080:localhost:80 user@your-server.com -p 22 -i ~/.ssh/id_rsa
```

上述命令创建了一个反向隧道：`your-server.com:8080 → localhost:80`

### 守护进程模式（后台运行）

```bash
nssh --daemon -R 8080:localhost:80 user@your-server.com -p 22 --passwd your_password
```

### 管理隧道

```bash
nssh --list                    # 列出所有运行中的隧道
nssh --stop user@server        # 停止指定隧道
nssh --stop-all                # 停止所有隧道
nssh --restart user@server     # 重启指定隧道
nssh --log user@server         # 查看隧道日志
```

## 安装方式

### 源码编译

```bash
git clone https://github.com/Vrolist/nssh.git
cd nssh
go build -o nssh main.go
sudo cp nssh /usr/local/bin/
```

### Docker 部署

```bash
docker build -t nssh -f docker/Dockerfile .
docker run -d --name nssh_tunnel --restart=unless-stopped \
  -e REMOTE_PORT=8080 \
  -e LOCAL_HOST=127.0.0.1 \
  -e LOCAL_PORT=80 \
  -e USERNAME=user \
  -e SERVER_NODE=your-server.com \
  -e SERVER_PORT=22 \
  -e PASSWORD=your_password \
  nssh
```

## 配置说明

### 命令行参数

| 参数 | 简写 | 说明 |
|------|------|------|
| `--remote` | `-R` | 反向隧道：`remote_port:local_host:local_port` |
| `--port` | `-p` | SSH 服务器端口 |
| `--passwd` | `-P` | SSH 密码 |
| `--identity` | `-i` | SSH 私钥路径 |
| `--reconnect` | `-r` | 重连延迟（秒，默认 30） |
| `--daemon` | | 后台守护进程模式 |
| `--list` | | 列出所有隧道 |
| `--stop` | | 停止指定隧道 |
| `--stop-all` | | 停止所有隧道 |
| `--restart` | | 重启隧道 |
| `--log` | | 查看隧道日志 |
| `--version` | `-v` | 显示版本号 |

### 环境变量

| 变量名 | 说明 | 默认值 |
|--------|------|--------|
| `SERVER_HOST` | SSH 服务器地址 | - |
| `SERVER_PORT` | SSH 服务器端口 | `20022` |
| `USERNAME` | SSH 用户名 | - |
| `PASSWORD` | SSH 密码 | - |
| `SSH_KEY` | SSH 私钥路径 | - |
| `LOCAL_PORT` | 本地服务端口 | `80` |
| `REMOTE_PORT` | 远程监听端口 | `8000` |
| `RECONNECT_DELAY` | 重连延迟（秒） | `30` |

## 架构设计

```
┌──────────────────────────────────────────────┐
│  CLI (main.go)                                │
│  解析参数 → 派发到守护进程                      │
└──────────────────────┬───────────────────────┘
                       │ Unix socket / 命名管道
┌──────────────────────▼───────────────────────┐
│  守护进程 (daemon/)                           │
│  后台进程，管理多个 Worker                     │
│  - 启动/停止/重启隧道                          │
│  - 状态查询、日志查看                           │
│  - 断线自动重连                                │
└──────────────────────┬───────────────────────┘
                       │ 创建子进程
         ┌─────────────┼─────────────┐
         ▼             ▼             ▼
    Worker 1        Worker 2     Worker N
  (user@svr:8080) (user@svr:8081)
```

## 跨平台编译

```bash
# 编译当前平台
go build -o nssh main.go

# Linux
GOOS=linux GOARCH=amd64 go build -o nssh_linux_amd64 main.go
GOOS=linux GOARCH=arm64 go build -o nssh_linux_arm64 main.go

# macOS
GOOS=darwin GOARCH=amd64 go build -o nssh_darwin_amd64 main.go
GOOS=darwin GOARCH=arm64 go build -o nssh_darwin_arm64 main.go

# Windows
GOOS=windows GOARCH=amd64 go build -o nssh_windows_amd64.exe main.go

# FreeBSD
GOOS=freebsd GOARCH=amd64 go build -o nssh_freebsd_amd64 main.go
```

## 许可证

MIT
