//go:build !windows
// +build !windows

package daemon

import (
	"bufio"
	"encoding/json"
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
	os.Remove(SocketPath)

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
