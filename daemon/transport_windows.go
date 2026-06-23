//go:build windows
// +build windows

package daemon

import (
	"encoding/json"
	"net"
	"sync"
	"time"

	"golang.org/x/sys/windows"
)

const (
	SocketPath     = `\\.\pipe\nssh_daemon`
	PipeBufferSize = 64 * 1024
)

type UnixTransport struct {
	listener     windows.Handle
	mu           sync.Mutex
	handler      func(int, map[string]string, string, int64) string
	stopChan     chan struct{}
	wg           sync.WaitGroup
	readyChan    chan struct{}
}

func NewTransport() Transport {
	return &UnixTransport{
		stopChan:  make(chan struct{}),
		readyChan: make(chan struct{}),
	}
}

func (t *UnixTransport) StartServer(handler func(int, map[string]string, string, int64) string) error {
	t.handler = handler

	go t.acceptLoop()

	select {
	case <-t.readyChan:
	case <-time.After(5 * time.Second):
	}
	return nil
}

func (t *UnixTransport) acceptLoop() {
	t.wg.Add(1)
	defer t.wg.Done()

	firstPipe := true
	for {
		select {
		case <-t.stopChan:
			return
		default:
		}

		pipeName, err := windows.UTF16PtrFromString(SocketPath)
		if err != nil {
			time.Sleep(100 * time.Millisecond)
			continue
		}

		handle, err := windows.CreateNamedPipe(
			pipeName,
			windows.PIPE_ACCESS_DUPLEX,
			windows.PIPE_TYPE_BYTE|windows.PIPE_READMODE_BYTE,
			1,
			PipeBufferSize,
			PipeBufferSize,
			0,
			nil,
		)
		if err != nil {
			time.Sleep(100 * time.Millisecond)
			continue
		}

		t.mu.Lock()
		t.listener = handle
		t.mu.Unlock()

		if firstPipe {
			firstPipe = false
			close(t.readyChan)
		}

		t.wg.Add(1)
		go t.handleNamedPipe(handle)
	}
}

func (t *UnixTransport) handleNamedPipe(handle windows.Handle) {
	defer t.wg.Done()
	defer windows.DisconnectNamedPipe(handle)
	defer windows.CloseHandle(handle)

	err := windows.ConnectNamedPipe(handle, nil)
	if err != nil && err != windows.ERROR_PIPE_CONNECTED {
		return
	}

	conn := &namedPipeConn{handle: handle}
	t.handleConnection(conn, t.handler)
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
	pipeName, err := windows.UTF16PtrFromString(SocketPath)
	if err != nil {
		return "", err
	}

	var handle windows.Handle
	for i := 0; i < 10; i++ {
		handle, err = windows.CreateFile(
			pipeName,
			windows.GENERIC_READ|windows.GENERIC_WRITE,
			0,
			nil,
			windows.OPEN_EXISTING,
			0,
			0,
		)
		if err == nil {
			break
		}
		if err == windows.ERROR_PIPE_BUSY {
			time.Sleep(50 * time.Millisecond)
			continue
		}
		return "", err
	}
	if err != nil {
		return "", err
	}
	defer windows.CloseHandle(handle)

	req := Request{
		Cmd:       cmd,
		Params:    params,
		Timestamp: timestamp,
		Key:       EncryptKeyWithTime(timestamp),
	}

	data, err := json.Marshal(req)
	if err != nil {
		return "", err
	}

	var bytesWritten uint32
	err = windows.WriteFile(handle, data, &bytesWritten, nil)
	if err != nil {
		return "", err
	}

	var buf [4096]byte
	var bytesRead uint32
	err = windows.ReadFile(handle, buf[:], &bytesRead, nil)
	if err != nil {
		return "", err
	}

	return string(buf[:bytesRead]), nil
}

func (t *UnixTransport) Stop() error {
	close(t.stopChan)
	t.wg.Wait()

	t.mu.Lock()
	if t.listener != 0 {
		windows.CloseHandle(t.listener)
		t.listener = 0
	}
	t.mu.Unlock()

	return nil
}

type namedPipeConn struct {
	handle windows.Handle
}

func (c *namedPipeConn) Read(b []byte) (n int, err error) {
	var bytesRead uint32
	err = windows.ReadFile(c.handle, b, &bytesRead, nil)
	return int(bytesRead), err
}

func (c *namedPipeConn) Write(b []byte) (n int, err error) {
	var bytesWritten uint32
	err = windows.WriteFile(c.handle, b, &bytesWritten, nil)
	return int(bytesWritten), err
}

func (c *namedPipeConn) Close() error {
	return windows.CloseHandle(c.handle)
}

func (c *namedPipeConn) LocalAddr() net.Addr {
	return &pipeAddr{}
}

func (c *namedPipeConn) RemoteAddr() net.Addr {
	return &pipeAddr{}
}

func (c *namedPipeConn) SetDeadline(t time.Time) error {
	return nil
}

func (c *namedPipeConn) SetReadDeadline(t time.Time) error {
	return nil
}

func (c *namedPipeConn) SetWriteDeadline(t time.Time) error {
	return nil
}

type pipeAddr struct{}

func (a *pipeAddr) Network() string { return "pipe" }
func (a *pipeAddr) String() string  { return SocketPath }
