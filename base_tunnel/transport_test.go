package base_tunnel

import (
	"context"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"io"
	"math/big"
	"net"
	"testing"
	"time"

	"github.com/quic-go/quic-go"
	"golang.org/x/crypto/ssh"

	"github.com/Vrolist/nssh/base_core"
)

// newTestSelfSigned 生成测试用内存自签证书
func newTestSelfSigned(t *testing.T) tls.Certificate {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	serial, _ := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	tmpl := x509.Certificate{
		SerialNumber:          serial,
		Subject:               pkix.Name{CommonName: "nssh-quic-test"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(time.Hour),
		KeyUsage:              x509.KeyUsageDigitalSignature,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
	}
	der, err := x509.CreateCertificate(rand.Reader, &tmpl, &tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatalf("create cert: %v", err)
	}
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key}
}

// startTestQUICServer 启动进程内 QUIC 监听，返回地址与关闭函数。
// 每条 stream 当作独立连接，echo 所有数据。
func startTestQUICServer(t *testing.T) (string, func()) {
	t.Helper()
	udpConn, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP("127.0.0.1")})
	if err != nil {
		t.Fatalf("listen udp: %v", err)
	}
	listener, err := quic.Listen(udpConn, &tls.Config{
		Certificates: []tls.Certificate{newTestSelfSigned(t)},
		NextProtos:   []string{quicALPN},
		MinVersion:   tls.VersionTLS13,
	}, &quic.Config{})
	if err != nil {
		t.Fatalf("quic listen: %v", err)
	}
	done := make(chan struct{})
	go func() {
		for {
			qconn, err := listener.Accept(context.Background())
			if err != nil {
				return
			}
			go func(qconn quic.Connection) {
				for {
					stream, err := qconn.AcceptStream(context.Background())
					if err != nil {
						return
					}
					go func(stream quic.Stream) {
						io.Copy(stream, stream)
						stream.Close()
					}(stream)
				}
			}(qconn)
		}
	}()
	return listener.Addr().String(), func() {
		close(done)
		listener.Close()
	}
}

// startTestTCPServer 启动纯 TCP 监听（仅接受连接，不说话），返回地址与关闭函数
func startTestTCPServer(t *testing.T) (string, func()) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen tcp: %v", err)
	}
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			conn.Close()
		}
	}()
	return ln.Addr().String(), func() { ln.Close() }
}

