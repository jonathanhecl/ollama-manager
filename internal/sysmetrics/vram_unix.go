//go:build !windows

package sysmetrics

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// collectVRAM returns VRAM usage on non-Windows systems. It prefers NVIDIA
// telemetry, then falls back to the amdgpu DRM sysfs. On macOS (unified
// memory) there is no separate VRAM, so OK is false.
func collectVRAM() vramSnapshot {
	if v := nvidiaVRAM(); v.OK {
		return v
	}
	return amdgpuVRAM()
}

// amdgpuVRAM reads per-device VRAM totals from the amdgpu DRM sysfs.
func amdgpuVRAM() vramSnapshot {
	var total, used uint64
	matches, _ := filepath.Glob("/sys/class/drm/card*/device/mem_info_vram_total")
	for _, p := range matches {
		t, err := readUint64File(p)
		if err != nil {
			continue
		}
		u, err := readUint64File(strings.TrimSuffix(p, "_total") + "_used")
		if err != nil {
			continue
		}
		total += t
		used += u
	}
	if total == 0 {
		return vramSnapshot{}
	}
	if used > total {
		used = total
	}
	return vramSnapshot{Total: total, Used: used, OK: true}
}

func readUint64File(p string) (uint64, error) {
	b, err := os.ReadFile(p)
	if err != nil {
		return 0, err
	}
	return strconv.ParseUint(strings.TrimSpace(string(b)), 10, 64)
}