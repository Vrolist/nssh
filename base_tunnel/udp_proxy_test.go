package base_tunnel

import (
	"bytes"
	"encoding/binary"
	"io"
	"net"
	"sync"
	"testing"
	"time"
)

// 帧编解码：flow_id / len 边界值
func TestUDPFrameRoundTrip(t *testing.T) {
	cases := []struct {
		name    string
		flowID  uint32
		payload []byte
	}{
		{"zero flow zero len", 0, []byte{}},
		{"max flow id", 0xFFFFFFFF, []byte{0x01, 0x02}},
		{"empty payload", 7, nil},
		{"max size payload", 42, bytes.Repeat([]byte{0xAB}, udpMaxPacketSize)},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			frame := encodeUDPFrame(c.flowID, c.payload)
			if len(frame) != udpFrameHeaderLen+len(c.payload) {
				t.Fatalf("frame length = %d, want %d", len(frame), udpFrameHeaderLen+len(c.payload))
			}
			if got := binary.BigEndian.Uint32(frame[0:4]); got != c.flowID {
				t.Fatalf("flowID = %d, want %d", got, c.flowID)
			}
			if got := binary.BigEndian.Uint16(frame[4:6]); int(got) != len(c.payload) {
				t.Fatalf("len = %d, want %d", got, len(c.payload))
			}
			if !bytes.Equal(frame[udpFrameHeaderLen:], c.payload) {
				t.Fatal("payload mismatch")
			}

			flowID, payload, err := readUDPFrame(bytes.NewReader(frame))
			if err != nil {
				t.Fatalf("readUDPFrame: %v", err)
			}
			if flowID != c.flowID {
				t.Fatalf("decoded flowID = %d, want %d", flowID, c.flowID)
			}
			if !bytes.Equal(payload, c.payload) {
				t.Fatal("decoded payload mismatch")
			}
		})
	}
}

// 连续多帧流式读取（模拟 SSH channel 字节流）
func TestReadUDPFrameStream(t *testing.T) {
	var buf bytes.Buffer
	for i := 0; i < 100; i++ {
		buf.Write(encodeUDPFrame(uint32(i), bytes.Repeat([]byte{byte(i)}, i)))
	}
	for i := 0; i < 100; i++ {
		flowID, payload, err := readUDPFrame(&buf)
		if err != nil {
			t.Fatalf("frame %d: %v", i, err)
		}
		if flowID != uint32(i) || len(payload) != i {
			t.Fatalf("frame %d: flowID=%d len=%d", i, flowID, len(payload))
		}
	}
	if buf.Len() != 0 {
		t.Fatalf("%d bytes left in stream", buf.Len())
	}
}

// 截断流（半帧）应返回错误而非 panic
func TestReadUDPFrameTruncated(t *testing.T) {
	full := encodeUDPFrame(1, []byte("hello"))
	if _, _, err := readUDPFrame(bytes.NewReader(full[:4])); err == nil {
		t.Fatal("truncated header should error")
	}
	if _, _, err := readUDPFrame(bytes.NewReader(full[:udpFrameHeaderLen+2])); err == nil {
		t.Fatal("truncated payload should error")
	}
}

// flow 表：并发首包只建一条流（粘性），不同 flow_id 各自独立
func TestUDPProxyFlowConcurrentCreate(t *testing.T) {
	localEcho := newLocalEchoServer(t)
	defer localEcho.Close()

	p := &udpProxy{ch: &noopChannel{}, localAddr: localEcho.addr, idleTimeout: udpFlowIdleTimeout}

	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 50; i++ {
				f := p.getOrCreateFlow(7)
				if f == nil {
					t.Error("flow create failed")
					return
				}
			}
		}()
	}
	wg.Wait()

	if n := countFlows(&p.flows); n != 1 {
		t.Fatalf("flow count = %d, want 1", n)
	}

	// 不同 flow_id 独立建流
	for id := uint32(1); id <= 5; id++ {
		if p.getOrCreateFlow(id) == nil {
			t.Fatalf("flow %d create failed", id)
		}
	}
	if n := countFlows(&p.flows); n != 6 {
		t.Fatalf("flow count = %d, want 6", n)
	}
	p.closeAll()
	if n := countFlows(&p.flows); n != 0 {
		t.Fatalf("after closeAll flow count = %d, want 0", n)
	}
}

