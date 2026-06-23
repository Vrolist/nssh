# 内网云 NSSH Client SDK (Go)

内网云客户端SDK Go 语言版本的 SSH 反向隧道客户端 SDK，提供简单易用的 API 来建立 SSH 隧道和端口转发。

## 快速开始

### 示例代码

```go
package main

import (
    "fmt"
    "time"

    "nssh_client/sdk/sdk_go"
)

func main() {
    // 创建 SSH 隧道实例并配置参数
    tunnel := sdk_go.NewTunnel(
        "your-username",    // SSH 用户名
        "your-password",    // SSH 密码
        "your-server.com",  // SSH 服务器地址
        22,                 // SSH 服务器端口
        "127.0.0.1",        // 本地服务地址
        80,                 // 本地服务端口
        80,                 // 远程端口（外网访问端口）
    ).EnableLog(false)

    fmt.Println("Starting SSH tunnel...")
    tunnel.Start()

    // 监控状态
    go func() {
        for {
            time.Sleep(5 * time.Second)
            fmt.Printf("Status - Running:%v Connected:%v Port:%d Sent:%d Recv:%d Error:%s\n",
                tunnel.IsRunning(),
                tunnel.IsConnected(),
                tunnel.GetRemotePort(),
                tunnel.GetBytesSent(),
                tunnel.GetBytesReceived(),
                tunnel.GetLastError())
        }
    }()

    tunnel.Wait()
}
```

## 运行示例

```bash
cd examples
go run example.go
```

## 参数说明

```go
sdk_go.NewTunnel(
    username,      // SSH 用户名
    password,      // SSH 密码
    serverHost,    // SSH 服务器地址（域名或 IP）
    serverPort,    // SSH 服务器端口（通常是 22）
    localHost,     // 本地服务地址（要暴露的内网服务 IP）
    localPort,     // 本地服务端口（内网服务的端口号）
    remotePort,    // 远程端口（外网访问的端口号）
)
```

## API 文档

### Tunnel

隧道对象，用于管理 SSH 隧道连接。

#### 方法

- `NewTunnel(username, password, serverHost string, serverPort int, localHost string, localPort, remotePort int) *Tunnel`
  - 创建新的隧道实例
  - 参数：用户名、密码、服务器地址、服务器端口、本地地址、本地端口、远程端口

- `EnableLog(enabled bool) *Tunnel`
  - 控制 SDK 内部日志输出
  - enabled: true 启用日志，false 禁用日志（默认）

- `Start() error`
  - 启动 SSH 隧道
  - 返回错误如果隧道已在运行

- `Stop() error`
  - 停止 SSH 隧道

- `Wait() error`
  - 阻塞等待，直到收到退出信号（Ctrl+C）

- `Status() *TunnelStatus`
  - 获取完整隧道状态

- `IsRunning() bool`
  - 隧道是否运行中

- `IsConnected() bool`
  - 是否已连接到 SSH 服务器

- `GetLastError() string`
  - 获取最后错误信息

- `GetRemotePort() int`
  - 获取远程端口

- `GetBytesSent() int64`
  - 获取发送字节数

- `GetBytesReceived() int64`
  - 获取接收字节数

## 依赖

- Go 1.21+
- golang.org/x/crypto v0.21.0
