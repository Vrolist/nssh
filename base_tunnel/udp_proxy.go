package base_tunnel

import (
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"sync"
	"sync/atomic"
	"time"

	"golang.org/x/crypto/ssh"

	"github.com/Vrolist/nssh/base_core"
)

// UDP 业务穿透（UDP 业务层阶段1/2，channel 基线模式）：
// UDP 报文（明信片）封装进 SSH 自定义 channel（挂号信系统）做双向搬运。
// - 控制面：udp-forward@nssh global request 声明端口（与 tcpip-forward 同构 + 1 字节能力位）
// - 数据面：udp-data@nssh channel + 应用层帧（flow_id 4B + len 2B + payload）
// - flow：一条 UDP 会话（外部五元组）；flow_id 由服务端分配，客户端按需建本地 connected socket
// - 语义代价：UDP 被 SSH stream 可靠有序化（OpenVPN 等 VPN 业务可接受，见设计稿「语义代价」）
//
// 兼容红线：TCP 转发路径（forwarded-tcpip）一行不动；老服务端收不到 udp-forward@nssh
// 时 SendRequest 返回 ok=false，客户端明确报错（不静默回退）。

const (
	// udpForwardRequest 控制面：声明 UDP 转发端口（global request）
	udpForwardRequest = "udp-forward@nssh"
	// udpChannelType 数据面：UDP 数据通道（channel 类型）
	udpChannelType = "udp-data@nssh"

	// udpModeChannel 服务端 reply 的数据面 mode：channel 基线（阶段1客户端仅支持该模式）
	udpModeChannel byte = 1

	// udpCapsChannel 客户端能力位 bit0：支持 channel 模式（恒 1）
	udpCapsChannel byte = 0x01

	// udpMaxPacketSize 单报文上限（超出丢弃，符合 UDP 语义；OpenVPN 建议配 tun-mtu 1200）
	udpMaxPacketSize = 1400
	// udpFrameHeaderLen 帧头长度：flow_id 4B + len 2B
	udpFrameHeaderLen = 6
	// udpFlowIdleTimeout flow 空闲回收：> 服务端 QUIC 空闲 300s + keepalive 30s，与 L4LB UDP 对齐
	udpFlowIdleTimeout = 330 * time.Second
	// udpFlowCleanupInterval flow 空闲扫描周期
	udpFlowCleanupInterval = 30 * time.Second
	// udpReadBuffer UDP socket 收发缓冲
	udpReadBufferSize = 1024 * 1024
)

// encodeUDPFrame 编码一帧 UDP 数据：flow_id(4B 大端) + len(2B 大端) + payload。
// 调用方须保证 len(payload) <= udpMaxPacketSize（socket 读侧已丢弃超限报文）。
func encodeUDPFrame(flowID uint32, payload []byte) []byte {
	frame := make([]byte, udpFrameHeaderLen+len(payload))
	binary.BigEndian.PutUint32(frame[0:4], flowID)
	binary.BigEndian.PutUint16(frame[4:6], uint16(len(payload)))
	copy(frame[udpFrameHeaderLen:], payload)
	return frame
}

// readUDPFrame 从流式 reader（SSH channel）读取一帧。
// SSH channel 是有序字节流，帧边界靠 len 字段还原；返回的 payload 为独立副本。
func readUDPFrame(r io.Reader) (flowID uint32, payload []byte, err error) {
	var hdr [udpFrameHeaderLen]byte
	if _, err = io.ReadFull(r, hdr[:]); err != nil {
		return 0, nil, err
	}
	flowID = binary.BigEndian.Uint32(hdr[0:4])
	length := int(binary.BigEndian.Uint16(hdr[4:6]))
	payload = make([]byte, length)
	if length > 0 {
		if _, err = io.ReadFull(r, payload); err != nil {
			return 0, nil, err
		}
	}
	return flowID, payload, nil
}

