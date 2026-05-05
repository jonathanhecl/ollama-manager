package sysmetrics

import (
	"context"
	"os"
	"path/filepath"
	"strings"
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
	DiskTotal      uint64
	DiskFree       uint64
	DiskUsed       uint64
	DiskUsedPct    float64
}

// Collect reads CPU, memory, and disk usage with a short timeout.
func Collect(parent context.Context, path string) Snapshot {
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

	if total, free, err := diskForPath(existingPathOrParent(path)); err == nil && total > 0 {
		if free > total {
			free = total
		}
		out.DiskTotal = total
		out.DiskFree = free
		out.DiskUsed = total - free
		out.DiskUsedPct = clampPercent((float64(out.DiskUsed) / float64(total)) * 100)
	}

	return out
}

func existingPathOrParent(path string) string {
	p := strings.TrimSpace(path)
	if p == "" {
		return "."
	}
	abs, err := filepath.Abs(p)
	if err != nil {
		abs = p
	}
	for {
		if _, err := os.Stat(abs); err == nil {
			return abs
		}
		parent := filepath.Dir(abs)
		if parent == abs {
			return abs
		}
		abs = parent
	}
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
