//go:build windows

package sysmetrics

// collectVRAM returns VRAM usage on Windows. nvidia-smi covers NVIDIA GPUs;
// other vendors are not exposed through a portable interface here.
func collectVRAM() vramSnapshot {
	return nvidiaVRAM()
}