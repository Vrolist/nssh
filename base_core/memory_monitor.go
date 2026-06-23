// +build memory

package base_core

import (
	"encoding/json"
	"runtime"
	"time"
)

type MemoryMonitor struct {
	logger *CustomLogger
}

func NewMemoryMonitor() *MemoryMonitor {
	return &MemoryMonitor{
		logger: GetLogger(),
	}
}

func (m *MemoryMonitor) PrintMemoryStats() {
	var memStats runtime.MemStats
	runtime.ReadMemStats(&memStats)

	stats := map[string]interface{}{
		"heap_alloc":      memStats.HeapAlloc,
		"heap_sys":        memStats.HeapSys,
		"heap_objects":    memStats.HeapObjects,
		"heap_idle":       memStats.HeapIdle,
		"heap_inuse":      memStats.HeapInuse,
		"stack_inuse":     memStats.StackInuse,
		"stack_sys":       memStats.StackSys,
		"goroutines":     runtime.NumGoroutine(),
		"gc_sys":          memStats.GCSys,
		"next_gc":         memStats.NextGC,
		"last_gc":         time.Unix(0, int64(memStats.LastGC)).Format("2006-01-02 15:04:05"),
		"num_gc":          memStats.NumGC,
		"pause_total_ns":   memStats.PauseTotalNs,
		"timestamp":      time.Now().Format("2006-01-02 15:04:05"),
	}

	_, err := json.Marshal(stats)
	if err != nil {
		m.logger.Error("Failed to marshal memory stats: %v", err)
		return
	}
}
