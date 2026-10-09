package daemon

import "testing"

// TestConfigFromStartParamsProtoTransport 关键回归：CLI 随 CMD_START 下发的 proto/transport
// 必须被还原到 Config，否则 daemon 模式 udp/both 会静默退化为 tcp（--proto 丢失）。
func TestConfigFromStartParamsProtoTransport(t *testing.T) {
	base := map[string]string{
		"username":    "10001",
		"server_host": "gz1.example.com",
		"server_port": "7022",
		"local_host":  "192.168.1.5",
		"local_port":  "80",
		"remote_port": "10001",
		"password":    "pw",
		"ssh_key":     "/root/.ssh/id_rsa",
	}

	cases := []struct {
		name          string
		proto         string
		transport     string
		wantProto     string
		wantTransport string
	}{
		{"udp 原样", "udp", "quic", "udp", "quic"},
		{"both 原样", "both", "tcp", "both", "tcp"},
		{"tcp 原样", "tcp", "auto", "tcp", "auto"},
		{"proto 缺省回退 tcp", "", "auto", "tcp", "auto"},
		{"transport 缺省回退 auto", "udp", "", "udp", "auto"},
		{"两者缺省（老 CLI）", "", "", "tcp", "auto"},
		{"未知值回退", "uudp", "quik", "tcp", "auto"},
		{"大小写与空白归一", "  UDP  ", " QUIC ", "udp", "quic"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			params := map[string]string{}
			for k, v := range base {
				params[k] = v
			}
			params["proto"] = c.proto
			params["transport"] = c.transport

			cfg := configFromStartParams(params)

			if cfg.Proto != c.wantProto {
				t.Errorf("Proto = %q, want %q", cfg.Proto, c.wantProto)
			}
			if cfg.Transport != c.wantTransport {
				t.Errorf("Transport = %q, want %q", cfg.Transport, c.wantTransport)
			}
		})
	}
}

// TestConfigFromStartParamsBasicFields 基础字段与 reconnect 默认值还原
func TestConfigFromStartParamsBasicFields(t *testing.T) {
	cfg := configFromStartParams(map[string]string{
		"username":     "10001",
		"server_host":  "gz1.example.com",
		"server_port":  "7022",
		"local_host":   "192.168.1.5",
		"local_port":   "80",
		"remote_port":  "10001",
		"password":     "pw",
		"ssh_key":      "/root/.ssh/id_rsa",
		"max_lifetime": "172800",
		// reconnect_delay 缺省
	})

	if cfg.Username != "10001" || cfg.ServerHost != "gz1.example.com" || cfg.ServerPort != 7022 {
		t.Errorf("服务端字段还原错误: %+v", cfg)
	}
	if cfg.LocalHost != "192.168.1.5" || cfg.LocalPort != 80 || cfg.RemotePort != 10001 {
		t.Errorf("端口字段还原错误: %+v", cfg)
	}
	if cfg.Password != "pw" || cfg.SSHKeyPath != "/root/.ssh/id_rsa" {
		t.Errorf("凭证字段还原错误: %+v", cfg)
	}
	if cfg.MaxLifetime != 172800 {
		t.Errorf("MaxLifetime = %d, want 172800", cfg.MaxLifetime)
	}
	if cfg.ReconnectDelay != 30 {
		t.Errorf("ReconnectDelay 缺省应为 30, got %d", cfg.ReconnectDelay)
	}

	cfg2 := configFromStartParams(map[string]string{"reconnect_delay": "60"})
	if cfg2.ReconnectDelay != 60 {
		t.Errorf("ReconnectDelay 显式值应为 60, got %d", cfg2.ReconnectDelay)
	}
}
