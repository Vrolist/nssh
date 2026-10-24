package daemon

import (
	"testing"
	"time"
)

// TestWorkerHeartbeatTickDoesNotMarkOnline 心跳 tick 只续期存活时间，
// 不得把 worker 置为 online（否则转发声明失败/半开连接时会假在线）
func TestWorkerHeartbeatTickDoesNotMarkOnline(t *testing.T) {
	w := &WorkerInfo{Status: "offline"}
	before := time.Now()

	workerHeartbeatTick(w)

	if w.Status != "offline" {
		t.Fatalf("heartbeat tick must not change status, got %q", w.Status)
	}
	if w.LastRegisterTime.Before(before) {
		t.Fatalf("LastRegisterTime not refreshed: %v", w.LastRegisterTime)
	}
}

// TestMarkWorkerEstablishedWorkerOnline 隧道建立成功后置 online 并记录状态变更时间
func TestMarkWorkerEstablishedWorkerOnline(t *testing.T) {
	w := &WorkerInfo{Status: "offline"}

	markWorkerEstablished(w)

	if w.Status != "online" {
		t.Fatalf("expected online, got %q", w.Status)
	}
	if w.StatusChangeTime.IsZero() {
		t.Fatal("StatusChangeTime must be set")
	}
}
