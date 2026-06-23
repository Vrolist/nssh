package main

import (
	"fmt"
	"time"

	"nssh_client/sdk/sdk_go"
)

func main() {
	// 创建 SSH 隧道实例并配置参数
	// 参数说明：
	//   - 第1个参数：SSH 服务器用户名
	//   - 第2个参数：SSH 服务器密码
	//   - 第3个参数：SSH 服务器地址（域名或 IP）
	//   - 第4个参数：SSH 服务器端口（通常是 22）
	//   - 第5个参数：本地服务地址（要暴露的内网服务 IP）
	//   - 第6个参数：本地服务端口（内网服务的端口号）
	//   - 第7个参数：远程端口（外网访问的端口号）
	//   - EnableLog(true)：启用日志输出，方便调试
	//
	// 隧道映射关系：
	//   远程 0.0.0.0:80  ->  本地 127.0.0.1:80
	//   含义：外网访问 SSH 服务器的 80 端口，会转发到本地的 127.0.0.1:80
	tunnel := sdk_go.NewTunnel(
		"your-username",    // SSH 用户名
		"your-password",    // SSH 密码
		"your-server.com",  // SSH 服务器地址
		22,                 // SSH 服务器端口
		"127.0.0.1",        // 本地服务地址
		80,                 // 本地服务端口
		80,                 // 远程端口（外网访问端口）
	).EnableLog(false)

	// 打印启动提示信息，显示 SSH 命令格式
	fmt.Printf("[Example] Starting SSH tunnel...\n")
	fmt.Printf("[Example] SSH Command: ssh -R %d:%s:%d %s@%s -p %d\n",
		80,
		"127.0.0.1",
		80,
		"your-username",
		"your-server.com",
		22,
	)

	// 启动隧道（异步执行）
	// Start() 会：
	//   1. 连接到 SSH 服务器
	//   2. 建立端口转发隧道
	//   3. 启动自动重连机制
	tunnel.Start()

	// 启动一个后台 goroutine，每 5 秒打印一次隧道状态
	// 状态信息包括：
	//   - Running: 隧道是否正在运行
	//   - Connected: 是否已连接到 SSH 服务器
	//   - Port: 远程映射端口
	//   - Sent: 已发送的字节数
	//   - Recv: 已接收的字节数
	//   - Error: 最后一次错误信息
	go func() {
		for {
			// 每隔 5 秒输出一次状态
			time.Sleep(5 * time.Second)
			fmt.Printf("[Example] Status - Running:%v Connected:%v Port:%d Sent:%d Recv:%d Error:%s\n",
				tunnel.IsRunning(),           // 隧道是否运行中
				tunnel.IsConnected(),         // 是否已连接
				tunnel.GetRemotePort(),       // 远程端口
				tunnel.GetBytesSent(),        // 发送字节数
				tunnel.GetBytesReceived(),    // 接收字节数
				tunnel.GetLastError())        // 最后错误
		}
	}()

	// 阻塞等待，直到收到 Ctrl+C 信号
	// Wait() 会阻塞主线程，直到用户按下 Ctrl+C 终止程序
	tunnel.Wait()
}
