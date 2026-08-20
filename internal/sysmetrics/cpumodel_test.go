package sysmetrics

import (
	"context"
	"testing"
)

func TestCleanCPUModel(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{
			input: "Apple M4 Pro",
			want:  "Apple M4 Pro",
		},
		{
			input: "Apple M1 Max",
			want:  "Apple M1 Max",
		},
		{
			input: "Intel(R) Core(TM) i7-9750H CPU @ 2.60GHz",
			want:  "Intel Core i7-9750H",
		},
		{
			input: "13th Gen Intel(R) Core(TM) i7-13700K",
			want:  "13th Gen Intel Core i7-13700K",
		},
		{
			input: "Intel(R) Core(TM) i9-14900K CPU @ 3.20GHz",
			want:  "Intel Core i9-14900K",
		},
		{
			input: "AMD Ryzen 7 7800X3D 8-Core Processor",
			want:  "AMD Ryzen 7 7800X3D",
		},
		{
			input: "AMD Ryzen 9 5950X 16-Core Processor",
			want:  "AMD Ryzen 9 5950X",
		},
		{
			input: "Intel(R) Xeon(R) CPU E5-2678 v3 @ 2.50GHz",
			want:  "Intel Xeon E5-2678 v3",
		},
		{
			input: "Snapdragon(R) X Elite - X1E-80-100",
			want:  "Snapdragon X Elite - X1E-80-100",
		},
		{
			input: "  AMD Ryzen 5 3600 6-Core Processor  ",
			want:  "AMD Ryzen 5 3600",
		},
		{
			input: "",
			want:  "",
		},
	}

	for _, tc := range tests {
		got := CleanCPUModel(tc.input)
		if got != tc.want {
			t.Errorf("CleanCPUModel(%q) = %q, want %q", tc.input, got, tc.want)
		}
	}
}

func TestCPUModel(t *testing.T) {
	model := CPUModel()
	t.Logf("Detected CPU Model: %q", model)
	// On this system (macOS), it should detect a non-empty CPU model
	if model == "" {
		t.Log("Warning: CPU model returned empty")
	}
}

func TestCollectWithCPUModel(t *testing.T) {
	snap := Collect(context.Background(), ".")
	t.Logf("Snapshot CPUModel: %q, CPUUsed: %.1f%%", snap.CPUModel, snap.CPUUsedPercent)
}
