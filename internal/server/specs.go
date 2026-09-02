package server

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/gense/ollama-manager/internal/sysmetrics"
)

// DeviceSpecs captures the hardware and operating system details for model benchmarking.
type DeviceSpecs struct {
	OS       string   `json:"os"`
	CPU      string   `json:"cpu"`
	RAM      string   `json:"ram"`
	RAMSpeed string   `json:"ram_speed,omitempty"`
	GPU      string   `json:"gpu,omitempty"`
	GPUs     []string `json:"gpus,omitempty"`
	VRAM     string   `json:"vram,omitempty"`
}

// DeviceUsageEntry holds the specs and benchmarked models for a single machine.
type DeviceUsageEntry struct {
	ID     string                      `json:"id"`
	Specs  DeviceSpecs                 `json:"specs"`
	Models map[string]ModelUsageRecord `json:"models"`
}

// DeviceUsageSummary exposes high-level info about a machine for frontend selection.
type DeviceUsageSummary struct {
	ID          string      `json:"id"`
	Name        string      `json:"name"`
	Specs       DeviceSpecs `json:"specs"`
	IsCurrent   bool        `json:"is_current"`
	ModelsCount int         `json:"models_count"`
}

// DetectCurrentDeviceSpecs discovers the current host's OS, CPU, RAM, and GPU info.
func DetectCurrentDeviceSpecs() (DeviceSpecs, string) {
	var specs DeviceSpecs

	switch runtime.GOOS {
	case "darwin":
		specs = detectDarwinSpecs()
	case "windows":
		specs = detectWindowsSpecs()
	case "linux":
		specs = detectLinuxSpecs()
	default:
		specs = DeviceSpecs{
			OS:  runtime.GOOS,
			CPU: sysmetrics.CPUModel(),
			RAM: "Unknown",
		}
	}

	id := GenerateDeviceID(specs)
	return specs, id
}

// GenerateDeviceID produces a stable slug/fingerprint based on the hardware.
// Keeping hardware stable allows minor OS updates to map to the same machine entry.
func GenerateDeviceID(specs DeviceSpecs) string {
	parts := []string{runtime.GOOS, runtime.GOARCH}
	if specs.CPU != "" {
		parts = append(parts, cleanSlug(specs.CPU))
	}
	if specs.RAM != "" {
		parts = append(parts, cleanSlug(specs.RAM))
	}
	if specs.GPU != "" {
		parts = append(parts, cleanSlug(specs.GPU))
	}
	if specs.VRAM != "" {
		parts = append(parts, cleanSlug(specs.VRAM))
	}

	base := strings.Join(parts, "-")
	if len(base) > 60 {
		h := sha256.Sum256([]byte(base))
		base = base[:40] + "-" + hex.EncodeToString(h[:4])
	}
	if base == "" {
		return "device-default"
	}
	return base
}

func cleanSlug(s string) string {
	s = strings.ToLower(s)
	re := regexp.MustCompile(`[^a-z0-9]+`)
	s = re.ReplaceAllString(s, "-")
	return strings.Trim(s, "-")
}

