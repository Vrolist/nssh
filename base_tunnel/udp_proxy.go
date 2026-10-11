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
// - 数据面：udp-data@nssh channel + 应用层帧
// - flow：一条 UDP 会话（外部五元组）；flow_id 由服务端分配，客户端按需建本地 connected socket
// - 语义代价：UDP 被 SSH stream 可靠有序化（OpenVPN 等 VPN 业务可接受，见设计稿「语义代价」）
//
// 帧格式（O3 隧道层分片）：
//   v1（6B）：flow_id(4B) + len(2B) + payload（未协商分片时使用，单报文 ≤ udpMaxPacketSize）
//   v2（11B）：flow_id(4B) + flags(1B) + frag_id(2B) + frag_idx(1B) + frag_cnt(1B) + len(2B) + payload
// 能力协商：udp-forward@nssh 请求/回复带 bit1=frag，**双端均置位**才启用 v2；否则回落 v1。
//
// 兼容红线：TCP 转发路径（forwarded-tcpip）一行不动；老服务端收不到 udp-forward@nssh
// 时 SendRequest 返回 ok=false，客户端明确报错（不静默回退）；老服务端不识别 bit1 → 回落 v1。

const (
	// udpForwardRequest 控制面：声明 UDP 转发端口（global request）
	udpForwardRequest = "udp-forward@nssh"
	// udpChannelType 数据面：UDP 数据通道（channel 类型）
	udpChannelType = "udp-data@nssh"

	// udpModeChannel 服务端 reply 的数据面 mode：channel 基线（阶段1客户端仅支持该模式）
	udpModeChannel byte = 1

	// udpCapsChannel 客户端能力位 bit0：支持 channel 模式（恒 1）
	udpCapsChannel byte = 0x01
	// udpCapsFrag 客户端能力位 bit1：支持分片帧（O3，与节点侧 bit1 对齐）
	udpCapsFrag byte = 0x02

	// udpMaxPacketSize 未协商分片时的单报文上限（v1 口径，超出丢弃；OpenVPN 建议配 tun-mtu 1200）
	udpMaxPacketSize = 1400
	// udpMaxDatagramSize UDP 载荷理论上限：协商分片后单条报文（重组后）可达此值
	udpMaxDatagramSize = 65507

	// udpFrameHeaderLen v1 帧头长度：flow_id 4B + len 2B
	udpFrameHeaderLen = 6
	// udpFrameHeaderLenV2 v2 帧头长度：flow_id 4B + flags 1B + frag_id 2B + frag_idx 1B + frag_cnt 1B + len 2B
	udpFrameHeaderLenV2 = 11
	// udpFrameFlagFragment v2 flags bit0：1 = 分片
	udpFrameFlagFragment byte = 0x01

	// udpReassemblyTimeout 重组槽超时（收不齐即丢整槽，不重传）
	udpReassemblyTimeout = 300 * time.Millisecond
	// udpMaxReassembliesPerFlow 并发重组槽上限（防内存放大）
	udpMaxReassembliesPerFlow = 16

	// udpFlowIdleTimeout flow 空闲回收：> 服务端 QUIC 空闲 300s + keepalive 30s，与 L4LB UDP 对齐
	udpFlowIdleTimeout = 330 * time.Second
	// udpFlowCleanupInterval flow 空闲扫描周期
	udpFlowCleanupInterval = 30 * time.Second
)

// udpReadBufPool 读缓冲池（v1）：pump 每 flow 一个 65KB 缓冲是 flow 规模化的内存大头，
// 改为按单报文上限（udpMaxPacketSize）尺寸复用。多出 1 字节用于识别超限报文
// （读满即判超限丢弃，与原「读入大缓冲再比较」的可观测行为一致）。
var udpReadBufPool = sync.Pool{
	New: func() interface{} { return make([]byte, udpMaxPacketSize+1) },
}

