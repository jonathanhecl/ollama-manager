package sysmetrics

import (
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"
)

// vramSnapshot holds VRAM usage aggregated across all detected GPUs.
type vramSnapshot struct {
	Total uint64
	Used  uint64
	OK    bool
}

var (
	vramCacheMu    sync.Mutex
	vramCacheValue vramSnapshot
	vramCacheTime  time.Time
)

// CollectVRAM returns total and used VRAM across all GPUs. It is best-effort:
// OK is false when no GPU telemetry is available (e.g. macOS unified memory,
// or no NVIDIA/AMD driver telemetry). Results are cached briefly to avoid
// spawning processes on every status poll.
func CollectVRAM() vramSnapshot {
	vramCacheMu.Lock()
	defer vramCacheMu.Unlock()
	if time.Since(vramCacheTime) < 1*time.Second {
		return vramCacheValue
	}
	v := collectVRAM()
	vramCacheValue = v
	vramCacheTime = time.Now()
	return v
}

// nvidiaVRAM returns total and used bytes summed across all NVIDIA GPUs via
// nvidia-smi. The CSV values are in MiB.
func nvidiaVRAM() vramSnapshot {
	out, err := exec.Command("nvidia-smi",
		"--query-gpu=memory.total,memory.used",
		"--format=csv,noheader,nounits").Output()
	if err != nil {
		return vramSnapshot{}
	}
	var total, used uint64
	for _, line := range strings.Split(string(out), "\n") {
		fields := strings.Split(line, ",")
		if len(fields) < 2 {
			continue
		}
		t, err1 := strconv.ParseUint(strings.TrimSpace(fields[0]), 10, 64)
		u, err2 := strconv.ParseUint(strings.TrimSpace(fields[1]), 10, 64)
		if err1 != nil || err2 != nil {
			continue
		}
		total += t
		used += u
	}
	if total == 0 {
		return vramSnapshot{}
	}
	return vramSnapshot{Total: total * 1024 * 1024, Used: used * 1024 * 1024, OK: true}
}