package base_core

import (
	"bytes"
	"testing"
)

// === 基础日志输出测试 ===

func TestLogger_Info(t *testing.T) {
	var buf bytes.Buffer
	l := NewCustomLogger(&buf)

	l.Info("test message %s", "hello")

	output := buf.String()
	if !containsString(output, "test message hello") {
		t.Errorf("output = %q, want contains 'test message hello'", output)
	}
}

func TestLogger_Debug_RespectsLevel(t *testing.T) {
	var buf bytes.Buffer
	l := NewCustomLogger(&buf)
	l.SetLevel(INFO)

	l.Debug("should not appear")

	output := buf.String()
	if containsString(output, "should not appear") {
		t.Errorf("DEBUG output appeared when level is INFO: %q", output)
	}
}

func TestLogger_Warn(t *testing.T) {
	var buf bytes.Buffer
	l := NewCustomLogger(&buf)
	l.SetLevel(DEBUG)

	l.Warn("warning %d", 42)

	output := buf.String()
	if !containsString(output, "warning 42") {
		t.Errorf("output = %q, want contains 'warning 42'", output)
	}
}

func TestLogger_Error(t *testing.T) {
	var buf bytes.Buffer
	l := NewCustomLogger(&buf)

	l.Error("error occurred")

	output := buf.String()
	if !containsString(output, "error occurred") {
		t.Errorf("output = %q, want contains 'error occurred'", output)
	}
}

// === Level 过滤测试 ===

func TestLogger_SetLevel(t *testing.T) {
	var buf bytes.Buffer
	l := NewCustomLogger(&buf)

	l.SetLevel(WARN)

	l.Debug("debug msg")
	l.Info("info msg")
	l.Warn("warn msg")

	output := buf.String()
	if containsString(output, "debug msg") {
		t.Error("DEBUG message should not appear at WARN level")
	}
	if containsString(output, "info msg") {
		t.Error("INFO message should not appear at WARN level")
	}
	if !containsString(output, "warn msg") {
		t.Error("WARN message should appear at WARN level")
	}
}

// === SetLogContext 测试 ===

func TestSetLogContext(t *testing.T) {
	var buf bytes.Buffer
	l := NewCustomLogger(&buf)
	l.SetLevel(DEBUG)

	SetLogContext("user1", "192.168.1.1", 22)
	l.Info("context test")

	output := buf.String()
	if !containsString(output, "user1:192.168.1.1:22") {
		t.Errorf("output = %q, want contains 'user1:192.168.1.1:22'", output)
	}
}

// === EnableLoki 测试 ===

func TestLogger_EnableLoki(t *testing.T) {
	var buf bytes.Buffer
	l := NewCustomLogger(&buf)

	l.EnableLoki("http://localhost:3100/loki/api/v1/push", "test_job")

	if l.lokiURL != "http://localhost:3100/loki/api/v1/push" {
		t.Errorf("lokiURL = %q, want http://localhost:3100/loki/api/v1/push", l.lokiURL)
	}
	if l.lokiJob != "test_job" {
		t.Errorf("lokiJob = %q, want test_job", l.lokiJob)
	}
	if !l.enableLoki {
		t.Error("enableLoki = false, want true")
	}
}

func TestLogger_EnableLoki_EmptyJob(t *testing.T) {
	var buf bytes.Buffer
	l := NewCustomLogger(&buf)

	l.EnableLoki("http://localhost:3100", "")

	// 空 job 应该保留默认值
	if l.lokiJob != "nssh" {
		t.Errorf("lokiJob = %q, want nssh (default)", l.lokiJob)
	}
}

// === WithFields 测试 ===

func TestLogger_WithFields(t *testing.T) {
	var buf bytes.Buffer
	l := NewCustomLogger(&buf)
	l.SetLevel(DEBUG)

	l.WithFields(map[string]interface{}{
		"user": "test",
	}).Info("fields test")

	output := buf.String()
	if !containsString(output, "fields test") {
		t.Errorf("output = %q, want contains 'fields test'", output)
	}
	if !containsString(output, "user=test") {
		t.Errorf("output = %q, want contains 'user=test'", output)
	}
}

// === SetProcessType 测试 ===

func TestLogger_SetProcessType(t *testing.T) {
	var buf bytes.Buffer
	l := NewCustomLogger(&buf)

	l.SetProcessType("daemon")
	if l.processType != "daemon" {
		t.Errorf("processType = %q, want daemon", l.processType)
	}
}

// === GetLogger 单例测试 ===

func TestGetLogger_Singleton(t *testing.T) {
	l1 := GetLogger()
	l2 := GetLogger()

	if l1 != l2 {
		t.Error("GetLogger returned different instances, want singleton")
	}
}

// === Helper ===

func containsString(s, substr string) bool {
	return len(s) >= len(substr) && (substr == "" || len(s) > 0 && findSubstring(s, substr))
}

func findSubstring(s, sub string) bool {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
