package base_tunnel

import (
	"context"
	"encoding/binary"
	"time"

	"github.com/Vrolist/nssh/base_core"
)

// UDP 业务穿透（阶段5）：QUIC DATAGRAM 数据面。
//
// 背景：channel 基线把 UDP 报文经 SSH stream（可靠有序）搬运，存在队头阻塞与可靠化代价。
// 阶段5 在 QUIC 传输且两端协商支持 RFC9221 时改走不可靠数据报直发，贴合 UDP 语义。
//
// 载荷格式（datagram 内容 = 2B 端口头 + v2 帧）：
//   [port 2B 大端][v2 帧：flow_id4 + flags1 + frag_id2 + frag_idx1 + frag_cnt1 + len2 + payload]
// - port：服务端监听的业务端口，节点侧据此路由到对应 udpPortForward；
// - v2 帧：与 channel 基线完全同构，复用 O3 分片/重组（>PMTU 的报文切分为多条 datagram）。
//
// 约束：QUIC 单条 datagram 受 PMTU 限制（两端 advertise ≤1200），故 datagram 模式的有效分片
// 上限收敛为 udpDatagramFramePayloadMax，确保内容（2+11+payload）不超 MTU。
//
// 回落：双端未同时置 bit2 或传输非 QUIC → setupUDPForward 不启用 datagram，数据面走 channel 基线。

const (
	// udpDatagramPortHeaderLen datagram 载荷端口头长度（2B 大端）
	udpDatagramPortHeaderLen = 2
	// udpDatagramFramePayloadMax datagram 模式下单帧 payload 上限：
	// 内容 = 2(port) + 11(v2 头) + payload = 13 + payload，取值保证 ≤ QUIC advertise 的 1200（留余量）。
	udpDatagramFramePayloadMax = 1136
)

// sendDatagramFrame 把一帧 v2（前加 2B 端口头）经 QUIC DATAGRAM 发出。
// 返回 false 表示发送失败（连接断开），调用方回收 flow。
func (p *udpProxy) sendDatagramFrame(meta udpFrameMeta, payload []byte) bool {
	if p.datagram == nil {
		return false
	}
	frame := encodeUDPFrameV2(meta, payload)
	buf := make([]byte, udpDatagramPortHeaderLen+len(frame))
	binary.BigEndian.PutUint16(buf[0:2], uint16(p.port))
	copy(buf[udpDatagramPortHeaderLen:], frame)
	if err := p.datagram.SendDatagram(buf); err != nil {
		return false
	}
	return true
}

// parseUDPFrameV2Bytes 从字节切片解析一帧 v2（datagram 载荷为独立一帧，边界即切片长度）。
// ok=false 表示切片过短/长度字段越界。
func parseUDPFrameV2Bytes(b []byte) (udpFrameMeta, []byte, bool) {
	var meta udpFrameMeta
	if len(b) < udpFrameHeaderLenV2 {
		return meta, nil, false
	}
	meta.flowID = binary.BigEndian.Uint32(b[0:4])
	meta.flags = b[4]
	meta.fragID = binary.BigEndian.Uint16(b[5:7])
	meta.fragIdx = b[7]
	meta.fragCnt = b[8]
	length := int(binary.BigEndian.Uint16(b[9:11]))
	if udpFrameHeaderLenV2+length > len(b) {
		return meta, nil, false
	}
	payload := make([]byte, length)
	copy(payload, b[udpFrameHeaderLenV2:udpFrameHeaderLenV2+length])
	return meta, payload, true
}

// decodeDatagramBytes 解析一条 datagram 载荷（已去端口头）为完整 UDP 报文：
// 保活帧只刷 lastSeen 不透传；分片帧走重组（收齐才返回）。ok=false 表示该帧被丢弃（继续读）。
func (p *udpProxy) decodeDatagramBytes(b []byte) (flowID uint32, payload []byte, ok bool) {
	meta, frag, valid := parseUDPFrameV2Bytes(b)
	if !valid {
		return 0, nil, false
	}
	if meta.flags&udpFrameFlagKeepalive != 0 {
		if val, exists := p.flows.Load(meta.flowID); exists {
			val.(*udpClientFlow).lastSeen.Store(time.Now().UnixNano())
		}
		return 0, nil, false
	}
	if meta.flags&udpFrameFlagFragment == 0 {
		return meta.flowID, frag, true
	}
	datagram, complete := p.reasm.add(meta.flowID, meta, frag)
	if !complete {
		return 0, nil, false
	}
	return meta.flowID, datagram, true
}

// readDatagrams 阶段5 数据面读循环（服务端 → 本地服务方向）：
// ReceiveDatagram 收一条 → 去 2B 端口头 → 解帧/重组 → 按 flow_id 建流写入本地服务。
// 连接关闭时 quic-go 的 datagram 队列 CloseWithError 使 ReceiveDatagram 返回错误，循环退出。
func (p *udpProxy) readDatagrams(ctx context.Context) {
	logger := base_core.GetLogger()
	for {
		buf, err := p.datagram.ReceiveDatagram(ctx)
		if err != nil {
			logger.Info("[UDP] Datagram receive loop ended: %v", err)
			return
		}
		if len(buf) <= udpDatagramPortHeaderLen {
			continue // 空载荷/仅端口头：丢弃
		}
		// port 头仅节点→客户端方向承载时使用；客户端侧单转发器实例无需再按 port 分流，直接去头
		flowID, payload, ok := p.decodeDatagramBytes(buf[udpDatagramPortHeaderLen:])
		if !ok {
			continue
		}
		flow := p.getOrCreateFlow(flowID)
		if flow == nil {
			logger.Debug("[UDP] local dial failed (%s), datagram dropped", p.localAddr)
			continue
		}
		if len(payload) == 0 {
			continue
		}
		if _, err := flow.uplink.Write(payload); err != nil {
			flow.remove()
		}
	}
}