// setupUDPForward 控制面：声明 UDP 转发端口并打开数据面 channel。
// 返回绑定该端口的数据面 channel 与服务端实际监听端口（assigned_port/随机分配可能与请求值不同）。
func setupUDPForward(client *ssh.Client, config *base_core.Config) (ssh.Channel, uint32, error) {
	// request payload 与 tcpip-forward 同构（addrLen+addr+port），尾部追加 1 字节能力位
	const bindAddr = "0.0.0.0"
	payload := make([]byte, 0, 4+len(bindAddr)+4+1)
	payload = binary.BigEndian.AppendUint32(payload, uint32(len(bindAddr)))
	payload = append(payload, bindAddr...)
	payload = binary.BigEndian.AppendUint32(payload, uint32(config.RemotePort))
	payload = append(payload, udpCapsChannel)

	ok, resp, err := client.SendRequest(udpForwardRequest, true, payload)
	if err != nil {
		return nil, 0, fmt.Errorf("udp-forward request failed: %w", err)
	}
	if !ok {
		// 老服务端 / 服务端 UDP 开关关闭：明确报错，不静默
		return nil, 0, fmt.Errorf("server rejected udp-forward@nssh (server too old or UDP forwarding disabled)")
	}
	if len(resp) < 5 {
		return nil, 0, fmt.Errorf("malformed udp-forward reply: %d bytes", len(resp))
	}
	actualPort := binary.BigEndian.Uint32(resp[0:4])
	mode := resp[4]
	if mode != udpModeChannel {
		return nil, 0, fmt.Errorf("server selected unsupported UDP data mode %d", mode)
	}

	// 打开数据面 channel：ExtraData 带服务端实际端口。
	// x/crypto/ssh 两侧接收窗口默认 2MB（channelWindowSize=64×32768），实时转发场景足够，
	// 无需 fork 定制窗口（设计稿 5.5 的 64KB 前提系记忆偏差，实测确认后修正文档）。
	chData := make([]byte, 4)
	binary.BigEndian.PutUint32(chData, actualPort)
	ch, _, err := client.OpenChannel(udpChannelType, chData)
	if err != nil {
		return nil, 0, fmt.Errorf("failed to open %s channel: %w", udpChannelType, err)
	}
	return ch, actualPort, nil
}

// udpClientFlow 客户端侧一条 flow：服务端分配的 flow_id ↔ 本地服务的 connected UDP socket。
// connected socket 的 Read 只收来自本地服务的回包，天然与 flow 一一对应，无需五元组表。
type udpClientFlow struct {
	flowID   uint32
	uplink   *net.UDPConn
	lastSeen atomic.Int64
	proxy    *udpProxy
	removed  sync.Once
}

// remove 回收 flow：从表移除并关 socket（触发读循环退出）
func (f *udpClientFlow) remove() {
	f.removed.Do(func() {
		f.proxy.flows.Delete(f.flowID)
		_ = f.uplink.Close()
	})
}

// pump 本地服务 → 服务端方向：读 connected socket 回包，帧化后写 channel
func (f *udpClientFlow) pump() {
	buf := make([]byte, 65527)
	for {
		n, err := f.uplink.Read(buf)
		if err != nil {
			f.remove()
			return
		}
		if n > udpMaxPacketSize {
			// 单报文超上限：丢弃（符合 UDP 语义，上层自行分片/调小 MTU）
			continue
		}
		if !f.proxy.writeFrame(f.flowID, buf[:n]) {
			f.remove()
			return
		}
	}
}

// udpProxy 客户端 UDP 转发器：一条 UDP 隧道端口一个实例
type udpProxy struct {
	ch          ssh.Channel
	localAddr   *net.UDPAddr
	idleTimeout time.Duration

	flows sync.Map // flow_id (uint32) -> *udpClientFlow
}

// writeFrame 帧 → channel 写入；channel 异常（连接断开）返回 false
func (p *udpProxy) writeFrame(flowID uint32, payload []byte) bool {
	if _, err := p.ch.Write(encodeUDPFrame(flowID, payload)); err != nil {
		return false
	}
	return true
}

