package daemon

import (
	"testing"
	"time"

	"github.com/Vrolist/nssh/base_core"
)

func newTestWorker(maxLifetime int, status string) *WorkerInfo {
	return &WorkerInfo{
		ID:          "test@host:22",
		Username:    "test",
		ServerHost:  "host",
		ServerPort:  22,
		LocalHost:   "localhost",
		LocalPort:   8080,
		RemotePort:  9090,
		StartTime:   time.Now(),
		Status:      status,
		MaxLifetime: maxLifetime,
		Config: &base_core.Config{
			Username:   "test",
			ServerHost: "host",
			ServerPort: 22,
		},
	}
}

// === CheckWorkerLifetime 测试 ===

func TestCheckWorkerLifetime_Disabled(t *testing.T) {
	w := newTestWorker(0, "online")
	w.StartTime = time.Now().Add(-49 * time.Hour) // exceeded, but disabled

	restart, _ := CheckWorkerLifetime(w, "key")
	if restart {
		t.Error("MaxLifetime=0 should never restart")
	}
}

func TestCheckWorkerLifetime_Offline(t *testing.T) {
	w := newTestWorker(86400, "offline") // exceeded, but offline
	w.StartTime = time.Now().Add(-49 * time.Hour)

	restart, _ := CheckWorkerLifetime(w, "key")
	if restart {
		t.Error("offline worker should not restart")
	}
}

func TestCheckWorkerLifetime_NotExpired(t *testing.T) {
	w := newTestWorker(86400, "online") // 24h lifetime
	w.StartTime = time.Now().Add(-12 * time.Hour) // only 12h passed

	restart, _ := CheckWorkerLifetime(w, "key")
	if restart {
		t.Error("lifetime not exceeded yet, should not restart")
	}
}

func TestCheckWorkerLifetime_FirstCheckSetsLastCheckTime(t *testing.T) {
	w := newTestWorker(86400, "online")
	w.StartTime = time.Now().Add(-49 * time.Hour) // exceeded

	restart, _ := CheckWorkerLifetime(w, "key")
	if restart {
		t.Error("first check should not restart, should set LastCheckTime")
	}
	if w.LastCheckTime.IsZero() {
		t.Error("LastCheckTime should be set after first check")
	}
	if w.LastBytesCheck != 0 {
		t.Errorf("LastBytesCheck = %d, want 0", w.LastBytesCheck)
	}
}

func TestCheckWorkerLifetime_IdleButNotLongEnough(t *testing.T) {
	w := newTestWorker(86400, "online")
	w.StartTime = time.Now().Add(-49 * time.Hour)
	w.LastCheckTime = time.Now().Add(-10 * time.Second) // checked 10s ago
	w.LastBytesCheck = 0

	restart, _ := CheckWorkerLifetime(w, "key")
	if restart {
		t.Error("idle time < 30s, should not restart yet")
	}
}

func TestCheckWorkerLifetime_IdleLongEnough_Restart(t *testing.T) {
	w := newTestWorker(86400, "online")
	w.StartTime = time.Now().Add(-49 * time.Hour)
	w.LastCheckTime = time.Now().Add(-31 * time.Second) // idle > 30s
	w.LastBytesCheck = 0                                  // no data transfer

	restart, key := CheckWorkerLifetime(w, "key")
	if !restart {
		t.Error("exceeded lifetime, idle > 30s, should restart")
	}
	if key != "key" {
		t.Errorf("key = %q, want key", key)
	}
}

func TestCheckWorkerLifetime_Busy_ResetsCheck(t *testing.T) {
	w := newTestWorker(86400, "online")
	w.StartTime = time.Now().Add(-49 * time.Hour)
	w.LastCheckTime = time.Now().Add(-31 * time.Second)
	w.LastBytesCheck = 100 // was 100 bytes before

	// StatsManager reports 200 bytes now (data flowing)
	sm := base_core.NewStatsManager(&base_core.Config{
		Username: "test", ServerHost: "host", ServerPort: 22,
		LocalHost: "localhost", LocalPort: 80, RemotePort: 8000,
	}, false)
	sm.AddBytesTransferred(200)
	w.StatsManager = sm

	restart, _ := CheckWorkerLifetime(w, "key")
	if restart {
		t.Error("data is flowing, should not restart")
	}
	// LastCheckTime should be updated
	if time.Since(w.LastCheckTime) > time.Second {
		t.Error("LastCheckTime should be updated when data is flowing")
	}
}

