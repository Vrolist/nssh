package base_tunnel

import (
	"bytes"
	"errors"
	"testing"
)

func testUDPPingNonce() []byte {
	n := make([]byte, udpPingNonceLen)
	for i := range n {
		n[i] = byte(i + 1)
	}
	return n
}

func buildTestPingReply(nonce []byte, version uint32, flags byte) []byte {
	reply := make([]byte, 0, udpPingReplyLen)
	reply = append(reply, nonce...)
	reply = append(reply, byte(version>>24), byte(version>>16), byte(version>>8), byte(version))
	reply = append(reply, flags)
	return reply
}

// TestParseUDPPingReply 正常回包：udp/both 且共享入口健康 → 通过
func TestParseUDPPingReply(t *testing.T) {
	nonce := testUDPPingNonce()
	for _, proto := range []string{"udp", "both", "UDP", "Both"} {
		if err := parseUDPPingReply(nonce, buildTestPingReply(nonce, 123, udpFlagSharedEntryHealthy), proto); err != nil {
			t.Errorf("proto=%s should pass, got: %v", proto, err)
		}
	}
	// tcp 隧道不要求 bit0：共享入口不健康也通过
	if err := parseUDPPingReply(nonce, buildTestPingReply(nonce, 123, 0), "tcp"); err != nil {
		t.Errorf("tcp proto should not require shared entry healthy, got: %v", err)
	}
	// tcp 隧道：配置变化位仍然生效
	if err := parseUDPPingReply(nonce, buildTestPingReply(nonce, 123, udpFlagConfigChanged), "tcp"); err == nil {
		t.Error("tcp proto with config changed should fail")
	}
}

// TestParseUDPPingReply_ConfigChanged U2：配置变化位触发重连
func TestParseUDPPingReply_ConfigChanged(t *testing.T) {
	nonce := testUDPPingNonce()
	err := parseUDPPingReply(nonce, buildTestPingReply(nonce, 999, udpFlagSharedEntryHealthy|udpFlagConfigChanged), "both")
	if err == nil || !bytes.Contains([]byte(err.Error()), []byte("config changed")) {
		t.Fatalf("want config changed error, got: %v", err)
	}
}

// TestParseUDPPingReply_SharedEntryUnhealthy U1：udp/both 隧道共享入口不健康 → 失败（计入连击）
func TestParseUDPPingReply_SharedEntryUnhealthy(t *testing.T) {
	nonce := testUDPPingNonce()
	err := parseUDPPingReply(nonce, buildTestPingReply(nonce, 123, 0), "udp")
	if err == nil || !bytes.Contains([]byte(err.Error()), []byte("unhealthy")) {
		t.Fatalf("want unhealthy error, got: %v", err)
	}
}

// TestParseUDPPingReply_ConfigUnknown 数据端故障（bit2）→ 不动作，不误报
func TestParseUDPPingReply_ConfigUnknown(t *testing.T) {
	nonce := testUDPPingNonce()
	// 配置未知 + 共享入口不健康：udp 隧道仍因健康位失败（数据面自检独立于配置查询）
	if err := parseUDPPingReply(nonce, buildTestPingReply(nonce, 0, udpFlagConfigUnknown), "udp"); err == nil {
		t.Error("udp tunnel with unhealthy shared entry should fail even when config unknown")
	}
	// 配置未知 + 全部健康：通过
	if err := parseUDPPingReply(nonce, buildTestPingReply(nonce, 0, udpFlagConfigUnknown|udpFlagSharedEntryHealthy), "both"); err != nil {
		t.Errorf("healthy + config unknown should pass, got: %v", err)
	}
}

// TestParseUDPPingReply_Malformed 异常回包
func TestParseUDPPingReply_Malformed(t *testing.T) {
	nonce := testUDPPingNonce()
	if err := parseUDPPingReply(nonce, []byte("short"), "udp"); err == nil {
		t.Error("short reply should fail")
	}
	bad := buildTestPingReply(nonce, 1, 0)
	bad[0] ^= 0xFF // nonce 被篡改
	if err := parseUDPPingReply(nonce, bad, "udp"); err == nil {
		t.Error("nonce mismatch should fail")
	}
}

// TestErrUDPPingUnsupported 哨兵错误语义
func TestErrUDPPingUnsupported(t *testing.T) {
	if !errors.Is(errUDPPingUnsupported, errUDPPingUnsupported) {
		t.Fatal("sanity")
	}
}