// flow 空闲清理：超过 idleTimeout 未活动的 flow 被回收
func TestUDPProxyIdleSweep(t *testing.T) {
	localEcho := newLocalEchoServer(t)
	defer localEcho.Close()

	p := &udpProxy{ch: &noopChannel{}, localAddr: localEcho.addr, idleTimeout: 50 * time.Millisecond}
	if p.getOrCreateFlow(1) == nil {
		t.Fatal("flow create failed")
	}
	time.Sleep(120 * time.Millisecond)
	p.sweepIdleFlows()
	if n := countFlows(&p.flows); n != 0 {
		t.Fatalf("idle flow not swept, count = %d", n)
	}
}

// 本地回环 echo：帧写入 flow uplink → 本地服务回包 → pump 帧化后写入 channel
// （验证「flow socket ↔ pump → channel」整条客户端下行链路）
func TestUDPProxyFlowEcho(t *testing.T) {
	localEcho := newLocalEchoServer(t)
	defer localEcho.Close()

	ch := &bufChannel{}
	p := &udpProxy{ch: ch, localAddr: localEcho.addr, idleTimeout: udpFlowIdleTimeout}
	f := p.getOrCreateFlow(9)
	if f == nil {
		t.Fatal("flow create failed")
	}
	if _, err := f.uplink.Write([]byte("ping")); err != nil {
		t.Fatalf("uplink write: %v", err)
	}

	// pump 收到回包后应向 channel 写出帧：flowID=9 + "ping"
	deadline := time.Now().Add(2 * time.Second)
	for ch.Len() < udpFrameHeaderLen+len("ping") {
		if time.Now().After(deadline) {
			t.Fatalf("no frame written to channel, got %d bytes", ch.Len())
		}
		time.Sleep(10 * time.Millisecond)
	}
	flowID, payload, err := readUDPFrame(bytes.NewReader(ch.Snapshot()))
	if err != nil {
		t.Fatalf("decode frame: %v", err)
	}
	if flowID != 9 || string(payload) != "ping" {
		t.Fatalf("frame flowID=%d payload=%q, want 9/ping", flowID, payload)
	}
}

// --- 测试辅助 ---

// noopChannel 空转 channel（flow 表单测用，吞掉 pump 写出的帧）
type noopChannel struct{}

func (c *noopChannel) Read(_ []byte) (int, error)                     { return 0, io.EOF }
func (c *noopChannel) Write(b []byte) (int, error)                    { return len(b), nil }
func (c *noopChannel) Close() error                                   { return nil }
func (c *noopChannel) CloseWrite() error                              { return nil }
func (c *noopChannel) SendRequest(string, bool, []byte) (bool, error) { return true, nil }
func (c *noopChannel) Extended() io.ReadWriter                        { return nil }
func (c *noopChannel) Stderr() io.ReadWriter                          { return nil }

// bufChannel 记录写入帧的 channel（pump 输出断言用）
type bufChannel struct {
	mu  sync.Mutex
	buf []byte
}

func (c *bufChannel) Read(_ []byte) (int, error) { return 0, io.EOF }
func (c *bufChannel) Write(b []byte) (int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.buf = append(c.buf, b...)
	return len(b), nil
}
func (c *bufChannel) Close() error                                   { return nil }
func (c *bufChannel) CloseWrite() error                              { return nil }
func (c *bufChannel) SendRequest(string, bool, []byte) (bool, error) { return true, nil }
func (c *bufChannel) Extended() io.ReadWriter                        { return nil }
func (c *bufChannel) Stderr() io.ReadWriter                          { return nil }
func (c *bufChannel) Len() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.buf)
}
func (c *bufChannel) Snapshot() []byte {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]byte(nil), c.buf...)
}

// newLocalEchoServer 本地 UDP echo 服务（回环测试用）
func newLocalEchoServer(t *testing.T) *localEchoServer {
	t.Helper()
	conn, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 0})
	if err != nil {
		t.Fatalf("listen echo server: %v", err)
	}
	srv := &localEchoServer{conn: conn}
	go func() {
		buf := make([]byte, 2048)
		for {
			n, from, err := conn.ReadFromUDP(buf)
			if err != nil {
				return
			}
			if _, err := conn.WriteToUDP(buf[:n], from); err != nil {
				return
			}
		}
	}()
	host, portStr, _ := net.SplitHostPort(conn.LocalAddr().String())
	port, _ := net.ResolveUDPAddr("udp", net.JoinHostPort(host, portStr))
	srv.addr = port
	return srv
}

type localEchoServer struct {
	conn *net.UDPConn
	addr *net.UDPAddr
}

func (s *localEchoServer) Close() { _ = s.conn.Close() }

func countFlows(m *sync.Map) int {
	n := 0
	m.Range(func(key, value interface{}) bool {
		n++
		return true
	})
	return n
}
