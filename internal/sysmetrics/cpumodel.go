package sysmetrics

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/shirou/gopsutil/v4/cpu"
)

var (
	cachedCPUModel     string
	cachedCPUModelOnce sync.Once
)

var (
	reMultiSpace   = regexp.MustCompile(`\s+`)
	reCleanSymbols = regexp.MustCompile(`(?i)\((?:r|tm|c)\)`)
	reTrailingFreq = regexp.MustCompile(`(?i)(?:\s+CPU)?\s*@\s*[\d\.]+\s*(?:ghz|mhz)`)
	reCoresSuffix  = regexp.MustCompile(`(?i)\b\d+-(?:core|core processor)\b`)
	reProcSuffix   = regexp.MustCompile(`(?i)\bprocessor\b`)
	reCPUWord      = regexp.MustCompile(`(?i)\bcpu\b`)
)

// CPUModel returns the detected, cleaned CPU model name.
// It is detected once and cached in memory.
func CPUModel() string {
	cachedCPUModelOnce.Do(func() {
		cachedCPUModel = detectCPUModel()
	})
	return cachedCPUModel
}

func detectCPUModel() string {
	// Try gopsutil first with a short timeout
	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel()

	if infos, err := cpu.InfoWithContext(ctx); err == nil && len(infos) > 0 {
		for _, info := range infos {
			if m := strings.TrimSpace(info.ModelName); m != "" {
				return CleanCPUModel(m)
			}
		}
	}

	// Fallbacks depending on OS
	var raw string
	switch runtime.GOOS {
	case "darwin":
		raw = execOutput("sysctl", "-n", "machdep.cpu.brand_string")
		if strings.TrimSpace(raw) == "" {
			raw = execOutput("sysctl", "-n", "hw.model")
		}
	case "linux":
		if data, err := os.ReadFile("/proc/cpuinfo"); err == nil {
			for _, line := range strings.Split(string(data), "\n") {
				lower := strings.ToLower(line)
				if strings.HasPrefix(lower, "model name") {
					parts := strings.SplitN(line, ":", 2)
					if len(parts) == 2 {
						raw = parts[1]
						break
					}
				}
			}
		}
		if strings.TrimSpace(raw) == "" {
			raw = execOutput("lscpu")
			for _, line := range strings.Split(raw, "\n") {
				lower := strings.ToLower(line)
				if strings.HasPrefix(lower, "model name:") {
					parts := strings.SplitN(line, ":", 2)
					if len(parts) == 2 {
						raw = parts[1]
						break
					}
				}
			}
		}
	case "windows":
		raw = execOutput("wmic", "cpu", "get", "Name", "/value")
		for _, line := range strings.Split(raw, "\n") {
			line = strings.TrimSpace(line)
			if strings.HasPrefix(strings.ToLower(line), "name=") {
				parts := strings.SplitN(line, "=", 2)
				if len(parts) == 2 {
					raw = parts[1]
					break
				}
			}
		}
		if strings.TrimSpace(raw) == "" {
			raw = execOutput("powershell", "-NoProfile", "-Command", "(Get-CimInstance Win32_Processor).Name")
		}
	}

	return CleanCPUModel(raw)
}

func execOutput(name string, args ...string) string {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, name, args...)
	var out bytes.Buffer
	cmd.Stdout = &out
	_ = cmd.Run()
	return out.String()
}

// CleanCPUModel normalizes raw CPU brand strings into clean, readable names.
func CleanCPUModel(raw string) string {
	s := strings.TrimSpace(raw)
	if s == "" {
		return ""
	}

	// Remove (R), (TM), (C) symbols
	s = reCleanSymbols.ReplaceAllString(s, "")

	// Remove trailing clock speed "@ 2.60GHz" or "CPU @ 3.40GHz"
	s = reTrailingFreq.ReplaceAllString(s, "")

	// Remove "8-Core Processor" or "8-Core"
	s = reCoresSuffix.ReplaceAllString(s, "")

	// Remove trailing "Processor"
	s = reProcSuffix.ReplaceAllString(s, "")

	// Remove standalone "CPU" word if there are other descriptive words
	cleanedWithoutCPU := strings.TrimSpace(reCPUWord.ReplaceAllString(s, ""))
	if cleanedWithoutCPU != "" {
		s = cleanedWithoutCPU
	}

	// Collapse multiple whitespaces
	s = reMultiSpace.ReplaceAllString(s, " ")
	s = strings.TrimSpace(s)

	return s
}
