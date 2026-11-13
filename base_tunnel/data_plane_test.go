package base_tunnel

import (
	"bytes"
	"encoding/binary"
	"io"
	"testing"
)

// buildDataOpenFrame 构造与节点侧一致的 DATA_OPEN 帧（客户端只解析不发送）
func buildDataOpenFrame(connID uint64, port uint32, host string, originPort uint32) []byte {
	buf := make([]byte, 1+8+4+2+len(host)+4)
	buf[0] = dataFrameTypeOpen
	binary.BigEndian.PutUint64(buf[1:9], connID)
	binary.BigEndian.PutUint32(buf[9:13], port)
	binary.BigEndian.PutUint16(buf[13:15], uint16(len(host)))
	copy(buf[15:15+len(host)], host)
	binary.BigEndian.PutUint32(buf[15+len(host):], originPort)
	return buf
}

// TestReadDataOpenFrame 验证 DATA_OPEN 解析（含 originHost 变长字段）
func TestReadDataOpenFrame(t *testing.T) {
	f, err := readDataOpenFrame(bytes.NewReader(buildDataOpenFrame(99, 8080, "192.168.1.10", 4567)))
	if err != nil {
		t.Fatalf("readDataOpenFrame: %v", err)
	}
	if f.connID != 99 || f.port != 8080 || f.originHost != "192.168.1.10" || f.originPort != 4567 {
		t.Fatalf("unexpected frame: %+v", f)
	}
}

// TestReadDataOpenFrameTruncated 截断帧必须报错（不能把残缺字段当有效请求）
func TestReadDataOpenFrameTruncated(t *testing.T) {
	frame := buildDataOpenFrame(1, 2, "host", 3)
	if _, err := readDataOpenFrame(bytes.NewReader(frame[:len(frame)-2])); err == nil {
		t.Fatal("expected error for truncated frame")
	}
}

// TestReadDataOpenFrameUnknownType 未知帧类型报错
func TestReadDataOpenFrameUnknownType(t *testing.T) {
	buf := make([]byte, 9)
	buf[0] = 9
	if _, err := readDataOpenFrame(bytes.NewReader(buf)); err == nil {
		t.Fatal("expected error for unknown frame type")
	}
}

// TestEncodeDataErrorFrame 验证 DATA_ERROR 布局（type + connID + len + reason）
func TestEncodeDataErrorFrame(t *testing.T) {
	frame := encodeDataErrorFrame(5, "boom")
	if frame[0] != dataFrameTypeError {
		t.Fatalf("unexpected type %d", frame[0])
	}
	if got := binary.BigEndian.Uint64(frame[1:9]); got != 5 {
		t.Fatalf("unexpected connID %d", got)
	}
	if got := int(binary.BigEndian.Uint16(frame[9:11])); got != 4 {
		t.Fatalf("unexpected reason len %d", got)
	}
	if string(frame[11:]) != "boom" {
		t.Fatalf("unexpected reason %q", frame[11:])
	}
}

// TestDataPlaneEnabledEnv 灰度开关：off/0/false/disabled 关闭，其余启用
func TestDataPlaneEnabledEnv(t *testing.T) {
	for _, v := range []string{"off", "OFF", "0", "false", "disabled"} {
		t.Setenv("NSSH_DATA_PLANE", v)
		if dataPlaneEnabled() {
			t.Fatalf("expected disabled for %q", v)
		}
	}
	for _, v := range []string{"", "auto", "channel", "1"} {
		t.Setenv("NSSH_DATA_PLANE", v)
		if !dataPlaneEnabled() {
			t.Fatalf("expected enabled for %q", v)
		}
	}
}

var _ io.ReadWriteCloser = (*quicDataConn)(nil)