// getOrCreateFlow 按 flow_id 取/建本地 connected socket；首包建流，后续粘性复用
func (p *udpProxy) getOrCreateFlow(flowID uint32) *udpClientFlow {
	if val, ok := p.flows.Load(flowID); ok {
		flow := val.(*udpClientFlow)
		flow.lastSeen.Store(time.Now().UnixNano())
		return flow
	}

	uplink, err := net.DialUDP("udp", nil, p.localAddr)
	if err != nil {
		// 本地服务不可达：丢弃该报文（UDP 语义），下个包重试拨号
		return nil
	}
	_ = uplink.SetReadBuffer(udpReadBufferSize)
	_ = uplink.SetWriteBuffer(udpReadBufferSize)

	flow := &udpClientFlow{
		flowID: flowID,
		uplink: uplink,
		proxy:  p,
	}
	flow.lastSeen.Store(time.Now().UnixNano())

	// LoadOrStore 防并发首包重复建流；竞争失败方弃用新建
	if actual, loaded := p.flows.LoadOrStore(flowID, flow); loaded {
		_ = uplink.Close()
		return actual.(*udpClientFlow)
	}
	go flow.pump()
	return flow
}

// sweepIdleFlows 回收超时空闲 flow（本地服务无回包即无流量）
func (p *udpProxy) sweepIdleFlows() {
	deadline := time.Now().Add(-p.idleTimeout).UnixNano()
	p.flows.Range(func(key, value interface{}) bool {
		if value.(*udpClientFlow).lastSeen.Load() < deadline {
			value.(*udpClientFlow).remove()
		}
		return true
	})
}

// closeAll 回收全部 flow
func (p *udpProxy) closeAll() {
	p.flows.Range(func(key, value interface{}) bool {
		value.(*udpClientFlow).remove()
		return true
	})
}

// runUDPProxy 启动 UDP 转发循环（服务端 → 本地服务方向 + flow 清理），阻塞至 ctx 取消或 channel 断开。
// 本地服务 → 服务端方向由每条 flow 的 pump goroutine 承担。
func runUDPProxy(ctx context.Context, ch ssh.Channel, config *base_core.Config, actualPort uint32) {
	logger := base_core.GetLogger()

	localAddr, err := net.ResolveUDPAddr("udp", net.JoinHostPort(config.LocalHost, fmt.Sprintf("%d", config.LocalPort)))
	if err != nil {
		logger.Error("UDP proxy: invalid local address %s:%d: %v", config.LocalHost, config.LocalPort, err)
		_ = ch.Close()
		return
	}

	p := &udpProxy{
		ch:          ch,
		localAddr:   localAddr,
		idleTimeout: udpFlowIdleTimeout,
	}

	// 清理循环：ctx 取消时关 channel + 全部 flow，各读循环随之退出
	go func() {
		ticker := time.NewTicker(udpFlowCleanupInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				_ = ch.Close()
				p.closeAll()
				return
			case <-ticker.C:
				p.sweepIdleFlows()
			}
		}
	}()

	logger.Info("[UDP] Data plane started (mode=channel) - Remote listener: 0.0.0.0:%d, Local target: %s:%d",
		actualPort, config.LocalHost, config.LocalPort)

	// 服务端 → 本地服务方向：解帧 → 按 flow_id 建流写入
	for {
		flowID, payload, err := readUDPFrame(ch)
		if err != nil {
			logger.Info("[UDP] Data channel closed: %v", err)
			_ = ch.Close()
			p.closeAll()
			return
		}
		flow := p.getOrCreateFlow(flowID)
		if flow == nil {
			base_core.GetLogger().Debug("[UDP] local dial failed (%s), packet dropped", localAddr)
			continue
		}
		if len(payload) == 0 {
			continue
		}
		if _, err := flow.uplink.Write(payload); err != nil {
			// socket 异常：回收 flow，服务端同 flow 下个包重建
			flow.remove()
		}
	}
}
