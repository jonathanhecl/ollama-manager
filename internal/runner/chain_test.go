package runner

import (
	"reflect"
	"testing"

	"github.com/gense/ollama-manager/internal/tests"
)

// Replicates the reported SystemA/B/C scenario:
//   - test-level system is SystemA; every case starts fresh from it
//     (or from its own case-level override).
//   - within a chain the system is sticky: empty keeps the active system,
//     non-empty replaces it from that turn onward.
func TestEffectiveChainSystems(t *testing.T) {
	cases := []struct {
		name      string
		base      string
		overrides []string
		want      []string
	}{
		{
			name:      "caso 1: override mid-chain sticks",
			base:      "SystemA",
			overrides: []string{"", "", "SystemB", ""},
			want:      []string{"SystemA", "SystemA", "SystemB", "SystemB"},
		},
		{
			name:      "caso 2: fresh chain inherits test system",
			base:      "SystemA",
			overrides: []string{""},
			want:      []string{"SystemA"},
		},
		{
			name:      "caso 3: step override then inherit",
			base:      "SystemA",
			overrides: []string{"SystemC", ""},
			want:      []string{"SystemC", "SystemC"},
		},
		{
			name:      "case-level override becomes the chain base",
			base:      tests.EffectiveSystemPrompt("SystemA", "SystemC"),
			overrides: []string{"", ""},
			want:      []string{"SystemC", "SystemC"},
		},
		{
			name:      "no system anywhere stays empty",
			base:      "",
			overrides: []string{"", ""},
			want:      []string{"", ""},
		},
		{
			name:      "empty chain",
			base:      "SystemA",
			overrides: nil,
			want:      []string{},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := effectiveChainSystems(tc.base, tc.overrides); !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("effectiveChainSystems(%q, %q) = %q, want %q", tc.base, tc.overrides, got, tc.want)
			}
		})
	}
}

func fptr(v float64) *float64 { return &v }
func iptr(v int) *int         { return &v }

// Same sticky idea as systems, but field by field: a step's set fields win
// from its turn onward, nil fields keep the active values.
func TestEffectiveChainOptions(t *testing.T) {
	cases := []struct {
		name      string
		base      *tests.TestOptions
		overrides []*tests.TestOptions
		want      []*tests.TestOptions
	}{
		{
			name:      "override mid-chain sticks",
			base:      &tests.TestOptions{Temperature: fptr(0.2)},
			overrides: []*tests.TestOptions{nil, {Temperature: fptr(0.9)}, nil},
			want: []*tests.TestOptions{
				{Temperature: fptr(0.2)},
				{Temperature: fptr(0.9)},
				{Temperature: fptr(0.9)},
			},
		},
		{
			name:      "unset fields inherit across levels",
			base:      &tests.TestOptions{Temperature: fptr(0.2), MaxTokens: iptr(100)},
			overrides: []*tests.TestOptions{{TopP: fptr(0.5)}, nil},
			want: []*tests.TestOptions{
				{Temperature: fptr(0.2), TopP: fptr(0.5), MaxTokens: iptr(100)},
				{Temperature: fptr(0.2), TopP: fptr(0.5), MaxTokens: iptr(100)},
			},
		},
		{
			name:      "nil base adopts first override",
			base:      nil,
			overrides: []*tests.TestOptions{nil, {Temperature: fptr(0.7)}},
			want:      []*tests.TestOptions{nil, {Temperature: fptr(0.7)}},
		},
		{
			name:      "empty chain",
			base:      &tests.TestOptions{Temperature: fptr(0.2)},
			overrides: nil,
			want:      []*tests.TestOptions{},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := effectiveChainOptions(tc.base, tc.overrides); !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("effectiveChainOptions(%+v, %+v) = %+v, want %+v", tc.base, tc.overrides, got, tc.want)
			}
		})
	}
}