func detectDarwinSpecs() DeviceSpecs {
	cpu := strings.TrimSpace(execWithTimeout(1*time.Second, "sysctl", "-n", "machdep.cpu.brand_string"))
	if cpu == "" {
		cpu = sysmetrics.CPUModel()
	}
	if cpu == "" {
		cpu = strings.TrimSpace(execWithTimeout(1*time.Second, "sysctl", "-n", "hw.model"))
	}

	ram := ""
	ramRaw := strings.TrimSpace(execWithTimeout(1*time.Second, "sysctl", "-n", "hw.memsize"))
	if b, err := strconv.ParseInt(ramRaw, 10, 64); err == nil && b > 0 {
		gb := math.Round(float64(b) / (1024 * 1024 * 1024))
		ram = fmt.Sprintf("%.0f GB", gb)
	}

	osVer := strings.TrimSpace(execWithTimeout(1*time.Second, "sysctl", "-n", "kern.osproductversion"))
	if osVer == "" {
		osVer = strings.TrimSpace(execWithTimeout(1*time.Second, "sw_vers", "-productVersion"))
	}
	osName := "macOS"
	if osVer != "" {
		osName = "macOS " + osVer
	}

	// For Mac Silicon (Apple M-series or arm64), architecture integrates CPU, GPU and unified memory.
	// As requested: "en caso Mac Silicon, guarde OS (version), CPU y RAM."
	// GPU, GPUs, VRAM, and RAMSpeed remain empty and omitted in JSON.
	isAppleSilicon := runtime.GOARCH == "arm64" || strings.HasPrefix(cpu, "Apple")
	if isAppleSilicon {
		return DeviceSpecs{
			OS:  osName,
			CPU: cpu,
			RAM: ram,
		}
	}

	// Intel Mac fallback: detect discrete GPU if any
	gpu := ""
	sp := execWithTimeout(2*time.Second, "system_profiler", "SPDisplaysDataType")
	for _, line := range strings.Split(sp, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "Chipset Model:") {
			gpu = strings.TrimSpace(strings.TrimPrefix(line, "Chipset Model:"))
			break
		}
	}

	return DeviceSpecs{
		OS:   osName,
		CPU:  cpu,
		RAM:  ram,
		GPU:  gpu,
		GPUs: filterNonEmptySlice([]string{gpu}),
	}
}

func detectWindowsSpecs() DeviceSpecs {
	osName := "Windows"
	wmicOS := execWithTimeout(2*time.Second, "wmic", "os", "get", "Caption,Version", "/value")
	capVal := extractWmicValue(wmicOS, "Caption")
	verVal := extractWmicValue(wmicOS, "Version")
	if capVal != "" {
		osName = strings.TrimPrefix(capVal, "Microsoft ")
		if verVal != "" {
			osName += fmt.Sprintf(" (%s)", verVal)
		}
	}

	cpu := extractWmicValue(execWithTimeout(2*time.Second, "wmic", "cpu", "get", "Name", "/value"), "Name")
	if cpu == "" {
		cpu = sysmetrics.CPUModel()
	}

	ram := ""
	wmicRAM := extractWmicValue(execWithTimeout(2*time.Second, "wmic", "ComputerSystem", "get", "TotalPhysicalMemory", "/value"), "TotalPhysicalMemory")
	if b, err := strconv.ParseInt(wmicRAM, 10, 64); err == nil && b > 0 {
		gb := math.Round(float64(b) / (1024 * 1024 * 1024))
		ram = fmt.Sprintf("%.0f GB", gb)
	}

	// RAM MHz
	ramSpeed := ""
	wmicSpeed := execWithTimeout(2*time.Second, "wmic", "memorychip", "get", "ConfiguredClockSpeed,Speed", "/value")
	for _, line := range strings.Split(wmicSpeed, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "ConfiguredClockSpeed=") || strings.HasPrefix(line, "Speed=") {
			val := strings.TrimSpace(strings.SplitN(line, "=", 2)[1])
			if n, err := strconv.Atoi(val); err == nil && n > 0 {
				ramSpeed = fmt.Sprintf("%d MHz", n)
				break
			}
		}
	}

	// GPU and VRAM
	gpus, vram := detectWindowsGPUs()

	gpuStr := ""
	if len(gpus) == 1 {
		gpuStr = gpus[0]
	} else if len(gpus) > 1 {
		gpuStr = strings.Join(gpus, ", ")
	}

	return DeviceSpecs{
		OS:       osName,
		CPU:      cpu,
		RAM:      ram,
		RAMSpeed: ramSpeed,
		GPU:      gpuStr,
		GPUs:     gpus,
		VRAM:     vram,
	}
}

