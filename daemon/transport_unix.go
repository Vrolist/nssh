//go:build !windows
// +build !windows

package daemon

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net"
	"os"
)

// SocketPath 由 getSocketPath() 动态获取，支持多平台
var SocketPath = getSocketPath()

type UnixTransport struct {
	listener net.Listener
}

func NewTransport() Transport {
	// 每次创建时重新计算路径（环境可能变化）
	SocketPath = getSocketPath()
	return &UnixTransport{}
}

func (t *UnixTransport) StartServer(handler func(int, map[string]string, string, int64) string) error {
	// 单例守卫（修复双 daemon）：
	//   - socket 已存在且可连接 → 已有活跃 daemon，绝不抢占其 socket，直接报错让本进程退出
	//   - socket 存在但不可连接 → 死 socket（进程已退出的残留文件），清理后重新绑定
	//   - socket 不存在 → 直接绑定
	// 竞态兜底：即便两个 daemon 同时进入，net.Listen 也只有一个成功，另一个报 EADDRINUSE 退出
	if _, err := os.Stat(SocketPath); err == nil {
		if conn, err := net.Dial("unix", SocketPath); err == nil {
			conn.Close()
			return fmt.Errorf("daemon already running on %s", SocketPath)
		}
		os.Remove(SocketPath)
	}

	listener, err := net.Listen("unix", SocketPath)
	if err != nil {
		return err
	}
	t.listener = listener

	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			go t.handleConnection(conn, handler)
		}
	}()

	return nil
}

func (t *UnixTransport) handleConnection(conn net.Conn, handler func(int, map[string]string, string, int64) string) {
	defer conn.Close()

	var req Request
	decoder := json.NewDecoder(conn)
	if err := decoder.Decode(&req); err != nil {
		return
	}

	response := handler(req.Cmd, req.Params, req.Key, req.Timestamp)
	conn.Write([]byte(response + "\n"))
}

func (t *UnixTransport) SendCommand(cmd int, params map[string]string, timestamp int64) (string, error) {
	conn, err := net.Dial("unix", SocketPath)
	if err != nil {
		return "", err
	}
	defer conn.Close()

	req := Request{
		Cmd:       cmd,
		Params:    params,
		Timestamp: timestamp,
		Key:       EncryptKeyWithTime(timestamp),
	}
	if err := json.NewEncoder(conn).Encode(req); err != nil {
		return "", err
	}

	reader := bufio.NewReader(conn)
	line, err := reader.ReadString('\n')
	if err != nil {
		return "", err
	}
	return line[:len(line)-1], nil
}

func (t *UnixTransport) Stop() error {
	if t.listener != nil {
		t.listener.Close()
		os.Remove(SocketPath)
	}
	return nil
}