func TestCheckWorkerLifetime_NoConfig(t *testing.T) {
	w := newTestWorker(86400, "online")
	w.Config = nil
	w.StartTime = time.Now().Add(-49 * time.Hour)

	restart, _ := CheckWorkerLifetime(w, "key")
	if restart {
		t.Error("nil Config should not restart")
	}
}

func TestCheckWorkerLifetime_IdleZeroBytes(t *testing.T) {
	w := newTestWorker(86400, "online")
	w.StartTime = time.Now().Add(-49 * time.Hour)
	// First call: sets LastCheckTime
	CheckWorkerLifetime(w, "key")
	// Simulate time passing but bytes unchanged
	w.LastBytesCheck = 0
	w.LastCheckTime = time.Now().Add(-60 * time.Second) // 60s ago

	// StatsManager still reports 0
	sm := base_core.NewStatsManager(&base_core.Config{
		Username: "test", ServerHost: "host", ServerPort: 22,
		LocalHost: "localhost", LocalPort: 80, RemotePort: 8000,
	}, false)
	w.StatsManager = sm

	restart, _ := CheckWorkerLifetime(w, "key")
	if !restart {
		t.Error("0 bytes both times, idle > 30s, should restart")
	}
}

// === 48h 完整生命周期测试 ===

func TestLifetime_48h_Idle_Restart(t *testing.T) {
	// 模拟：连接建立 49 小时，空闲，经过 3 个 tick 后触发重启
	w := newTestWorker(172800, "online") // 48h lifetime
	w.StartTime = time.Now().Add(-49 * time.Hour)
	sm := base_core.NewStatsManager(&base_core.Config{
		Username: "test", ServerHost: "host", ServerPort: 22,
		LocalHost: "localhost", LocalPort: 80, RemotePort: 8000,
	}, false)
	w.StatsManager = sm

	// Tick 1: 首次检查，设置 LastCheckTime，不重启
	restart, _ := CheckWorkerLifetime(w, "worker@host:22")
	if restart {
		t.Fatal("tick 1: should not restart (first check)")
	}
	if w.LastCheckTime.IsZero() {
		t.Fatal("tick 1: LastCheckTime should be set")
	}

	// Tick 2: 空闲但不足 30s，不重启
	w.LastCheckTime = time.Now().Add(-10 * time.Second)
	restart, _ = CheckWorkerLifetime(w, "worker@host:22")
	if restart {
		t.Fatal("tick 2: should not restart (idle < 30s)")
	}

	// Tick 3: 空闲超过 30s，触发重启
	w.LastCheckTime = time.Now().Add(-31 * time.Second)
	restart, key := CheckWorkerLifetime(w, "worker@host:22")
	if !restart {
		t.Fatal("tick 3: should restart (48h exceeded, idle > 30s)")
	}
	if key != "worker@host:22" {
		t.Errorf("tick 3: key = %q, want worker@host:22", key)
	}
}

func TestLifetime_48h_Busy_NoRestart(t *testing.T) {
	// 模拟：连接建立 49 小时，有数据流动，不重启
	w := newTestWorker(172800, "online")
	w.StartTime = time.Now().Add(-49 * time.Hour)
	sm := base_core.NewStatsManager(&base_core.Config{
		Username: "test", ServerHost: "host", ServerPort: 22,
		LocalHost: "localhost", LocalPort: 80, RemotePort: 8000,
	}, false)
	w.StatsManager = sm

	// Tick 1: 首次检查
	CheckWorkerLifetime(w, "worker@host:22")

	// 模拟数据流动：字节数变化
	sm.AddBytesTransferred(1024)
	w.LastCheckTime = time.Now().Add(-31 * time.Second)

	// Tick 2: 有数据流动，不重启
	restart, _ := CheckWorkerLifetime(w, "worker@host:22")
	if restart {
		t.Fatal("should not restart (data is flowing)")
	}
	if time.Since(w.LastCheckTime) > time.Second {
		t.Fatal("LastCheckTime should be updated when data is flowing")
	}
}

