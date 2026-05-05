package sysmetrics

import (
	"context"
	"time"

	"github.com/shirou/gopsutil/v4/cpu"
	"github.com/shirou/gopsutil/v4/mem"
)

// Snapshot holds system-level CPU and memory usage.
type Snapshot struct {
	CPUUsedPercent float64
	MemoryTotal    uint64
	MemoryFree     uint64
	MemoryUsed     uint64
	MemoryUsedPct  float64
}

// Collect reads CPU and memory usage with a short timeout.
func Collect(parent context.Context) Snapshot {
	ctx, cancel := context.WithTimeout(parent, 900*time.Millisecond)
	defer cancel()

	out := Snapshot{}

	if pct, err := cpu.PercentWithContext(ctx, 0, false); err == nil && len(pct) > 0 {
		out.CPUUsedPercent = clampPercent(pct[0])
	}

	if vm, err := mem.VirtualMemoryWithContext(ctx); err == nil && vm != nil {
		out.MemoryTotal = vm.Total
		out.MemoryFree = vm.Available
		out.MemoryUsed = vm.Used
		out.MemoryUsedPct = clampPercent(vm.UsedPercent)
	}

	return out
}

func clampPercent(v float64) float64 {
	if v < 0 {
		return 0
	}
	if v > 100 {
		return 100
	}
	return v
}