// udpReadBufPoolLarge 读缓冲池（v2 分片）：协商分片后 socket 读缓冲须容纳完整报文
// （UDP 载荷理论上限 udpMaxDatagramSize），多 1 字节识别超限。
var udpReadBufPoolLarge = sync.Pool{
	New: func() interface{} { return make([]byte, udpMaxDatagramSize+1) },
}

// udpFrameMeta v2 帧头元数据
type udpFrameMeta struct {
	flowID  uint32
	flags   byte
	fragID  uint16
	fragIdx byte
	fragCnt byte
}

// encodeUDPFrame 编码一帧 v1 UDP 数据：flow_id(4B 大端) + len(2B 大端) + payload。
// 调用方须保证 len(payload) <= udpMaxPacketSize（socket 读侧已丢弃超限报文）。
func encodeUDPFrame(flowID uint32, payload []byte) []byte {
	frame := make([]byte, udpFrameHeaderLen+len(payload))
	binary.BigEndian.PutUint32(frame[0:4], flowID)
	binary.BigEndian.PutUint16(frame[4:6], uint16(len(payload)))
	copy(frame[udpFrameHeaderLen:], payload)
	return frame
}

// readUDPFrame 从流式 reader（SSH channel）读取一帧 v1。
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

// encodeUDPFrameV2 编码一帧 v2（含分片元数据）：见文件头帧格式说明。
func encodeUDPFrameV2(meta udpFrameMeta, payload []byte) []byte {
	frame := make([]byte, udpFrameHeaderLenV2+len(payload))
	binary.BigEndian.PutUint32(frame[0:4], meta.flowID)
	frame[4] = meta.flags
	binary.BigEndian.PutUint16(frame[5:7], meta.fragID)
	frame[7] = meta.fragIdx
	frame[8] = meta.fragCnt
	binary.BigEndian.PutUint16(frame[9:11], uint16(len(payload)))
	copy(frame[udpFrameHeaderLenV2:], payload)
	return frame
}

// readUDPFrameV2 从流式 reader 读取一帧 v2；返回元数据与 payload（独立副本）。
func readUDPFrameV2(r io.Reader) (meta udpFrameMeta, payload []byte, err error) {
	var hdr [udpFrameHeaderLenV2]byte
	if _, err = io.ReadFull(r, hdr[:]); err != nil {
		return meta, nil, err
	}
	meta.flowID = binary.BigEndian.Uint32(hdr[0:4])
	meta.flags = hdr[4]
	meta.fragID = binary.BigEndian.Uint16(hdr[5:7])
	meta.fragIdx = hdr[7]
	meta.fragCnt = hdr[8]
	length := int(binary.BigEndian.Uint16(hdr[9:11]))
	payload = make([]byte, length)
	if length > 0 {
		if _, err = io.ReadFull(r, payload); err != nil {
			return meta, nil, err
		}
	}
	return meta, payload, nil
}

// udpReassemblySlot 一条报文的乱序重组槽
type udpReassemblySlot struct {
	fragCnt  int
	frags    [][]byte
	count    int
	bytes    int
	lastSeen time.Time
}

// udpReassembler 分片重组器（每个 udpProxy 一个；按 (flow_id, frag_id) 归并）。
type udpReassembler struct {
	mu            sync.Mutex
	slots         map[uint64]*udpReassemblySlot
	maxConcurrent int
	timeout       time.Duration
	maxDatagram   int
}

func newUDPReassembler() *udpReassembler {
	return &udpReassembler{
		slots:         make(map[uint64]*udpReassemblySlot),
		maxConcurrent: udpMaxReassembliesPerFlow,
		timeout:       udpReassemblyTimeout,
		maxDatagram:   udpMaxDatagramSize,
	}
}

func udpReassemblyKey(flowID uint32, fragID uint16) uint64 {
	return uint64(flowID)<<16 | uint64(fragID)
}

