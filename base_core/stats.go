package base_core

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"runtime"
	"sync"
	"time"
)

type Stats struct {
	Username             string                 `json:"username"`
	ServerHost           string                 `json:"server_host"`
	ServerPort           int                    `json:"server_port"`
	LocalHost            string                 `json:"local_host"`
	LocalPort            int                    `json:"local_port"`
	StartTime            time.Time              `json:"start_time"`
	Uptime               string                 `json:"uptime"`
	ConnectionCount      int64                  `json:"connection_count"`
	FailureCount         int64                  `json:"failure_count"`
	LastConnectTime      time.Time              `json:"last_connect_time"`
	StatusDuration       string                 `json:"status_duration"`
	BytesTransferred     int64                  `json:"bytes_transferred"`
	ConnectionStatus     string                 `json:"connection_status"`
	LastStatusChangeTime time.Time              `json:"last_status_change_time"`
	Memory               map[string]interface{} `json:"memory,omitempty"`
}

// 【worker】Stats 结构体内存占用分析：
// - string (Username): ~16-64 bytes
// - string (ServerHost): ~16-128 bytes
// - int (ServerPort): 8 bytes
// - string (LocalHost): ~16-32 bytes
// - int (LocalPort): 8 bytes
// - time.Time (StartTime): 24 bytes
// - string (Uptime): ~16-32 bytes (动态生成)
// - int64 (ConnectionCount): 8 bytes
// - int64 (FailureCount): 8 bytes
// - time.Time (LastConnectTime): 24 bytes
// - string (StatusDuration): ~16-32 bytes (动态生成)
// - int64 (BytesTransferred): 8 bytes
// - string (ConnectionStatus): ~16-32 bytes
// - time.Time (LastStatusChangeTime): 24 bytes
// - map[string]interface{} (Memory): 约200-500 bytes (包含多个内存指标)
// 总计: 约 400-900 bytes/实例
// 生命周期: worker进程启动时创建，进程结束时释放

type StatsManager struct {
	stats        Stats
	mu           sync.RWMutex
	logger       *CustomLogger
	ticker       *time.Ticker
	stopChan     chan struct{}
	memoryHook   func()
	lokiURL      string
	enableDaemon bool
}

// 【worker】StatsManager 结构体内存占用分析：
// - Stats: 约400-900 bytes
// - sync.RWMutex: 24 bytes
// - *CustomLogger: 指针8B，共享实例约1-2KB
// - *time.Ticker: 约100-200 bytes
// - chan struct{}: 约96 bytes (无缓冲channel)
// - func(): 函数指针 8 bytes
// - string (lokiURL): ~64 bytes
// - bool: 1 byte
// 总计: 约 700B-1.3KB/实例
// 生命周期: worker进程启动时创建，进程结束时释放

func NewStatsManager(config *Config, enableDaemon bool) *StatsManager {
	sm := &StatsManager{
		stats: Stats{
			Username:             config.Username,
			ServerHost:           config.ServerHost,
			ServerPort:           config.ServerPort,
			LocalHost:            config.LocalHost,
			LocalPort:            config.LocalPort,
			StartTime:            time.Now(),
			ConnectionCount:      0,
			FailureCount:         0,
			BytesTransferred:     0,
			ConnectionStatus:     "offline",
			LastStatusChangeTime: time.Now(),
		},
		logger:      GetLogger(),
		enableDaemon: enableDaemon,
	}

	return sm
}

func (sm *StatsManager) RecordConnection() {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	sm.stats.ConnectionCount++
	sm.stats.LastConnectTime = time.Now()
	if sm.stats.ConnectionStatus != "online" {
		sm.stats.ConnectionStatus = "online"
		sm.stats.LastStatusChangeTime = time.Now()
	}
}

func (sm *StatsManager) RecordFailure() {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	sm.stats.FailureCount++
	if sm.stats.ConnectionStatus != "offline" {
		sm.stats.ConnectionStatus = "offline"
		sm.stats.LastStatusChangeTime = time.Now()
	}
}

func (sm *StatsManager) RecordDisconnection() {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	if sm.stats.ConnectionStatus != "offline" {
		sm.stats.ConnectionStatus = "offline"
		sm.stats.LastStatusChangeTime = time.Now()
	}
}

