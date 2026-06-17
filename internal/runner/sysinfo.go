// Package runner implements the test battery execution engine.
package runner

import (
	"bytes"
	"fmt"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
)

// SysInfo captures the hardware / OS environment where a battery run was executed.
type SysInfo struct {
	OS       string `json:"os,omitempty"`
	CPUModel string `json:"cpu_model,omitempty"`
	GPUModel string `json:"gpu_model,omitempty"`
	VRAMGB   string `json:"vram_gb,omitempty"`
	RAMGB    string `json:"ram_gb,omitempty"`
}

// DetectSysInfo attempts to gather OS and hardware details.
// It is best-effort: fields are left empty when detection fails.
func DetectSysInfo() SysInfo {
	s := SysInfo{OS: runtime.GOOS}
	switch runtime.GOOS {
	case "windows":
		s.CPUModel = windowsCPU()
		s.GPUModel, s.VRAMGB = windowsGPU()
		ramBytes := wmicIntValue(execOutput("wmic", "ComputerSystem", "get", "TotalPhysicalMemory", "/value"), "TotalPhysicalMemory")
		if ramBytes > 0 {
			s.RAMGB = fmt.Sprintf("%.1f", float64(ramBytes)/(1024*1024*1024))
		}
	case "linux":
		s.CPUModel = linuxCPU()
		s.GPUModel = linuxGPU()
		ramKB := parseIntSuffix(execOutput("grep", "MemTotal", "/proc/meminfo"), "kB")
		if ramKB > 0 {
			s.RAMGB = fmt.Sprintf("%.1f", float64(ramKB)/(1024*1024))
		}
		vramMB := nvidiaVRAM()
		if vramMB > 0 {
			s.VRAMGB = fmt.Sprintf("%.1f", float64(vramMB)/1024)
		}
	case "darwin":
		s.CPUModel = strings.TrimSpace(execOutput("sysctl", "-n", "machdep.cpu.brand_string"))
		ramBytes := parseIntSuffix(execOutput("sysctl", "-n", "hw.memsize"), "")
		if ramBytes > 0 {
			s.RAMGB = fmt.Sprintf("%.1f", float64(ramBytes)/(1024*1024*1024))
		}
		s.GPUModel = macGPU()
	}
	return s
}

func execOutput(name string, args ...string) string {
	cmd := exec.Command(name, args...)
	var out bytes.Buffer
	cmd.Stdout = &out
	_ = cmd.Run()
	return out.String()
}

// wmicValue extracts the value after key= from wmic /value output.
func wmicValue(s, key string) string {
	prefix := key + "="
	for _, line := range strings.Split(s, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, prefix) {
			return strings.TrimSpace(strings.TrimPrefix(line, prefix))
		}
	}
	return ""
}

// wmicIntValue extracts and parses the int value after key=.
func wmicIntValue(s, key string) int64 {
	v := wmicValue(s, key)
	if v == "" {
		return 0
	}
	n, _ := strconv.ParseInt(v, 10, 64)
	return n
}

func windowsCPU() string {
	return wmicValue(execOutput("wmic", "cpu", "get", "Name", "/value"), "Name")
}

// windowsGPU returns the best GPU name and VRAM in GB.
// It filters out virtual and basic display adapters and prefers
// discrete GPUs (NVIDIA/AMD) over integrated Intel.
func windowsGPU() (string, string) {
	out := execOutput("wmic", "path", "win32_VideoController", "get", "Name,AdapterRAM", "/value")
	type gpu struct {
		name string
		ram  int64
	}
	var gpus []gpu
	var cur gpu
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			if cur.name != "" {
				gpus = append(gpus, cur)
				cur = gpu{}
			}
			continue
		}
		if strings.HasPrefix(line, "Name=") {
			cur.name = strings.TrimSpace(strings.TrimPrefix(line, "Name="))
		}
		if strings.HasPrefix(line, "AdapterRAM=") {
			v := strings.TrimSpace(strings.TrimPrefix(line, "AdapterRAM="))
			if v != "" {
				cur.ram, _ = strconv.ParseInt(v, 10, 64)
			}
		}
	}
	if cur.name != "" {
		gpus = append(gpus, cur)
	}

	// Filter virtual / basic adapters.
	var filtered []gpu
	for _, g := range gpus {
		lower := strings.ToLower(g.name)
		if strings.Contains(lower, "virtual") || strings.Contains(lower, "basic display") || strings.Contains(lower, "basic render") || strings.Contains(lower, "microsoft") {
			continue
		}
		if g.name == "" {
			continue
		}
		filtered = append(filtered, g)
	}
	if len(filtered) == 0 {
		return "", ""
	}

	// Prefer discrete GPU: NVIDIA or AMD over Intel integrated.
	for _, g := range filtered {
		lower := strings.ToLower(g.name)
		if strings.Contains(lower, "nvidia") || strings.Contains(lower, "amd") || strings.Contains(lower, "radeon") || strings.Contains(lower, "geforce") {
			return g.name, vramGB(g.ram)
		}
	}
	// Fallback to first available.
	return filtered[0].name, vramGB(filtered[0].ram)
}

func vramGB(bytes int64) string {
	if bytes <= 0 {
		return ""
	}
	gb := float64(bytes) / (1024 * 1024 * 1024)
	// Sanity: if reported value looks like MB (common wmic bug), convert.
	if gb < 0.5 && bytes > 0 {
		gb = float64(bytes) / (1024 * 1024)
	}
	if gb > 128 {
		return "" // sanity cap
	}
	return fmt.Sprintf("%.1f", gb)
}

func parseIntSuffix(s, suffix string) int64 {
	fields := strings.Fields(s)
	for _, f := range fields {
		f = strings.TrimSpace(f)
		if suffix != "" && strings.HasSuffix(f, suffix) {
			f = strings.TrimSuffix(f, suffix)
		}
		if v, err := strconv.ParseInt(f, 10, 64); err == nil {
			return v
		}
	}
	return 0
}

func linuxCPU() string {
	out := execOutput("grep", "-m1", "model name", "/proc/cpuinfo")
	parts := strings.SplitN(out, ":", 2)
	if len(parts) == 2 {
		return strings.TrimSpace(parts[1])
	}
	return ""
}

func linuxGPU() string {
	// Try lspci first
	out := execOutput("lspci")
	for _, line := range strings.Split(out, "\n") {
		if strings.Contains(line, "VGA") || strings.Contains(line, "3D") {
			parts := strings.SplitN(line, ":", 3)
			if len(parts) >= 3 {
				return strings.TrimSpace(parts[2])
			}
		}
	}
	return ""
}

func nvidiaVRAM() int64 {
	out := execOutput("nvidia-smi", "--query-gpu=memory.total", "--format=csv,noheader,nounits")
	fields := strings.Fields(out)
	if len(fields) > 0 {
		v, _ := strconv.ParseInt(fields[0], 10, 64)
		return v
	}
	return 0
}

func macGPU() string {
	out := execOutput("system_profiler", "SPDisplaysDataType")
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "Chipset Model:") {
			parts := strings.SplitN(line, ":", 2)
			if len(parts) == 2 {
				return strings.TrimSpace(parts[1])
			}
		}
	}
	return ""
}
