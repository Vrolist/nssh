package base_tunnel

import (
	"context"
	"errors"
	"testing"

	"golang.org/x/crypto/ssh"

	"github.com/Vrolist/nssh/base_core"
)

// fakeTCPForwardClient 模拟 *ssh.Client 的转发声明能力
type fakeTCPForwardClient struct {
	requests    chan ssh.NewChannel
	ok          bool
	sendErr     error
	closed      bool
	requestType string
}

func (f *fakeTCPForwardClient) HandleChannelOpen(string) <-chan ssh.NewChannel {
	return f.requests
}

func (f *fakeTCPForwardClient) SendRequest(name string, wantReply bool, payload []byte) (bool, []byte, error) {
	f.requestType = name
	return f.ok, nil, f.sendErr
}

func (f *fakeTCPForwardClient) Close() error {
	f.closed = true
	return nil
}

// TestDeclareTCPForward_Rejected 服务端拒绝 tcpip-forward 时必须断线并返回错误
// （旧实现丢弃 ok，导致连接存活但无转发能力 → 客户端假在线）
func TestDeclareTCPForward_Rejected(t *testing.T) {
	fc := &fakeTCPForwardClient{requests: make(chan ssh.NewChannel), ok: false}

	if _, err := declareTCPForward(fc, 14572); err == nil {
		t.Fatal("expected error when server rejects tcpip-forward")
	}
	if !fc.closed {
		t.Fatal("connection must be closed when tcpip-forward is rejected")
	}
	if fc.requestType != "tcpip-forward" {
		t.Fatalf("unexpected request type: %s", fc.requestType)
	}
}

// TestDeclareTCPForward_SendError 发送失败（连接已坏）必须断线并返回错误
func TestDeclareTCPForward_SendError(t *testing.T) {
	fc := &fakeTCPForwardClient{
		requests: make(chan ssh.NewChannel),
		ok:       false,
		sendErr:  errors.New("connection lost"),
	}

	if _, err := declareTCPForward(fc, 14572); err == nil {
		t.Fatal("expected error when tcpip-forward send fails")
	}
	if !fc.closed {
		t.Fatal("connection must be closed when send fails")
	}
}

// TestDeclareTCPForward_RegistrationFailed channel 注册失败必须断线并返回错误
func TestDeclareTCPForward_RegistrationFailed(t *testing.T) {
	fc := &fakeTCPForwardClient{requests: nil}

	if _, err := declareTCPForward(fc, 14572); err == nil {
		t.Fatal("expected error when channel registration fails")
	}
	if !fc.closed {
		t.Fatal("connection must be closed when channel registration fails")
	}
}

// TestDeclareTCPForward_Accepted 服务端接受时返回 channel 列表且不断线
func TestDeclareTCPForward_Accepted(t *testing.T) {
	fc := &fakeTCPForwardClient{requests: make(chan ssh.NewChannel, 1), ok: true}

	requests, err := declareTCPForward(fc, 14572)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if requests == nil {
		t.Fatal("expected requests channel")
	}
	if fc.closed {
		t.Fatal("connection must stay open when tcpip-forward is accepted")
	}
}

// TestSetupTCPForward_Rejected 声明被拒绝时 setupTCPForward 必须传播错误
func TestSetupTCPForward_Rejected(t *testing.T) {
	fc := &fakeTCPForwardClient{requests: make(chan ssh.NewChannel), ok: false}
	config := &base_core.Config{RemotePort: 14572, LocalHost: "127.0.0.1", LocalPort: 5666}

	if err := setupTCPForward(fc, &ChannelHandler{localHost: "127.0.0.1", localPort: 5666}, context.Background(), config); err == nil {
		t.Fatal("expected setupTCPForward to return error when rejected")
	}
	if !fc.closed {
		t.Fatal("connection must be closed when rejected")
	}
}

// TestSetupTCPForward_Accepted 声明被接受时返回 nil 并交给转发循环
func TestSetupTCPForward_Accepted(t *testing.T) {
	requests := make(chan ssh.NewChannel)
	fc := &fakeTCPForwardClient{requests: requests, ok: true}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	config := &base_core.Config{RemotePort: 14572, LocalHost: "127.0.0.1", LocalPort: 5666}

	if err := setupTCPForward(fc, &ChannelHandler{localHost: "127.0.0.1", localPort: 5666}, ctx, config); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if fc.closed {
		t.Fatal("connection must stay open when accepted")
	}
	// 关闭 requests 让转发 goroutine 退出，避免测试泄漏
	close(requests)
}