func (sm *StatsManager) GetStatus() string {
	sm.mu.RLock()
	defer sm.mu.RUnlock()
	return sm.stats.ConnectionStatus
}

func (sm *StatsManager) GetStartTime() time.Time {
	sm.mu.RLock()
	defer sm.mu.RUnlock()
	return sm.stats.StartTime
}

func (sm *StatsManager) GetLastStatusChangeTime() time.Time {
	sm.mu.RLock()
	defer sm.mu.RUnlock()
	return sm.stats.LastStatusChangeTime
}

func (sm *StatsManager) AddBytesTransferred(bytes int64) {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	sm.stats.BytesTransferred += bytes
}

func (sm *StatsManager) PrintStatus() {
	sm.mu.Lock()
	sm.stats.Uptime = time.Since(sm.stats.StartTime).Round(time.Second).String()
	sm.stats.StatusDuration = time.Since(sm.stats.LastStatusChangeTime).Round(time.Second).String()

	var memStats runtime.MemStats
	runtime.ReadMemStats(&memStats)
	sm.stats.Memory = map[string]interface{}{
		"heap_alloc":      memStats.HeapAlloc,
		"heap_sys":        memStats.HeapSys,
		"heap_objects":    memStats.HeapObjects,
		"heap_idle":       memStats.HeapIdle,
		"heap_inuse":      memStats.HeapInuse,
		"stack_inuse":     memStats.StackInuse,
		"stack_sys":       memStats.StackSys,
		"goroutines":      runtime.NumGoroutine(),
		"gc_sys":          memStats.GCSys,
		"next_gc":         memStats.NextGC,
		"last_gc":         time.Unix(0, int64(memStats.LastGC)).Format("2006-01-02 15:04:05"),
		"num_gc":          memStats.NumGC,
		"pause_total_ns": memStats.PauseTotalNs,
	}

	lokiURL := sm.lokiURL
	connectionStatus := sm.stats.ConnectionStatus
	lastStatusChangeTime := sm.stats.LastStatusChangeTime
	statsCopy := sm.stats

	sm.mu.Unlock()

	if lokiURL != "" {
		go sm.pushToLoki(&statsCopy)
	}

	if connectionStatus == "offline" {
		if time.Since(lastStatusChangeTime) > 7*24*60*time.Minute {
			sm.logger.Fatal("Offline exceeded 7 days, exiting...")
		}
	}

	if sm.memoryHook != nil {
		sm.memoryHook()
	}
}

func (sm *StatsManager) StartPeriodicReport(interval time.Duration) {
	sm.ticker = time.NewTicker(interval)
	sm.stopChan = make(chan struct{})
	go func() {
		for {
			select {
			case <-sm.ticker.C:
				sm.PrintStatus()
			case <-sm.stopChan:
				sm.ticker.Stop()
				return
			}
		}
	}()
}

func (sm *StatsManager) Stop() {
	if sm.ticker != nil {
		sm.ticker.Stop()
	}
	if sm.stopChan != nil {
		close(sm.stopChan)
	}
}

func (sm *StatsManager) SetMemoryHook(hook func()) {
	sm.memoryHook = hook
}

func (sm *StatsManager) SetLokiURL(url string) {
	sm.lokiURL = url
}

func (sm *StatsManager) pushToLoki(s *Stats) {
	data, err := json.Marshal(s)
	if err != nil {
		sm.logger.Error("Failed to marshal stats for Loki: %v", err)
		return
	}

	timestamp := time.Now().UnixNano()
	lokiData := map[string]interface{}{
		"streams": []map[string]interface{}{
			{
				"stream": map[string]string{"job": "nssh"},
				"values": [][]string{{fmt.Sprintf("%d", timestamp), string(data)}},
			},
		},
	}

	jsonData, err := json.Marshal(lokiData)
	if err != nil {
		sm.logger.Error("Failed to marshal Loki data: %v", err)
		return
	}

	req, err := http.NewRequest("POST", sm.lokiURL, bytes.NewBuffer(jsonData))
	if err != nil {
		sm.logger.Error("Failed to create Loki request: %v", err)
		return
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := GetHTTPClient().Do(req)
	if err != nil {
		sm.logger.Error("Loki push failed: %v", err)
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		sm.logger.Error("Loki push failed with status: %d", resp.StatusCode)
	}
}
