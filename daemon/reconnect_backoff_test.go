package daemon

import (
	"testing"
	"time"
)

// TestReconnectBackoff P4-C 普通断线指数退避：
// base → 2× → 4× …（上限 5min），附 0~min(base/2, 30s) 抖动。
func TestReconnectBackoff(t *testing.T) {
	base := 30 * time.Second
	cases := []struct {
		streak   int
		minDelay time.Duration
		maxDelay time.Duration
	}{
		{1, base, base + 15*time.Second},
		{2, 2 * base, 2*base + 30*time.Second},
		{3, 4 * base, 4*base + 30*time.Second},
		{10, 5 * time.Minute, 5*time.Minute + 30*time.Second},
	}
	for _, c := range cases {
		got := reconnectBackoff(c.streak, base)
		if got < c.minDelay || got > c.maxDelay {
			t.Errorf("reconnectBackoff(%d) = %v, want [%v, %v]", c.streak, got, c.minDelay, c.maxDelay)
		}
	}
}

// 非法 base 兜底 30s，不能返回 0（0 会导致断线后立即重连风暴）
func TestReconnectBackoffZeroBase(t *testing.T) {
	got := reconnectBackoff(1, 0)
	if got < 30*time.Second || got > 45*time.Second {
		t.Fatalf("reconnectBackoff(1, 0) = %v, want [30s, 45s]", got)
	}
}

// 连续失败必须递增（指数退避生效）；抖动不改变单调趋势的下界
func TestReconnectBackoffGrows(t *testing.T) {
	base := 10 * time.Second
	if reconnectBackoff(1, base) >= reconnectBackoff(4, base) {
		t.Fatal("backoff should grow with consecutive failures")
	}
}
