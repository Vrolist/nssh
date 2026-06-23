// +build !memory

package base_core

type MemoryMonitor struct{}

func NewMemoryMonitor() *MemoryMonitor {
	return &MemoryMonitor{}
}

func (m *MemoryMonitor) PrintMemoryStats() {
}