func detectWindowsGPUs() ([]string, string) {
	// First check nvidia-smi
	nvOut := execWithTimeout(2*time.Second, "nvidia-smi", "--query-gpu=name,memory.total", "--format=csv,noheader,nounits")
	if nvOut != "" {
		var gpus []string
		var totalVRAMMB int64
		for _, line := range strings.Split(nvOut, "\n") {
			parts := strings.Split(line, ",")
			if len(parts) >= 2 {
				name := strings.TrimSpace(parts[0])
				if name != "" {
					gpus = append(gpus, name)
				}
				if mb, err := strconv.ParseInt(strings.TrimSpace(parts[1]), 10, 64); err == nil {
					totalVRAMMB += mb
				}
			}
		}
		if len(gpus) > 0 {
			vramStr := ""
			if totalVRAMMB > 0 {
				vramStr = fmt.Sprintf("%.0f GB", math.Round(float64(totalVRAMMB)/1024))
			}
			return gpus, vramStr
		}
	}

	// Fallback to wmic
	wmicVid := execWithTimeout(2*time.Second, "wmic", "path", "win32_VideoController", "get", "Name,AdapterRAM", "/value")
	var gpus []string
	var totalVRAMBytes int64
	var curName string
	var curRAM int64

	for _, line := range strings.Split(wmicVid, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "Name=") {
			curName = strings.TrimSpace(strings.TrimPrefix(line, "Name="))
		} else if strings.HasPrefix(line, "AdapterRAM=") {
			v := strings.TrimSpace(strings.TrimPrefix(line, "AdapterRAM="))
			curRAM, _ = strconv.ParseInt(v, 10, 64)
		} else if line == "" && curName != "" {
			lower := strings.ToLower(curName)
			if !strings.Contains(lower, "virtual") && !strings.Contains(lower, "basic display") && !strings.Contains(lower, "microsoft") {
				gpus = append(gpus, curName)
				if curRAM > 0 {
					totalVRAMBytes += curRAM
				}
			}
			curName = ""
			curRAM = 0
		}
	}
	if curName != "" {
		lower := strings.ToLower(curName)
		if !strings.Contains(lower, "virtual") && !strings.Contains(lower, "basic display") && !strings.Contains(lower, "microsoft") {
			gpus = append(gpus, curName)
			if curRAM > 0 {
				totalVRAMBytes += curRAM
			}
		}
	}

	vramStr := ""
	if totalVRAMBytes > 0 {
		gb := float64(totalVRAMBytes) / (1024 * 1024 * 1024)
		if gb < 0.5 {
			gb = float64(totalVRAMBytes) / (1024 * 1024) // wmic mb bug
		}
		if gb > 0 && gb <= 512 {
			vramStr = fmt.Sprintf("%.0f GB", math.Round(gb))
		}
	}

	return gpus, vramStr
}

func detectLinuxSpecs() DeviceSpecs {
	osName := "Linux"
	if data, err := os.ReadFile("/etc/os-release"); err == nil {
		for _, line := range strings.Split(string(data), "\n") {
			line = strings.TrimSpace(line)
			if strings.HasPrefix(line, "PRETTY_NAME=") {
				val := strings.TrimPrefix(line, "PRETTY_NAME=")
				osName = strings.Trim(val, `"'`)
				break
			}
		}
	}

	cpu := ""
	if data, err := os.ReadFile("/proc/cpuinfo"); err == nil {
		for _, line := range strings.Split(string(data), "\n") {
			if strings.HasPrefix(line, "model name") {
				parts := strings.SplitN(line, ":", 2)
				if len(parts) == 2 {
					cpu = strings.TrimSpace(parts[1])
					break
				}
			}
		}
	}
	if cpu == "" {
		cpu = sysmetrics.CPUModel()
	}

	ram := ""
	if data, err := os.ReadFile("/proc/meminfo"); err == nil {
		for _, line := range strings.Split(string(data), "\n") {
			if strings.HasPrefix(line, "MemTotal:") {
				fields := strings.Fields(line)
				if len(fields) >= 2 {
					if kb, err := strconv.ParseInt(fields[1], 10, 64); err == nil && kb > 0 {
						gb := math.Round(float64(kb) / (1024 * 1024))
						ram = fmt.Sprintf("%.0f GB", gb)
					}
				}
				break
			}
		}
	}

	// RAM MHz
	ramSpeed := ""
	dmi := execWithTimeout(1*time.Second, "dmidecode", "-t", "memory")
	if dmi != "" {
		re := regexp.MustCompile(`(?i)(?:Configured\s+Memory\s+Speed|Speed):\s*(\d+)\s*(?:MT/s|MHz)`)
		if m := re.FindStringSubmatch(dmi); len(m) >= 2 {
			ramSpeed = m[1] + " MHz"
		}
	}

	// GPUs & VRAM
	gpus, vram := detectLinuxGPUs()
	gpuStr := ""
	if len(gpus) == 1 {
		gpuStr = gpus[0]
	} else if len(gpus) > 1 {
		gpuStr = strings.Join(gpus, ", ")
	}

	return DeviceSpecs{
		OS:       osName,
		CPU:      cpu,
		RAM:      ram,
		RAMSpeed: ramSpeed,
		GPU:      gpuStr,
		GPUs:     gpus,
		VRAM:     vram,
	}
}