// add 收一片：收齐返回整条报文 (datagram, true)；否则 (nil, false)。
// 异常（越界/重复/超限/槽超时/并发超限/frag_cnt 不一致）一律丢弃整槽（UDP 语义，不重传）。
func (r *udpReassembler) add(flowID uint32, meta udpFrameMeta, payload []byte) ([]byte, bool) {
	fragCnt := int(meta.fragCnt)
	fragIdx := int(meta.fragIdx)
	if fragCnt < 1 || fragCnt > 255 || fragIdx >= fragCnt {
		return nil, false
	}
	now := time.Now()
	r.mu.Lock()
	defer r.mu.Unlock()

	key := udpReassemblyKey(flowID, meta.fragID)
	slot := r.slots[key]
	if slot != nil && (slot.fragCnt != fragCnt || now.Sub(slot.lastSeen) > r.timeout) {
		// frag_cnt 不一致 或 槽已超时：丢弃旧槽重开
		delete(r.slots, key)
		slot = nil
	}
	if slot == nil {
		if len(r.slots) >= r.maxConcurrent {
			return nil, false // 并发重组数超限：丢弃该片（防内存放大）
		}
		slot = &udpReassemblySlot{fragCnt: fragCnt, frags: make([][]byte, fragCnt), lastSeen: now}
		r.slots[key] = slot
	}
	if slot.frags[fragIdx] != nil {
		delete(r.slots, key) // 重复片：丢弃整槽
		return nil, false
	}
	slot.frags[fragIdx] = payload
	slot.count++
	slot.bytes += len(payload)
	slot.lastSeen = now
	if slot.bytes > r.maxDatagram {
		delete(r.slots, key)
		return nil, false
	}
	if slot.count < slot.fragCnt {
		return nil, false
	}
	// 收齐：按序拼接（frags 未到的槽不会走到这里）
	buf := make([]byte, 0, slot.bytes)
	for _, f := range slot.frags {
		buf = append(buf, f...)
	}
	delete(r.slots, key)
	return buf, true
}

// sweep 回收超时重组槽
func (r *udpReassembler) sweep() {
	now := time.Now()
	r.mu.Lock()
	defer r.mu.Unlock()
	for k, slot := range r.slots {
		if now.Sub(slot.lastSeen) > r.timeout {
			delete(r.slots, k)
		}
	}
}

// udpForwardSession UDP 转发控制面协商结果（数据面 channel + 分片能力）
type udpForwardSession struct {
	ch             ssh.Channel
	actualPort     uint32
	fragEnabled    bool // 双端协商启用 v2 分片帧
	fragPayloadMax int  // 单帧 payload 上限（v2）
}