func TestLifetime_48h_NotExpired_NoRestart(t *testing.T) {
	// 模拟：连接建立 20 小时，空闲，不重启
	w := newTestWorker(172800, "online")
	w.StartTime = time.Now().Add(-20 * time.Hour) // 仅 20h，未超 48h

	restart, _ := CheckWorkerLifetime(w, "worker@host:22")
	if restart {
		t.Fatal("should not restart (lifetime not exceeded)")
	}
}

func TestLifetime_48h_Offline_NoRestart(t *testing.T) {
	// 模拟：连接建立 49 小时，但状态为 offline，不重启
	w := newTestWorker(172800, "offline")
	w.StartTime = time.Now().Add(-49 * time.Hour)

	restart, _ := CheckWorkerLifetime(w, "worker@host:22")
	if restart {
		t.Fatal("should not restart (offline)")
	}
}

func TestLifetime_48h_Disabled_NoRestart(t *testing.T) {
	// 模拟：MaxLifetime=0（禁用），49 小时不重启
	w := newTestWorker(0, "online")
	w.StartTime = time.Now().Add(-49 * time.Hour)

	restart, _ := CheckWorkerLifetime(w, "worker@host:22")
	if restart {
		t.Fatal("should not restart (max lifetime disabled)")
	}
}

func TestLifetime_48h_BusyThenIdle_Restart(t *testing.T) {
	// 模拟：连接建立 49 小时，先有数据流动，然后空闲，最终重启
	w := newTestWorker(172800, "online")
	w.StartTime = time.Now().Add(-49 * time.Hour)
	sm := base_core.NewStatsManager(&base_core.Config{
		Username: "test", ServerHost: "host", ServerPort: 22,
		LocalHost: "localhost", LocalPort: 80, RemotePort: 8000,
	}, false)
	w.StatsManager = sm

	// Tick 1: 首次检查
	CheckWorkerLifetime(w, "worker@host:22")

	// Tick 2: 有数据流动（500 bytes）
	sm.AddBytesTransferred(500)
	w.LastCheckTime = time.Now().Add(-31 * time.Second)
	restart, _ := CheckWorkerLifetime(w, "worker@host:22")
	if restart {
		t.Fatal("tick 2: should not restart (data flowing)")
	}

	// Tick 3: 字节不再增长（空闲）
	// StatsManager 保持 500 bytes，但 w.LastBytesCheck 被设为 500
	w.LastCheckTime = time.Now().Add(-31 * time.Second)
	w.LastBytesCheck = 500 // 上次检查时记录的字节
	// sm.GetBytesTransferred() 仍为 500，字节没变 → 空闲
	restart, _ = CheckWorkerLifetime(w, "worker@host:22")
	if !restart {
		t.Fatal("tick 3: should restart (data stopped, idle > 30s)")
	}
}

func TestCheckWorkerLifetime_BytesChangedBetweenChecks(t *testing.T) {
	w := newTestWorker(86400, "online")
	w.StartTime = time.Now().Add(-49 * time.Hour)

	// First call: no StatsManager, currentBytes=0
	w.LastBytesCheck = 0
	w.LastCheckTime = time.Now().Add(-60 * time.Second)

	// Now add bytes to simulate data flowing
	sm := base_core.NewStatsManager(&base_core.Config{
		Username: "test", ServerHost: "host", ServerPort: 22,
		LocalHost: "localhost", LocalPort: 80, RemotePort: 8000,
	}, false)
	sm.AddBytesTransferred(500)
	w.StatsManager = sm

	restart, _ := CheckWorkerLifetime(w, "key")
	if restart {
		t.Error("bytes changed, should not restart")
	}
}