func detectLinuxGPUs() ([]string, string) {
	// First check nvidia-smi
	nvOut := execWithTimeout(2*time.Second, "nvidia-smi", "--query-gpu=name,memory.total", "--format=csv,noheader,nounits")
	if nvOut != "" {
		var gpus []string
		var totalVRAMMB int64
		for _, line := range strings.Split(nvOut, "\n") {
			parts := strings.Split(line, ",")
			if len(parts) >= 2 {
				name := strings.TrimSpace(parts[0])
				if name != "" {
					gpus = append(gpus, name)
				}
				if mb, err := strconv.ParseInt(strings.TrimSpace(parts[1]), 10, 64); err == nil {
					totalVRAMMB += mb
				}
			}
		}
		if len(gpus) > 0 {
			vramStr := ""
			if totalVRAMMB > 0 {
				vramStr = fmt.Sprintf("%.0f GB", math.Round(float64(totalVRAMMB)/1024))
			}
			return gpus, vramStr
		}
	}

	// lspci check
	var gpus []string
	lspci := execWithTimeout(2*time.Second, "lspci")
	for _, line := range strings.Split(lspci, "\n") {
		if strings.Contains(line, "VGA compatible controller") || strings.Contains(line, "3D controller") {
			parts := strings.SplitN(line, ":", 3)
			if len(parts) >= 3 {
				gpuName := strings.TrimSpace(parts[2])
				if gpuName != "" {
					gpus = append(gpus, gpuName)
				}
			}
		}
	}

	// Check AMD DRM VRAM
	var totalVRAMBytes uint64
	matches, _ := filepath.Glob("/sys/class/drm/card*/device/mem_info_vram_total")
	for _, p := range matches {
		if b, err := os.ReadFile(p); err == nil {
			if v, err := strconv.ParseUint(strings.TrimSpace(string(b)), 10, 64); err == nil {
				totalVRAMBytes += v
			}
		}
	}
	vramStr := ""
	if totalVRAMBytes > 0 {
		vramStr = fmt.Sprintf("%.0f GB", math.Round(float64(totalVRAMBytes)/(1024*1024*1024)))
	}

	return gpus, vramStr
}

func execWithTimeout(timeout time.Duration, name string, args ...string) string {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, name, args...)
	var out bytes.Buffer
	cmd.Stdout = &out
	_ = cmd.Run()
	return strings.TrimSpace(out.String())
}

func extractWmicValue(s, key string) string {
	prefix := strings.ToLower(key) + "="
	for _, line := range strings.Split(s, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(strings.ToLower(line), prefix) {
			return strings.TrimSpace(line[len(prefix):])
		}
	}
	return ""
}

func filterNonEmptySlice(items []string) []string {
	var out []string
	for _, it := range items {
		it = strings.TrimSpace(it)
		if it != "" {
			out = append(out, it)
		}
	}
	return out
}

// DeviceDisplayName creates a concise, beautiful label for UI dropdowns.
func DeviceDisplayName(specs DeviceSpecs, isCurrent bool) string {
	var parts []string
	if specs.CPU != "" {
		parts = append(parts, specs.CPU)
	}
	if specs.RAM != "" {
		ramStr := specs.RAM
		if specs.RAMSpeed != "" {
			ramStr += " (" + specs.RAMSpeed + ")"
		}
		parts = append(parts, ramStr)
	}
	if specs.GPU != "" {
		gpuStr := specs.GPU
		if specs.VRAM != "" {
			gpuStr += " (" + specs.VRAM + ")"
		}
		parts = append(parts, gpuStr)
	}
	if specs.OS != "" {
		parts = append(parts, specs.OS)
	}

	res := strings.Join(parts, " · ")
	if res == "" {
		res = "Unknown Device"
	}
	return res
}
