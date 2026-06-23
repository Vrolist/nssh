package sdk_go

import (
	"sync/atomic"
	"time"
)

type TunnelStatus struct {
	IsConnected    bool
	RemotePort     int
	StartTime      time.Time
	BytesSent      int64
	BytesReceived  int64
	ConnectionTime time.Time
	LastError      string
}

type StatusManager struct {
	isConnected    atomic.Bool
	remotePort     atomic.Int32
	startTime      atomic.Value
	bytesSent      atomic.Int64
	bytesReceived  atomic.Int64
	connectionTime atomic.Value
	lastError      atomic.Value
}

func NewStatusManager() *StatusManager {
	sm := &StatusManager{}
	sm.startTime.Store(time.Time{})
	sm.connectionTime.Store(time.Time{})
	sm.lastError.Store("")
	return sm
}

func (sm *StatusManager) SetConnected(connected bool) {
	sm.isConnected.Store(connected)
	if connected {
		sm.connectionTime.Store(time.Now())
	}
}

func (sm *StatusManager) SetRemotePort(port int) {
	sm.remotePort.Store(int32(port))
}

func (sm *StatusManager) SetStartTime(t time.Time) {
	sm.startTime.Store(t)
}

func (sm *StatusManager) AddBytesSent(bytes int64) {
	sm.bytesSent.Add(bytes)
}

func (sm *StatusManager) AddBytesReceived(bytes int64) {
	sm.bytesReceived.Add(bytes)
}

func (sm *StatusManager) SetLastError(err string) {
	sm.lastError.Store(err)
}

func (sm *StatusManager) GetStatus() *TunnelStatus {
	startTime, _ := sm.startTime.Load().(time.Time)
	connectionTime, _ := sm.connectionTime.Load().(time.Time)
	lastError, _ := sm.lastError.Load().(string)

	return &TunnelStatus{
		IsConnected:    sm.isConnected.Load(),
		RemotePort:     int(sm.remotePort.Load()),
		StartTime:      startTime,
		BytesSent:      sm.bytesSent.Load(),
		BytesReceived:  sm.bytesReceived.Load(),
		ConnectionTime: connectionTime,
		LastError:      lastError,
	}
}

func (sm *StatusManager) Reset() {
	sm.isConnected.Store(false)
	sm.remotePort.Store(0)
	sm.startTime.Store(time.Time{})
	sm.bytesSent.Store(0)
	sm.bytesReceived.Store(0)
	sm.connectionTime.Store(time.Time{})
	sm.lastError.Store("")
}
