package base_core

import (
	"testing"
	"time"
)

func newTestStatsManager() *StatsManager {
	config := &Config{
		ServerHost: "test.example.com",
		ServerPort: 22,
		Username:   "testuser",
		LocalHost:  "localhost",
		LocalPort:  8080,
		RemotePort: 9090,
	}
	return NewStatsManager(config, false)
}

// === 状态转换测试 ===

func TestStatsManager_InitialState(t *testing.T) {
	sm := newTestStatsManager()

	if sm.GetStatus() != "offline" {
		t.Errorf("initial status = %q, want offline", sm.GetStatus())
	}
}

func TestStatsManager_RecordConnection_GoesOnline(t *testing.T) {
	sm := newTestStatsManager()

	sm.RecordConnection()

	if sm.GetStatus() != "online" {
		t.Errorf("status after RecordConnection = %q, want online", sm.GetStatus())
	}
}

func TestStatsManager_RecordFailure_GoesOffline(t *testing.T) {
	sm := newTestStatsManager()

	sm.RecordConnection()
	sm.RecordFailure()

	if sm.GetStatus() != "offline" {
		t.Errorf("status after RecordFailure = %q, want offline", sm.GetStatus())
	}
}

func TestStatsManager_RecordDisconnection_GoesOffline(t *testing.T) {
	sm := newTestStatsManager()

	sm.RecordConnection()
	sm.RecordDisconnection()

	if sm.GetStatus() != "offline" {
		t.Errorf("status after RecordDisconnection = %q, want offline", sm.GetStatus())
	}
}

func TestStatsManager_DoubleRecordFailure_StaysOffline(t *testing.T) {
	sm := newTestStatsManager()

	sm.RecordFailure()
	sm.RecordFailure()

	if sm.GetStatus() != "offline" {
		t.Errorf("status = %q, want offline after double failure", sm.GetStatus())
	}
}

func TestStatsManager_ConnectionCycle(t *testing.T) {
	sm := newTestStatsManager()

	// offline -> online -> offline -> online
	sm.RecordConnection()
	if sm.GetStatus() != "online" {
		t.Fatalf("expected online, got %s", sm.GetStatus())
	}

	sm.RecordFailure()
	if sm.GetStatus() != "offline" {
		t.Fatalf("expected offline, got %s", sm.GetStatus())
	}

	sm.RecordConnection()
	if sm.GetStatus() != "online" {
		t.Fatalf("expected online again, got %s", sm.GetStatus())
	}
}

// === 字节传输测试 ===

func TestStatsManager_AddBytesTransferred(t *testing.T) {
	sm := newTestStatsManager()

	sm.AddBytesTransferred(1024)
	sm.AddBytesTransferred(2048)

	if sm.stats.BytesTransferred != 3072 {
		t.Errorf("BytesTransferred = %d, want 3072", sm.stats.BytesTransferred)
	}
}

func TestStatsManager_AddBytesTransferred_Zero(t *testing.T) {
	sm := newTestStatsManager()

	sm.AddBytesTransferred(0)

	if sm.stats.BytesTransferred != 0 {
		t.Errorf("BytesTransferred = %d, want 0", sm.stats.BytesTransferred)
	}
}

// === Stats 字段初始化测试 ===

func TestStatsManager_InitFields(t *testing.T) {
	sm := newTestStatsManager()

	if sm.stats.Username != "testuser" {
		t.Errorf("Username = %q, want testuser", sm.stats.Username)
	}
	if sm.stats.ServerHost != "test.example.com" {
		t.Errorf("ServerHost = %q, want test.example.com", sm.stats.ServerHost)
	}
	if sm.stats.ServerPort != 22 {
		t.Errorf("ServerPort = %d, want 22", sm.stats.ServerPort)
	}
	if sm.stats.LocalHost != "localhost" {
		t.Errorf("LocalHost = %q, want localhost", sm.stats.LocalHost)
	}
	if sm.stats.LocalPort != 8080 {
		t.Errorf("LocalPort = %d, want 8080", sm.stats.LocalPort)
	}
	if sm.stats.StartTime.IsZero() {
		t.Error("StartTime is zero, want non-zero")
	}
}

// === SetLokiURL 测试 ===

func TestStatsManager_SetLokiURL(t *testing.T) {
	sm := newTestStatsManager()

	sm.SetLokiURL("http://localhost:3100/loki/api/v1/push")
	if sm.lokiURL != "http://localhost:3100/loki/api/v1/push" {
		t.Errorf("lokiURL = %q, want http://localhost:3100/loki/api/v1/push", sm.lokiURL)
	}

	// 空 URL
	sm.SetLokiURL("")
	if sm.lokiURL != "" {
		t.Errorf("lokiURL = %q, want empty", sm.lokiURL)
	}
}

// === MemoryHook 测试 ===

func TestStatsManager_SetMemoryHook(t *testing.T) {
	sm := newTestStatsManager()

	called := false
	sm.SetMemoryHook(func() {
		called = true
	})

	if sm.memoryHook == nil {
		t.Fatal("memoryHook is nil, expected non-nil")
	}

	sm.memoryHook()
	if !called {
		t.Error("memoryHook was not called")
	}
}

// === GetStartTime / GetLastStatusChangeTime 测试 ===

func TestStatsManager_TimingMethods(t *testing.T) {
	sm := newTestStatsManager()

	start := sm.GetStartTime()
	if time.Since(start) > time.Second {
		t.Errorf("GetStartTime() returned time too far in the past: %v", start)
	}

	lastChange := sm.GetLastStatusChangeTime()
	if time.Since(lastChange) > time.Second {
		t.Errorf("GetLastStatusChangeTime() returned time too far in the past: %v", lastChange)
	}
}

// === Stop 不 panic 测试 ===

func TestStatsManager_Stop_WithoutStart(t *testing.T) {
	sm := newTestStatsManager()
	sm.Stop() // 不应该 panic
}

func TestStatsManager_Stop_AfterStart(t *testing.T) {
	sm := newTestStatsManager()
	sm.StartPeriodicReport(time.Second)
	time.Sleep(10 * time.Millisecond)
	sm.Stop()
}
