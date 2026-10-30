package daemon

import (
	"testing"
	"time"
)

// TestBindRejectBackoff P2 端口声明被拒的退避序列：
// 3s → 5s → 10s → 30s 回归常态，附 0~1s jitter（含下界不含上界）
func TestBindRejectBackoff(t *testing.T) {
	cases := []struct {
		streak int
		base   time.Duration
	}{
		{1, 3 * time.Second},
		{2, 5 * time.Second},
		{3, 10 * time.Second},
		{4, 30 * time.Second},
		{100, 30 * time.Second},
	}
	for _, c := range cases {
		d := bindRejectBackoff(c.streak)
		if d < c.base || d >= c.base+time.Second {
			t.Errorf("bindRejectBackoff(%d) = %v, want [%v, %v)", c.streak, d, c.base, c.base+time.Second)
		}
	}
}
