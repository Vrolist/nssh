package sdk_go

import (
	"context"
	"os"
	"os/signal"
	"syscall"
)

type Tunnel struct {
	client     *Client
	cancel     context.CancelFunc
	done       chan struct{}
	logEnabled bool
}

func NewTunnel(username, password, serverHost string, serverPort int,
	localHost string, localPort, remotePort int) *Tunnel {

	config := NewConfig(username, password, serverHost, serverPort, localPort)
	config.SetLocalHost(localHost).SetRemotePort(remotePort)

	client, _ := NewClient(config)

	_, cancel := context.WithCancel(context.Background())

	return &Tunnel{
		client:     client,
		cancel:     cancel,
		done:       make(chan struct{}),
		logEnabled: false,
	}
}

func (t *Tunnel) EnableLog(enabled bool) *Tunnel {
	// EnableLog 控制 SDK 内部日志的输出开关
	// enabled: true 启用日志输出，false 禁用日志输出（默认）
	// 启用后会输出：连接状态、隧道建立、错误信息等调试日志
	t.logEnabled = enabled
	t.client.EnableLog(enabled)
	return t
}

func (t *Tunnel) Start() error {
	if err := t.client.Start(); err != nil {
		return err
	}
	go t.handleSignals()
	return nil
}

func (t *Tunnel) Wait() error {
	<-t.done
	return nil
}

func (t *Tunnel) Stop() error {
	t.cancel()
	return t.client.Stop()
}

func (t *Tunnel) Status() *TunnelStatus {
	return t.client.Status()
}

func (t *Tunnel) IsRunning() bool {
	return t.client.Status().StartTime.IsZero() == false
}

func (t *Tunnel) IsConnected() bool {
	return t.client.Status().IsConnected
}

func (t *Tunnel) GetLastError() string {
	return t.client.Status().LastError
}

func (t *Tunnel) GetRemotePort() int {
	return t.client.Status().RemotePort
}

func (t *Tunnel) GetBytesSent() int64 {
	return t.client.Status().BytesSent
}

func (t *Tunnel) GetBytesReceived() int64 {
	return t.client.Status().BytesReceived
}

func (t *Tunnel) handleSignals() {
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
	signal.Ignore(syscall.SIGHUP)
	<-sigChan
	close(t.done)
}
