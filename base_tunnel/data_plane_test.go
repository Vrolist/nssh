package base_tunnel

import (
	"bytes"
	"encoding/binary"
	"io"
	"strings"
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

// TestDataPlanePreferredVersion 版本偏好：显式 v1 回退，其余默认优先 v2
func TestDataPlanePreferredVersion(t *testing.T) {
	for _, v := range []string{"v1", "V1", "1"} {
		t.Setenv("NSSH_DATA_PLANE_VERSION", v)
		if got := dataPlanePreferredVersion(); got != 1 {
			t.Fatalf("expected version 1 for %q, got %d", v, got)
		}
	}
	for _, v := range []string{"", "v2", "2", "auto"} {
		t.Setenv("NSSH_DATA_PLANE_VERSION", v)
		if got := dataPlanePreferredVersion(); got != 2 {
			t.Fatalf("expected version 2 for %q, got %d", v, got)
		}
	}
}

// TestEncodeDataStatusOK v2 成功状态只占 1 字节
func TestEncodeDataStatusOK(t *testing.T) {
	buf := encodeDataStatusOK()
	if len(buf) != 1 || buf[0] != dataStatusOK {
		t.Fatalf("unexpected status ok frame: %v", buf)
	}
}

// TestEncodeDataStatusFail 与节点侧 readDataStatus 约定一致：0x00 + uint16 len + reason
func TestEncodeDataStatusFail(t *testing.T) {
	buf := encodeDataStatusFail("local service unavailable")
	if buf[0] != dataStatusFail {
		t.Fatalf("unexpected status type %d", buf[0])
	}
	if got := int(binary.BigEndian.Uint16(buf[1:3])); got != len("local service unavailable") {
		t.Fatalf("unexpected reason len %d", got)
	}
	if string(buf[3:]) != "local service unavailable" {
		t.Fatalf("unexpected reason %q", buf[3:])
	}
}

// TestDataPlaneChannelVersionsDiffer v1/v2 通道类型必须不同，否则协商无法区分
func TestDataPlaneChannelVersionsDiffer(t *testing.T) {
	if DataPlaneChannelType == DataPlaneChannelTypeV2 {
		t.Fatal("v1 and v2 channel types must differ")
	}
	if !strings.HasSuffix(DataPlaneChannelTypeV2, "/v2") {
		t.Fatalf("v2 channel type should end with /v2, got %q", DataPlaneChannelTypeV2)
	}
}

// TestDataPlaneIsolationEnabled 隔离灰度开关：仅显式 on/1/true 系列开启（默认关闭）
func TestDataPlaneIsolationEnabled(t *testing.T) {
	for _, v := range []string{"on", "ON", "1", "true", "yes", "enabled"} {
		t.Setenv("NSSH_DATA_PLANE_ISOLATION", v)
		if !dataPlaneIsolationEnabled() {
			t.Fatalf("expected isolation enabled for %q", v)
		}
	}
	for _, v := range []string{"", "off", "0", "false", "disabled"} {
		t.Setenv("NSSH_DATA_PLANE_ISOLATION", v)
		if dataPlaneIsolationEnabled() {
			t.Fatalf("expected isolation disabled for %q", v)
		}
	}
}

// TestEncodePlaneBindFrame 控制帧布局：type=3 + plane_id（与节点 readDataFrame 对齐）
func TestEncodePlaneBindFrame(t *testing.T) {
	buf := encodePlaneBindFrame(0x1122334455667788)
	if len(buf) != 9 || buf[0] != dataFrameTypeBindPlane {
		t.Fatalf("unexpected frame: %v", buf)
	}
	if got := binary.BigEndian.Uint64(buf[1:]); got != 0x1122334455667788 {
		t.Fatalf("unexpected plane id %x", got)
	}
}

// TestPlaneBindMagicLayout bulk 绑定前导：magic(4B) + plane_id(8B)
func TestPlaneBindMagicLayout(t *testing.T) {
	if len(planeBindMagic) != 4 {
		t.Fatalf("plane bind magic should be 4 bytes, got %d", len(planeBindMagic))
	}
	if bytes.Equal(planeBindMagic, dataStreamMagic) {
		t.Fatal("plane bind magic must differ from data stream magic")
	}
}

// TestDataConnectionFallback 未绑定 bulk 时数据流仍走 SSH 所在 QUIC 连接
func TestDataConnectionFallback(t *testing.T) {
	dp := newTunnelDataPlane(nil, nil, &ChannelHandler{})
	if dp.dataConnection() != nil {
		t.Fatal("dataConnection should return qconn (nil here) when no bulk connection")
	}
}
