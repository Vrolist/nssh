package base_tunnel

import (
	"bytes"
	"context"
	"encoding/binary"
	"sync"
	"testing"
	"time"
)

// 阶段5 QUIC DATAGRAM 数据面单测（客户端）：
// - 载荷解析/端口头/sendDatagramFrame 编码
// - datagram 解帧：整包/保活刷 lastSeen/分片重组
// - readDatagrams 端到端：datagram 入 → 本地 echo → datagram 出
// - datagram 非 nil 时数据面走 DATAGRAM，channel 基线不受影响（回落对照）

// fakeDatagram 假 QUIC DATAGRAM：记录 SendDatagram；ReceiveDatagram 从 recvCh 取。
type fakeDatagram struct {
	mu       sync.Mutex
	sent     [][]byte
	recvCh   chan []byte
	supports bool
}

func newFakeDatagram(supports bool) *fakeDatagram {
	return &fakeDatagram{recvCh: make(chan []byte, 16), supports: supports}
}

func (d *fakeDatagram) SendDatagram(payload []byte) error {
	cp := make([]byte, len(payload))
	copy(cp, payload)
	d.mu.Lock()
	d.sent = append(d.sent, cp)
	d.mu.Unlock()
	return nil
}

func (d *fakeDatagram) ReceiveDatagram(ctx context.Context) ([]byte, error) {
	select {
	case b := <-d.recvCh:
		return b, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (d *fakeDatagram) SupportsDatagrams() bool { return d.supports }

func (d *fakeDatagram) count() int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return len(d.sent)
}

func (d *fakeDatagram) last() []byte {
	d.mu.Lock()
	defer d.mu.Unlock()
	if len(d.sent) == 0 {
		return nil
	}
	return d.sent[len(d.sent)-1]
}

// 端口头：datagram 载荷 = 2B 大端端口 + v2 帧
func TestUDPDatagramSendFramePortHeader(t *testing.T) {
	d := newFakeDatagram(true)
	p := &udpProxy{datagram: d, port: 8080, fragEnabled: true}
	meta := udpFrameMeta{flowID: 0x11223344, fragCnt: 1}
	payload := []byte("hello")
	if !p.sendDatagramFrame(meta, payload) {
		t.Fatal("sendDatagramFrame returned false")
	}
	got := d.last()
	want := append([]byte{0x1F, 0x90}, encodeUDPFrameV2(meta, payload)...)
	if !bytes.Equal(got, want) {
		t.Fatalf("datagram = %x, want %x", got, want)
	}
	if port := binary.BigEndian.Uint16(got[0:2]); port != 8080 {
		t.Fatalf("port header = %d, want 8080", port)
	}
}

// datagram == nil 时 sendDatagramFrame 返回 false（通道模式不误入 datagram 路径）
func TestUDPDatagramSendFrameNil(t *testing.T) {
	p := &udpProxy{port: 1}
	if p.sendDatagramFrame(udpFrameMeta{flowID: 1, fragCnt: 1}, []byte("x")) {
		t.Fatal("nil datagram should return false")
	}
}

// parseUDPFrameV2Bytes：过短 / 长度字段越界 / 正常
func TestUDPParseFrameV2Bytes(t *testing.T) {
	if _, _, ok := parseUDPFrameV2Bytes(make([]byte, udpFrameHeaderLenV2-1)); ok {
		t.Fatal("short slice should be invalid")
	}
	// 长度字段声称超出实际切片
	bad := encodeUDPFrameV2(udpFrameMeta{flowID: 1, fragCnt: 1}, []byte("hello"))
	if _, _, ok := parseUDPFrameV2Bytes(bad[:len(bad)-2]); ok {
		t.Fatal("len field beyond slice should be invalid")
	}
	good := encodeUDPFrameV2(udpFrameMeta{flowID: 7, fragCnt: 1}, []byte("ok"))
	meta, payload, ok := parseUDPFrameV2Bytes(good)
	if !ok || meta.flowID != 7 || string(payload) != "ok" {
		t.Fatalf("parse: meta=%+v payload=%q ok=%v", meta, payload, ok)
	}
}

// 整包 datagram：直接返回 flowID + payload
func TestUDPDecodeDatagramBytesPlain(t *testing.T) {
	p := &udpProxy{fragEnabled: true}
	frame := encodeUDPFrameV2(udpFrameMeta{flowID: 42, fragCnt: 1}, []byte("ping"))
	flowID, payload, ok := p.decodeDatagramBytes(frame)
	if !ok || flowID != 42 || string(payload) != "ping" {
		t.Fatalf("decode: flowID=%d payload=%q ok=%v", flowID, payload, ok)
	}
}

// 保活帧：只刷 flow lastSeen，不透传（ok=false）
func TestUDPDecodeDatagramBytesKeepalive(t *testing.T) {
	d := newFakeDatagram(true)
	p := &udpProxy{datagram: d, fragEnabled: true}
	flow := &udpClientFlow{flowID: 9, proxy: p}
	flow.lastSeen.Store(1) // 陈旧
	p.flows.Store(uint32(9), flow)

	frame := encodeUDPFrameV2(udpFrameMeta{flowID: 9, flags: udpFrameFlagKeepalive, fragCnt: 1}, nil)
	if _, _, ok := p.decodeDatagramBytes(frame); ok {
		t.Fatal("keepalive should not deliver payload")
	}
	if flow.lastSeen.Load() == 1 {
		t.Fatal("keepalive did not refresh flow lastSeen")
	}
}

// 分片 datagram：乱序收齐才交付，按序拼接
func TestUDPDecodeDatagramBytesFragment(t *testing.T) {
	p := &udpProxy{fragEnabled: true, reasm: newUDPReassembler()}
	mk := func(idx byte) []byte {
		return encodeUDPFrameV2(udpFrameMeta{flowID: 5, flags: udpFrameFlagFragment, fragID: 1, fragIdx: idx, fragCnt: 3}, []byte{byte('A' + idx)})
	}
	if _, _, ok := p.decodeDatagramBytes(mk(2)); ok {
		t.Fatal("incomplete should not deliver")
	}
	if _, _, ok := p.decodeDatagramBytes(mk(0)); ok {
		t.Fatal("incomplete should not deliver")
	}
	flowID, payload, ok := p.decodeDatagramBytes(mk(1))
	if !ok || flowID != 5 || string(payload) != "ABC" {
		t.Fatalf("reassembled: flowID=%d payload=%q ok=%v", flowID, payload, ok)
	}
}

// readDatagrams 端到端：datagram 入 → 本地 echo → 下行帧经 QUIC DATAGRAM 出
func TestUDPReadDatagramsRoundTrip(t *testing.T) {
	localEcho := newLocalEchoServer(t)
	defer localEcho.Close()

	d := newFakeDatagram(true)
	p := &udpProxy{
		datagram:       d,
		port:           7000,
		localAddr:      localEcho.addr,
		idleTimeout:    udpFlowIdleTimeout,
		fragEnabled:    true,
		fragPayloadMax: udpDatagramFramePayloadMax,
		reasm:          newUDPReassembler(),
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go p.readDatagrams(ctx)

	// 构造上行 datagram：2B 端口头 + v2 整包帧（flowID=3, "ping"）
	frame := encodeUDPFrameV2(udpFrameMeta{flowID: 3, fragCnt: 1}, []byte("ping"))
	buf := make([]byte, udpDatagramPortHeaderLen+len(frame))
	binary.BigEndian.PutUint16(buf[0:2], 7000)
	copy(buf[udpDatagramPortHeaderLen:], frame)
	d.recvCh <- buf

	deadline := time.Now().Add(2 * time.Second)
	for d.count() == 0 {
		if time.Now().After(deadline) {
			t.Fatal("no downlink datagram sent")
		}
		time.Sleep(10 * time.Millisecond)
	}
	out := d.last()
	if port := binary.BigEndian.Uint16(out[0:2]); port != 7000 {
		t.Fatalf("downlink port header = %d, want 7000", port)
	}
	meta, payload, ok := parseUDPFrameV2Bytes(out[udpDatagramPortHeaderLen:])
	if !ok || meta.flowID != 3 || string(payload) != "ping" {
		t.Fatalf("downlink frame: meta=%+v payload=%q ok=%v", meta, payload, ok)
	}
}

// 回落对照：datagram == nil 时 writeFrameV2 仍写 channel（基线不受影响）
func TestUDPWriteFrameV2ChannelWhenNoDatagram(t *testing.T) {
	ch := &bufChannel{}
	p := &udpProxy{ch: ch, fragEnabled: true, fragPayloadMax: 100}
	if !p.writeFrameV2(udpFrameMeta{flowID: 1, fragCnt: 1}, []byte("x")) {
		t.Fatal("writeFrameV2 returned false")
	}
	if ch.Len() == 0 {
		t.Fatal("channel path should receive the frame when datagram is nil")
	}
}

// datagram 非 nil 时 writeFrameV2 走 DATAGRAM，不写 channel
func TestUDPWriteFrameV2UsesDatagram(t *testing.T) {
	ch := &bufChannel{}
	d := newFakeDatagram(true)
	p := &udpProxy{ch: ch, datagram: d, port: 321, fragEnabled: true}
	if !p.writeFrameV2(udpFrameMeta{flowID: 1, fragCnt: 1}, []byte("x")) {
		t.Fatal("writeFrameV2 returned false")
	}
	if ch.Len() != 0 {
		t.Fatalf("channel should be untouched in datagram mode, got %d bytes", ch.Len())
	}
	if d.count() != 1 {
		t.Fatalf("datagram sends = %d, want 1", d.count())
	}
}