// setupUDPForward 控制面：声明 UDP 转发端口并打开数据面 channel。
// 返回协商结果（channel + 服务端实际端口 + 分片能力）。
func setupUDPForward(client *ssh.Client, config *base_core.Config) (*udpForwardSession, error) {
	// request payload 与 tcpip-forward 同构（addrLen+addr+port），尾部追加 1 字节能力位
	const bindAddr = "0.0.0.0"
	payload := make([]byte, 0, 4+len(bindAddr)+4+1)
	payload = binary.BigEndian.AppendUint32(payload, uint32(len(bindAddr)))
	payload = append(payload, bindAddr...)
	payload = binary.BigEndian.AppendUint32(payload, uint32(config.RemotePort))
	payload = append(payload, udpCapsChannel|udpCapsFrag) // bit0 恒 1；bit1 声明本端支持分片

	ok, resp, err := client.SendRequest(udpForwardRequest, true, payload)
	if err != nil {
		return nil, fmt.Errorf("udp-forward request failed: %w", err)
	}
	if !ok {
		// 老服务端 / 服务端 UDP 开关关闭：明确报错，不静默
		return nil, fmt.Errorf("server rejected udp-forward@nssh (server too old or UDP forwarding disabled)")
	}
	if len(resp) < 5 {
		return nil, fmt.Errorf("malformed udp-forward reply: %d bytes", len(resp))
	}
	actualPort := binary.BigEndian.Uint32(resp[0:4])
	mode := resp[4]
	if mode != udpModeChannel {
		return nil, fmt.Errorf("server selected unsupported UDP data mode %d", mode)
	}

	sess := &udpForwardSession{actualPort: actualPort}
	// v2 协商：reply 扩展段 [caps 1B][maxPacketSize 2B][fragPayloadMax 2B]（共 10B）；
	// 老服务端只回 5B → 保持 v1（兼容红线）；服务端未回带 frag 位 → 保持 v1。
	if len(resp) >= 10 {
		respCaps := resp[5]
		srvFragMax := int(binary.BigEndian.Uint16(resp[8:10]))
		if respCaps&udpCapsFrag != 0 && srvFragMax > 0 && srvFragMax <= udpMaxDatagramSize {
			sess.fragEnabled = true
			sess.fragPayloadMax = srvFragMax
		}
	}

	// 打开数据面 channel：ExtraData 带服务端实际端口。
	// x/crypto/ssh 两侧接收窗口默认 2MB（channelWindowSize=64×32768），实时转发场景足够，
	// 无需 fork 定制窗口（设计稿 5.5 的 64KB 前提系记忆偏差，实测确认后修正文档）。
	chData := make([]byte, 4)
	binary.BigEndian.PutUint32(chData, actualPort)
	ch, _, err := client.OpenChannel(udpChannelType, chData)
	if err != nil {
		return nil, fmt.Errorf("failed to open %s channel: %w", udpChannelType, err)
	}
	sess.ch = ch
	return sess, nil
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

// pump 本地服务 → 服务端方向：读 connected socket 回包，帧化（按需分片）后写 channel
func (f *udpClientFlow) pump() {
	buf := f.proxy.getReadBuf()
	defer f.proxy.putReadBuf(buf)
	for {
		n, err := f.uplink.Read(buf)
		if err != nil {
			f.remove()
			return
		}
		if !f.proxy.writeDatagram(f.flowID, buf[:n]) {
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

	// socketBuffer UDP flow socket 内核收发缓冲（字节）：>0 时按配置设置，<=0 走内核默认（O1）
	socketBuffer int

	// 分片（O3）：fragEnabled 时使用 v2 帧头与分片/重组
	fragEnabled    bool
	fragPayloadMax int
	nextFragID     atomic.Uint32
	reasm          *udpReassembler

	flows sync.Map // flow_id (uint32) -> *udpClientFlow
}

// getReadBuf/putReadBuf 按是否分片选取对应尺寸的池化读缓冲
func (p *udpProxy) getReadBuf() []byte {
	if p.fragEnabled {
		return udpReadBufPoolLarge.Get().([]byte)
	}
	return udpReadBufPool.Get().([]byte)
}

func (p *udpProxy) putReadBuf(buf []byte) {
	if p.fragEnabled {
		udpReadBufPoolLarge.Put(buf)
		return
	}
	udpReadBufPool.Put(buf)
}

// writeFrame v1 帧 → channel 写入；channel 异常（连接断开）返回 false
func (p *udpProxy) writeFrame(flowID uint32, payload []byte) bool {
	if _, err := p.ch.Write(encodeUDPFrame(flowID, payload)); err != nil {
		return false
	}
	return true
}

// writeFrameV2 v2 帧 → channel 写入
func (p *udpProxy) writeFrameV2(meta udpFrameMeta, payload []byte) bool {
	if _, err := p.ch.Write(encodeUDPFrameV2(meta, payload)); err != nil {
		return false
	}
	return true
}

// writeDatagram 把一条完整 UDP 报文写入 channel：
//   - 未协商分片（v1）：超限（> udpMaxPacketSize）丢弃（非致命，符合 UDP 语义）；
//   - 已协商（v2）：≤ fragPayloadMax 走整包帧，超出按 fragPayloadMax 切分（共享 frag_id）。
// 返回 false 表示 channel 写失败（连接断开，需回收 flow）。
func (p *udpProxy) writeDatagram(flowID uint32, payload []byte) bool {
	if !p.fragEnabled {
		if len(payload) > udpMaxPacketSize {
			return true // 超限丢弃
		}
		return p.writeFrame(flowID, payload)
	}
	if len(payload) > udpMaxDatagramSize || p.fragPayloadMax <= 0 {
		return true
	}
	if len(payload) <= p.fragPayloadMax {
		// 整包：frag_cnt=1, frag_idx=0, flags=0
		return p.writeFrameV2(udpFrameMeta{flowID: flowID, fragCnt: 1}, payload)
	}
	fragCnt := (len(payload) + p.fragPayloadMax - 1) / p.fragPayloadMax
	if fragCnt > 255 {
		return true // 理论不可达（65507/fragPayloadMax 远小于 255），丢弃兜底
	}
	fragID := uint16(p.nextFragID.Add(1))
	for i := 0; i < fragCnt; i++ {
		start := i * p.fragPayloadMax
		end := start + p.fragPayloadMax
		if end > len(payload) {
			end = len(payload)
		}
		meta := udpFrameMeta{
			flowID:  flowID,
			flags:   udpFrameFlagFragment,
			fragID:  fragID,
			fragIdx: byte(i),
			fragCnt: byte(fragCnt),
		}
		if !p.writeFrameV2(meta, payload[start:end]) {
			return false
		}
	}
	return true
}

// readDatagram 从 channel 读取一条完整 UDP 报文（v1 直读；v2 按需重组）。
// ok=false 表示该帧被丢弃（分片未收齐/异常），调用方跳过继续读。
func (p *udpProxy) readDatagram() (flowID uint32, payload []byte, ok bool, err error) {
	if !p.fragEnabled {
		flowID, payload, err = readUDPFrame(p.ch)
		return flowID, payload, err == nil, err
	}
	meta, payload, err := readUDPFrameV2(p.ch)
	if err != nil {
		return 0, nil, false, err
	}
	if meta.flags&udpFrameFlagFragment == 0 {
		return meta.flowID, payload, true, nil
	}
	datagram, complete := p.reasm.add(meta.flowID, meta, payload)
	if !complete {
		return 0, nil, false, nil
	}
	return meta.flowID, datagram, true, nil
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
	if p.socketBuffer > 0 {
		// O1：按配置设置内核收发缓冲（<=0 走内核默认，不显式设置）
		_ = uplink.SetReadBuffer(p.socketBuffer)
		_ = uplink.SetWriteBuffer(p.socketBuffer)
	}

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
func runUDPProxy(ctx context.Context, sess *udpForwardSession, config *base_core.Config) {
	logger := base_core.GetLogger()
	ch := sess.ch

	localAddr, err := net.ResolveUDPAddr("udp", net.JoinHostPort(config.LocalHost, fmt.Sprintf("%d", config.LocalPort)))
	if err != nil {
		logger.Error("UDP proxy: invalid local address %s:%d: %v", config.LocalHost, config.LocalPort, err)
		_ = ch.Close()
		return
	}

	p := &udpProxy{
		ch:             ch,
		localAddr:      localAddr,
		idleTimeout:    udpFlowIdleTimeout,
		socketBuffer:   config.UDPSocketBuffer,
		fragEnabled:    sess.fragEnabled,
		fragPayloadMax: sess.fragPayloadMax,
	}
	if p.fragEnabled {
		p.reasm = newUDPReassembler()
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
				if p.reasm != nil {
					p.reasm.sweep()
				}
			}
		}
	}()

	logger.Info("[UDP] Data plane started (mode=channel, frag=%v) - Remote listener: 0.0.0.0:%d, Local target: %s:%d",
		sess.fragEnabled, sess.actualPort, config.LocalHost, config.LocalPort)

	// 服务端 → 本地服务方向：解帧（按需重组）→ 按 flow_id 建流写入
	for {
		flowID, payload, ok, err := p.readDatagram()
		if err != nil {
			logger.Info("[UDP] Data channel closed: %v", err)
			_ = ch.Close()
			p.closeAll()
			return
		}
		if !ok {
			continue // 分片未收齐/异常帧：丢弃，继续读
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