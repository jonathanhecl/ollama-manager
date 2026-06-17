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
	OS        string `json:"os,omitempty"`
	CPUModel  string `json:"cpu_model,omitempty"`
	GPUModel  string `json:"gpu_model,omitempty"`
	VRAMGB    string `json:"vram_gb,omitempty"`
	RAMGB     string `json:"ram_gb,omitempty"`
}

// DetectSysInfo attempts to gather OS and hardware details.
// It is best-effort: fields are left empty when detection fails.
func DetectSysInfo() SysInfo {
	s := SysInfo{OS: runtime.GOOS}
	switch runtime.GOOS {
	case "windows":
		s.CPUModel = cleanWmic(execOutput("wmic", "cpu", "get", "Name", "/value"))
		s.GPUModel = cleanWmic(execOutput("wmic", "path", "win32_VideoController", "get", "Name", "/value"))
		ramBytes := parseWmicInt(execOutput("wmic", "ComputerSystem", "get", "TotalPhysicalMemory", "/value"))
		if ramBytes > 0 {
			s.RAMGB = fmt.Sprintf("%.1f", float64(ramBytes)/(1024*1024*1024))
		}
		vramBytes := parseWmicInt(execOutput("wmic", "path", "win32_VideoController", "get", "AdapterRAM", "/value"))
		if vramBytes > 0 && vramBytes < 1<<40 { // sanity cap
			s.VRAMGB = fmt.Sprintf("%.1f", float64(vramBytes)/(1024*1024*1024))
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

func cleanWmic(s string) string {
	lines := strings.Split(s, "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "Name=") {
			continue
		}
		return line
	}
	return ""
}

func parseWmicInt(s string) int64 {
	for _, line := range strings.Split(s, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "TotalPhysicalMemory=") || strings.HasPrefix(line, "AdapterRAM=") {
			continue
		}
		v, _ := strconv.ParseInt(line, 10, 64)
		return v
	}
	return 0
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