// TestNormalizeTransport 验证传输模式归一化：空值/未知值回退 auto
func TestNormalizeTransport(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"", "auto"},
		{"auto", "auto"},
		{"AUTO", "auto"},
		{"  quic ", "quic"},
		{"QUIC", "quic"},
		{"tcp", "tcp"},
		{"Tcp", "tcp"},
		{"unknown-mode", "auto"},
	}
	for _, c := range cases {
		if got := NormalizeTransport(c.in); got != c.want {
			t.Errorf("NormalizeTransport(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// TestDialQUICTransport_Echo 验证 QUIC 拨号 + 单 stream 双向数据往返
func TestDialQUICTransport_Echo(t *testing.T) {
	addr, stop := startTestQUICServer(t)
	defer stop()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	conn, err := dialQUICTransport(ctx, addr, 3*time.Second)
	if err != nil {
		t.Fatalf("dialQUICTransport: %v", err)
	}
	defer conn.Close()

	// net.Conn 接口完整性
	if conn.LocalAddr() == nil || conn.RemoteAddr() == nil {
		t.Fatal("LocalAddr/RemoteAddr must not be nil")
	}

	payload := []byte("quic stream roundtrip")
	if _, err := conn.Write(payload); err != nil {
		t.Fatalf("write: %v", err)
	}
	buf := make([]byte, len(payload))
	conn.SetReadDeadline(time.Now().Add(3 * time.Second))
	if _, err := io.ReadFull(conn, buf); err != nil {
		t.Fatalf("read echo: %v", err)
	}
	if string(buf) != string(payload) {
		t.Fatalf("echo mismatch: got %q", string(buf))
	}
}

// TestDialQUICTransport_DeadTarget 验证拨号到无 QUIC 服务的地址快速失败
func TestDialQUICTransport_DeadTarget(t *testing.T) {
	// 占住一个 UDP 端口后立刻关闭，得到一个确定无人监听的端口号
	u, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP("127.0.0.1")})
	if err != nil {
		t.Fatalf("probe udp port: %v", err)
	}
	deadAddr := u.LocalAddr().String()
	u.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	start := time.Now()
	_, err = dialQUICTransport(ctx, deadAddr, 1*time.Second)
	if err == nil {
		t.Fatal("expected error dialing dead QUIC target")
	}
	if elapsed := time.Since(start); elapsed > 3*time.Second {
		t.Fatalf("dial took too long, expected fast failure: %v", elapsed)
	}
}

// TestDialTransport_AutoProbePrefersQUIC auto 模式：服务端有 QUIC 时探测成功并粘性缓存
func TestDialTransport_AutoProbePrefersQUIC(t *testing.T) {
	resetTransportCacheForTest()
	defer resetTransportCacheForTest()

	addr, stop := startTestQUICServer(t)
	defer stop()

	config := &base_core.Config{Transport: "auto"}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	conn, mode, err := dialTransport(ctx, config, addr)
	if err != nil {
		t.Fatalf("dialTransport: %v", err)
	}
	defer conn.Close()
	if mode != "quic" {
		t.Fatalf("expected mode quic, got %s", mode)
	}

	// 缓存应记录 quic
	if cached, ok := transportCache.Load(addr); !ok {
		t.Fatal("expected transport cache entry")
	} else if cached.(transportCacheEntry).mode != "quic" {
		t.Fatalf("expected cached mode quic, got %s", cached.(transportCacheEntry).mode)
	}
}

// TestDialTransport_AutoFallbackTCP auto 模式：无 QUIC 服务时探测失败回退 TCP 并缓存
func TestDialTransport_AutoFallbackTCP(t *testing.T) {
	resetTransportCacheForTest()
	defer resetTransportCacheForTest()

	addr, stop := startTestTCPServer(t)
	defer stop()

	config := &base_core.Config{Transport: "auto"}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	conn, mode, err := dialTransport(ctx, config, addr)
	if err != nil {
		t.Fatalf("dialTransport: %v", err)
	}
	defer conn.Close()
	if mode != "tcp" {
		t.Fatalf("expected fallback to tcp, got %s", mode)
	}

	// 缓存应为 tcp 且带重探时间
	cached, ok := transportCache.Load(addr)
	if !ok {
		t.Fatal("expected transport cache entry")
	}
	entry := cached.(transportCacheEntry)
	if entry.mode != "tcp" {
		t.Fatalf("expected cached mode tcp, got %s", entry.mode)
	}
	if time.Now().After(entry.retryQUICAt) {
		t.Fatal("expected retryQUICAt in the future")
	}
}

// TestDialTransport_AutoQuicCachedFallsBackOnFailure auto 模式：QUIC 已缓存但本轮失败时回退 TCP 兜底
func TestDialTransport_AutoQuicCachedFallsBackOnFailure(t *testing.T) {
	resetTransportCacheForTest()
	defer resetTransportCacheForTest()

	// 先对一个 QUIC 服务成功探测，缓存 quic
	addr, stop := startTestQUICServer(t)
	config := &base_core.Config{Transport: "auto"}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	conn, mode, err := dialTransport(ctx, config, addr)
	if err != nil || mode != "quic" {
		t.Fatalf("first dial: mode=%s err=%v", mode, err)
	}
	conn.Close()

	// TCP 监听同端口不可行（UDP/TCP 可同号），改为：QUIC 服务关闭后，
	// 在同端口起 TCP 服务，模拟"QUIC 消失但 TCP 还在"的场景
	stop()

	tcpLn, err := net.Listen("tcp", addr)
	if err != nil {
		t.Fatalf("listen tcp on %s: %v", addr, err)
	}
	defer tcpLn.Close()
	go func() {
		for {
			c, err := tcpLn.Accept()
			if err != nil {
				return
			}
			c.Close()
		}
	}()

	conn2, mode2, err := dialTransport(ctx, config, addr)
	if err != nil {
		t.Fatalf("second dial: %v", err)
	}
	defer conn2.Close()
	if mode2 != "tcp" {
		t.Fatalf("expected tcp fallback after QUIC gone, got %s", mode2)
	}

	// 缓存应保持 quic（本轮失败不降缓存，下轮仍优先 QUIC）
	cached, _ := transportCache.Load(addr)
	if entry := cached.(transportCacheEntry); entry.mode != "quic" {
		t.Fatalf("expected cache to stay quic, got %s", entry.mode)
	}
}

// TestDialTransport_ForcedModes 强制 tcp/quic 模式不走探测缓存
func TestDialTransport_ForcedModes(t *testing.T) {
	resetTransportCacheForTest()
	defer resetTransportCacheForTest()

	quicAddr, stopQUIC := startTestQUICServer(t)
	defer stopQUIC()
	tcpAddr, stopTCP := startTestTCPServer(t)
	defer stopTCP()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// 强制 tcp：显式指定 tcp 模式时直接走 TCP 拨号（不探测 QUIC）
	conn, mode, err := dialTransport(ctx, &base_core.Config{Transport: "tcp"}, tcpAddr)
	if err != nil {
		t.Fatalf("forced tcp dial: %v", err)
	}
	conn.Close()
	if mode != "tcp" {
		t.Fatalf("expected tcp, got %s", mode)
	}

	// 强制 quic：显式指定 quic 模式时走 QUIC 拨号
	conn2, mode2, err := dialTransport(ctx, &base_core.Config{Transport: "quic"}, quicAddr)
	if err != nil {
		t.Fatalf("forced quic dial: %v", err)
	}
	conn2.Close()
	if mode2 != "quic" {
		t.Fatalf("expected quic, got %s", mode2)
	}

	// 强制 quic 到无 QUIC 服务：应报错（不回退）
	_, _, err = dialTransport(ctx, &base_core.Config{Transport: "quic"}, tcpAddr)
	if err == nil {
		t.Fatal("expected error for forced quic against quic-less server")
	}
}

// TestSSHandshakeOverQUICTransport 集成：dialQUICTransport 的 conn 直接喂给 ssh.NewClientConn 完成 SSH 握手
func TestSSHandshakeOverQUICTransport(t *testing.T) {
	// 服务端：QUIC 监听 + 每 stream 一条 SSH 会话（NoClientAuth）
	_, hostPriv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate host key: %v", err)
	}
	hostSigner, err := ssh.NewSignerFromKey(hostPriv)
	if err != nil {
		t.Fatalf("create signer: %v", err)
	}
	serverSSHConfig := &ssh.ServerConfig{NoClientAuth: true}
	serverSSHConfig.AddHostKey(hostSigner)

	udpConn, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP("127.0.0.1")})
	if err != nil {
		t.Fatalf("listen udp: %v", err)
	}
	listener, err := quic.Listen(udpConn, &tls.Config{
		Certificates: []tls.Certificate{newTestSelfSigned(t)},
		NextProtos:   []string{quicALPN},
		MinVersion:   tls.VersionTLS13,
	}, &quic.Config{})
	if err != nil {
		t.Fatalf("quic listen: %v", err)
	}
	defer listener.Close()
	addr := listener.Addr().String()

	handshakeDone := make(chan error, 1)
	go func() {
		for {
			qconn, err := listener.Accept(context.Background())
			if err != nil {
				return
			}
			go func(qconn quic.Connection) {
				stream, err := qconn.AcceptStream(context.Background())
				if err != nil {
					return
				}
				sconn, chans, reqs, err := ssh.NewServerConn(&quicStreamConn{Stream: stream, conn: qconn}, serverSSHConfig)
				if err != nil {
					handshakeDone <- err
					return
				}
				handshakeDone <- nil
				go ssh.DiscardRequests(reqs)
				for newCh := range chans {
					newCh.Reject(ssh.UnknownChannelType, "not supported in test")
				}
				sconn.Close()
			}(qconn)
		}
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	transportConn, err := dialQUICTransport(ctx, addr, 3*time.Second)
	if err != nil {
		t.Fatalf("dialQUICTransport: %v", err)
	}
	defer transportConn.Close()

	clientCfg := &ssh.ClientConfig{
		User:            "transport-test-user",
		HostKeyCallback: ssh.InsecureIgnoreHostKey(),
		Timeout:         3 * time.Second,
	}
	clientConn, chans, reqs, err := ssh.NewClientConn(transportConn, addr, clientCfg)
	if err != nil {
		t.Fatalf("ssh.NewClientConn over QUIC transport: %v", err)
	}
	client := ssh.NewClient(clientConn, chans, reqs)
	defer client.Close()

	select {
	case err := <-handshakeDone:
		if err != nil {
			t.Fatalf("server-side handshake failed: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("server handshake ack timed out")
	}

	go ssh.DiscardRequests(reqs)
	// keepalive 全局请求验证请求通道（与 worker 心跳同路径）
	if _, _, err := client.SendRequest("keepalive@openssh.com", true, nil); err != nil {
		t.Fatalf("keepalive request failed: %v", err)
	}
}
