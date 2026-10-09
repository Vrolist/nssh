package main

import (
	"testing"

	"github.com/Vrolist/nssh/base_core"
)

// TestBuildStartParamsCarriesProtoAndTransport 关键回归：
// daemon 是多隧道共享进程，转发协议/传输模式必须逐隧道随 CMD_START 下发，
// 缺字段会导致 daemon 模式静默丢失 --proto/--transport（udp/both 退化为 tcp）。
func TestBuildStartParamsCarriesProtoAndTransport(t *testing.T) {
	config := &base_core.Config{
		Username:       "10001",
		ServerHost:     "gz1.example.com",
		ServerPort:     7022,
		Password:       "pw",
		SSHKeyPath:     "/root/.ssh/id_rsa",
		LocalHost:      "192.168.1.5",
		LocalPort:      80,
		RemotePort:     10001,
		ReconnectDelay: 30,
		MaxLifetime:    172800,
		Proto:          "udp",
		Transport:      "quic",
	}

	params := buildStartParams(config)

	want := map[string]string{
		"username":        "10001",
		"server_host":     "gz1.example.com",
		"server_port":     "7022",
		"local_host":      "192.168.1.5",
		"local_port":      "80",
		"remote_port":     "10001",
		"password":        "pw",
		"ssh_key":         "/root/.ssh/id_rsa",
		"reconnect_delay": "30",
		"max_lifetime":    "172800",
		"proto":           "udp",
		"transport":       "quic",
	}
	if len(params) != len(want) {
		t.Fatalf("params 字段数不符: got %d want %d (%v)", len(params), len(want), params)
	}
	for k, v := range want {
		if params[k] != v {
			t.Errorf("params[%q] = %q, want %q", k, params[k], v)
		}
	}
}

// TestBuildStartParamsExplicitBothProto CLI 默认 proto=both 时必须原样透传（不被改写为 tcp）
func TestBuildStartParamsExplicitBothProto(t *testing.T) {
	config := &base_core.Config{
		ServerHost: "gz1.example.com", ServerPort: 7022, LocalHost: "localhost", LocalPort: 80,
		RemotePort: 10001, Proto: "both", Transport: "auto",
	}
	params := buildStartParams(config)
	if params["proto"] != "both" {
		t.Errorf("proto = %q, want both", params["proto"])
	}
	if params["transport"] != "auto" {
		t.Errorf("transport = %q, want auto", params["transport"])
	}
}

// TestBuildStartParamsEmptyProtoPassedThrough 空串原样传递，由 daemon 侧归一为 tcp
// （CLI 与 daemon 职责分离：CLI 不做归一，避免两处规则漂移）
func TestBuildStartParamsEmptyProtoPassedThrough(t *testing.T) {
	config := &base_core.Config{
		ServerHost: "gz1.example.com", ServerPort: 7022, LocalHost: "localhost", LocalPort: 80, RemotePort: 10001,
	}
	params := buildStartParams(config)
	if v, ok := params["proto"]; !ok || v != "" {
		t.Errorf(`params["proto"] = %q (present=%v), want ""`, v, ok)
	}
	if v, ok := params["transport"]; !ok || v != "" {
		t.Errorf(`params["transport"] = %q (present=%v), want ""`, v, ok)
	}
}
