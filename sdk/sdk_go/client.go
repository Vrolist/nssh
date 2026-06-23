package sdk_go

import (
	"context"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"sync"
	"time"

	"golang.org/x/crypto/ssh"
)

type Client struct {
	config        *Config
	statusManager *StatusManager
	ctx           context.Context
	cancel        context.CancelFunc
	mu            sync.RWMutex
	running       bool
	logger        *log.Logger
	logEnabled    bool
}

func NewClient(config *Config) (*Client, error) {
	if config == nil {
		return nil, fmt.Errorf("config cannot be nil")
	}
	if config.Username == "" {
		return nil, fmt.Errorf("username is required")
	}
	if config.ServerHost == "" {
		return nil, fmt.Errorf("server host is required")
	}
	if config.ServerPort <= 0 {
		return nil, fmt.Errorf("server port is required")
	}
	if config.LocalPort <= 0 {
		return nil, fmt.Errorf("local port is required")
	}

	ctx, cancel := context.WithCancel(context.Background())

	return &Client{
		config:        config,
		statusManager: NewStatusManager(),
		ctx:           ctx,
		cancel:        cancel,
		running:       false,
		logger:        log.New(os.Stdout, "", 0),
		logEnabled:    false,
	}, nil
}

func (c *Client) EnableLog(enabled bool) *Client {
	// EnableLog 控制 SDK 内部日志的输出
	// enabled: true 启用日志，false 禁用日志（默认）
	// 日志内容包括连接状态、隧道建立、错误信息等
	c.logEnabled = enabled
	return c
}

func (c *Client) log(format string, args ...interface{}) {
	if c.logEnabled {
		c.logger.Printf("[SDK] "+format, args...)
	}
}

func (c *Client) Start() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.running {
		return fmt.Errorf("client is already running")
	}

	c.running = true
	c.statusManager.SetStartTime(time.Now())

	c.log("SSH tunnel started")

	go c.run()

	return nil
}

func (c *Client) Stop() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if !c.running {
		return fmt.Errorf("client is not running")
	}

	c.cancel()
	c.running = false
	c.statusManager.SetConnected(false)

	c.log("SSH tunnel stopped")

	return nil
}

func (c *Client) Status() *TunnelStatus {
	return c.statusManager.GetStatus()
}

func (c *Client) SetLogger(logger *Logger) *Client {
	c.logger = log.New(logger, "", 0)
	return c
}

func (c *Client) run() {
	for {
		select {
		case <-c.ctx.Done():
			return
		default:
			if err := c.connectAndTunnel(); err != nil {
				c.statusManager.SetConnected(false)
				c.statusManager.SetLastError(err.Error())
				c.log("Connection error: %v", err)

				delay := time.Duration(c.config.ReconnectDelay) * time.Second
				select {
				case <-time.After(delay):
				case <-c.ctx.Done():
					return
				}
			}
		}
	}
}

func (c *Client) connectAndTunnel() error {
	sshConfig := &ssh.ClientConfig{
		User:            c.config.Username,
		Auth:            []ssh.AuthMethod{},
		HostKeyCallback: ssh.InsecureIgnoreHostKey(),
		Timeout:         10 * time.Second,
	}

	if c.config.Password != "" {
		sshConfig.Auth = append(sshConfig.Auth, ssh.Password(c.config.Password))
	}

	serverAddr := fmt.Sprintf("%s:%d", c.config.ServerHost, c.config.ServerPort)

	c.statusManager.SetConnected(true)
	c.statusManager.SetLastError("")

	c.log("Connecting to %s...", serverAddr)

	client, err := ssh.Dial("tcp", serverAddr, sshConfig)
	if err != nil {
		c.log("Failed to connect: %v", err)
		return fmt.Errorf("failed to dial: %w", err)
	}
	c.log("Connected to %s", serverAddr)

	defer client.Close()

	connCtx, connCancel := context.WithCancel(c.ctx)
	defer connCancel()

	connErr := make(chan error, 1)
	go func() {
		err := client.Wait()
		connErr <- err
		connCancel()
	}()

	channelHandler := &channelHandler{
		localHost:     c.config.LocalHost,
		localPort:     c.config.LocalPort,
		statusManager: c.statusManager,
	}

	requests := client.HandleChannelOpen("forwarded-tcpip")
	if requests == nil {
		c.log("Failed to register for forwarded-tcpip channel requests")
		return fmt.Errorf("failed to register for forwarded-tcpip channel requests")
	}

	_, _, err = client.SendRequest("tcpip-forward", true, ssh.Marshal(&struct {
		Address string
		Port    uint32
	}{
		Address: "0.0.0.0",
		Port:    uint32(c.config.RemotePort),
	}))
	if err != nil {
		c.log("Failed to send tcpip-forward request: %v", err)
		return fmt.Errorf("failed to send tcpip-forward request: %w", err)
	}

	c.statusManager.SetRemotePort(c.config.RemotePort)

	c.log("Tunnel established: remote:%d -> local:%s:%d",
		c.config.RemotePort, c.config.LocalHost, c.config.LocalPort)

	go func() {
		for {
			select {
			case <-connCtx.Done():
				return
			case newCh, ok := <-requests:
				if !ok {
					return
				}

				localAddr := fmt.Sprintf("%s:%d", channelHandler.localHost, channelHandler.localPort)
				localConn, err := net.Dial("tcp", localAddr)
				if err != nil {
					newCh.Reject(ssh.ConnectionFailed, "local service unavailable")
					continue
				}

				ch, reqs, err := newCh.Accept()
				if err != nil {
					localConn.Close()
					newCh.Reject(ssh.ConnectionFailed, "failed to accept")
					continue
				}
				go ssh.DiscardRequests(reqs)

				go channelHandler.handleChannel(ch, localConn)
			}
		}
	}()

	heartbeatTicker := time.NewTicker(60 * time.Second)
	defer heartbeatTicker.Stop()

	for {
		select {
		case <-c.ctx.Done():
			c.log("Context cancelled, stopping")
			return nil
		case <-connCtx.Done():
			if err := <-connErr; err != nil {
				c.log("Connection closed: %v", err)
				return fmt.Errorf("connection closed: %w", err)
			}
			c.log("Connection closed normally")
			return fmt.Errorf("connection closed normally")
		case <-heartbeatTicker.C:
			client.SendRequest("keepalive@nwyssh.net", true, nil)
		}
	}
}

type channelHandler struct {
	localHost     string
	localPort     int
	statusManager *StatusManager
}

func (h *channelHandler) handleChannel(ch ssh.Channel, localConn net.Conn) {
	defer localConn.Close()
	defer ch.Close()

	go func() {
		ch.SendRequest("exit-status", false, ssh.Marshal(&struct{ ExitStatus uint32 }{0}))
	}()

	var wg sync.WaitGroup
	wg.Add(2)

	go func() {
		defer wg.Done()
		n, _ := io.Copy(ch, localConn)
		h.statusManager.AddBytesSent(n)
	}()

	go func() {
		defer wg.Done()
		n, _ := io.Copy(localConn, ch)
		h.statusManager.AddBytesReceived(n)
	}()

	wg.Wait()
}
