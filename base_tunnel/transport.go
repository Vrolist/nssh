package base_tunnel

import (
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"strings"
	"sync"
	"time"

	"github.com/quic-go/quic-go"

	"github.com/Vrolist/nssh/base_core"
)

// 传输层抽象（SSH over QUIC 阶段1）：
// - TCP：现状路径，一行不动（标准 ssh -R、老版 nssh/nssh-agent 全部照旧）
// - QUIC：一条 QUIC 连接（UDP）+ 单条 stream 承载完整 SSH 连接，
//   ssh.NewClientConn 直接吃 net.Conn，SSH 协议层零改动
// - auto：QUIC 握手探测（短超时）→ 成功粘性缓存；失败回退 TCP 并按周期重探

const (
	// QUICProbeTimeout auto 模式下 QUIC 握手探测超时（短探针，失败快速回退 TCP）
	QUICProbeTimeout = 1 * time.Second
	// QUICDialTimeout 已确认走 QUIC 后的拨号超时（与服务端 300s 空闲超时无关，仅握手期）
	QUICDialTimeout = 10 * time.Second
	// QUICRetryProbeInterval QUIC 探测失败后缓存 TCP 的时长，到期后重连会重新探测 QUIC
	QUICRetryProbeInterval = 5 * time.Minute
	// QUICKeepAlivePeriod QUIC keep-alive 周期，防 NAT/防火墙 UDP 表项超时
	QUICKeepAlivePeriod = 30 * time.Second
	// quicALPN 与服务端一致的 ALPN 协议标识
	quicALPN = "nssh"
)

// NormalizeTransport 归一化传输模式；空串/未知值回退 auto（默认值，与老行为兼容——
// auto 探测不到 QUIC 时就是纯 TCP，等价于老版本行为）
func NormalizeTransport(mode string) string {
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case "tcp":
		return "tcp"
	case "quic":
		return "quic"
	default:
		return "auto"
	}
}

// quicStreamConn 把 quic.Stream 适配为完整 net.Conn：补上 LocalAddr/RemoteAddr（挂在父连接上），
// 并重写 Close——阶段1每条 SSH 会话独占一条 QUIC 连接，SSH 会话结束即释放整个连接。
type quicStreamConn struct {
	quic.Stream
	conn quic.Connection
}

func (c *quicStreamConn) LocalAddr() net.Addr  { return c.conn.LocalAddr() }
func (c *quicStreamConn) RemoteAddr() net.Addr { return c.conn.RemoteAddr() }

// Close 同时关闭 stream 与 QUIC 连接
func (c *quicStreamConn) Close() error {
	_ = c.Stream.Close()
	c.conn.CloseWithError(0, "closed")
	return nil
}

// transportCache auto 模式的粘性探测结果缓存：serverAddr → {mode, retryQUICAt}
type transportCacheEntry struct {
	mode        string // "quic" | "tcp"
	retryQUICAt time.Time
}

var transportCache sync.Map

// resetTransportCacheForTest 仅测试使用：清空探测缓存
func resetTransportCacheForTest() {
	transportCache.Range(func(key, _ interface{}) bool {
		transportCache.Delete(key)
		return true
	})
}

// dialTCPTransport 现状 TCP 路径（保持一行不动）
func dialTCPTransport(serverAddr string) (net.Conn, error) {
	// TCP 层 keepalive：OS 默认探测间隔太长（约 2 小时），显式 30s 尽快发现半开连接
	dialer := &net.Dialer{KeepAlive: 30 * time.Second}
	tcpConn, err := dialer.Dial("tcp", serverAddr)
	if err != nil {
		return nil, err
	}
	if tc, ok := tcpConn.(*net.TCPConn); ok {
		tc.SetNoDelay(true)
	}
	return tcpConn, nil
}

// quicClientTLS QUIC 客户端 TLS 配置。
// InsecureSkipVerify：服务端默认用内存自签证书（零配置），仅做传输加密——
// 真正的身份鉴权由 SSH 密码/密钥完成，不新增信任假设。
// ClientSessionCache 启用 TLS 会话恢复，重连时 1-RTT 握手更快。
func quicClientTLS() *tls.Config {
	return &tls.Config{
		NextProtos:         []string{quicALPN},
		InsecureSkipVerify: true,
		MinVersion:         tls.VersionTLS13,
		ClientSessionCache: tls.NewLRUClientSessionCache(32),
	}
}

// dialQUICTransport QUIC 拨号：UDP 握手 → 开单条 stream → 适配为 net.Conn
func dialQUICTransport(ctx context.Context, serverAddr string, timeout time.Duration) (net.Conn, error) {
	dialCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	qconn, err := quic.DialAddr(dialCtx, serverAddr, quicClientTLS(), &quic.Config{
		MaxIdleTimeout:  300 * time.Second,
		KeepAlivePeriod: QUICKeepAlivePeriod,
	})
	if err != nil {
		return nil, err
	}

	stream, err := qconn.OpenStreamSync(dialCtx)
	if err != nil {
		qconn.CloseWithError(0, "open stream failed")
		return nil, err
	}
	return &quicStreamConn{Stream: stream, conn: qconn}, nil
}

// dialTransport 按传输模式建立连接，返回 (net.Conn, 实际使用模式, error)。
// auto 语义：
//   - 首次：QUIC 短超时探测 → 成功则缓存 "quic" 并使用；失败回退 TCP 并缓存 "tcp"（5 分钟内免重探）
//   - 缓存 "quic"：直接 QUIC 拨号；万一失败（服务端关掉 QUIC）回退 TCP 本轮兜底
//   - 缓存 "tcp" 未到期：直接 TCP；到期重新探测 QUIC（运营商 UDP 恢复后自动切回）
func dialTransport(ctx context.Context, config *base_core.Config, serverAddr string) (net.Conn, string, error) {
	mode := NormalizeTransport(config.Transport)

	switch mode {
	case "tcp":
		conn, err := dialTCPTransport(serverAddr)
		return conn, "tcp", err

	case "quic":
		conn, err := dialQUICTransport(ctx, serverAddr, QUICDialTimeout)
		if err != nil {
			return nil, "quic", err
		}
		return conn, "quic", nil
	}

	// ---- auto 模式 ----
	if cached, ok := transportCache.Load(serverAddr); ok {
		entry := cached.(transportCacheEntry)
		switch {
		case entry.mode == "quic":
			conn, err := dialQUICTransport(ctx, serverAddr, QUICDialTimeout)
			if err == nil {
				return conn, "quic", nil
			}
			// 已确认支持 QUIC 但本轮失败：不降缓存，回退 TCP 兜底本轮
			fallback, tcpErr := dialTCPTransport(serverAddr)
			if tcpErr != nil {
				return nil, "quic", fmt.Errorf("quic dial failed (%v), tcp fallback failed (%w)", err, tcpErr)
			}
			return fallback, "tcp", nil

		case time.Now().Before(entry.retryQUICAt):
			conn, err := dialTCPTransport(serverAddr)
			return conn, "tcp", err
		}
	}

	// 探测 QUIC（短超时，失败快速回退）
	probeConn, err := dialQUICTransport(ctx, serverAddr, QUICProbeTimeout)
	if err == nil {
		transportCache.Store(serverAddr, transportCacheEntry{mode: "quic"})
		return probeConn, "quic", nil
	}

	// QUIC 不可用：缓存 TCP，周期性重探
	transportCache.Store(serverAddr, transportCacheEntry{mode: "tcp", retryQUICAt: time.Now().Add(QUICRetryProbeInterval)})
	conn, err := dialTCPTransport(serverAddr)
	return conn, "tcp", err
}
