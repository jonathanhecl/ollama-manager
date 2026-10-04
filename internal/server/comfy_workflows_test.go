package server

import (
	"encoding/json"
	"testing"

	"github.com/gense/ollama-manager/internal/comfyui"
)

// stockSDGraph is the shape nearly every SD and SDXL workflow exports: two text
// encoders whose text says nothing about which is which.
func stockSDGraph(negativeText string) comfyui.Workflow {
	return comfyui.Workflow{
		"3": {ClassType: "KSampler", Inputs: map[string]any{
			"seed": float64(12345), "steps": float64(20), "cfg": 7.5, "denoise": 1.0,
		}},
		"4": {ClassType: "EmptyLatentImage", Inputs: map[string]any{
			"width": float64(768), "height": float64(1024),
		}},
		"6": {ClassType: "CLIPTextEncode", Inputs: map[string]any{"text": "a cat, studio light"}},
		"7": {ClassType: "CLIPTextEncode", Inputs: map[string]any{"text": negativeText}},
		"8": {ClassType: "SaveImage", Inputs: map[string]any{"filename_prefix": "x"}},
	}
}

func bindingFor(bindings []ComfyBinding, param string) (ComfyBinding, bool) {
	for _, b := range bindings {
		if b.Param == param {
			return b, true
		}
	}
	return ComfyBinding{}, false
}

func TestAutoDetectComfyBindingsPromptAndNegative(t *testing.T) {
	tests := []struct {
		name       string
		negative   string
		wantNegID  string
		wantNoNeg  bool
		wantNegFor string
	}{
		{
			// The whole point: the wording does not say "negative".
			name:      "plain negative wording is still negative",
			negative:  "blurry, low quality, watermark",
			wantNegID: "7",
		},
		{
			name:       "explicit negative label wins",
			negative:   "negative: blurry, watermark",
			wantNegID:  "7",
			wantNegFor: "negative",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			bindings := autoDetectComfyBindings(stockSDGraph(tc.negative))
			pos, ok := bindingFor(bindings, "prompt")
			if !ok {
				t.Fatalf("no prompt binding detected, got %+v", bindings)
			}
			if pos.NodeID != "6" {
				t.Errorf("prompt node = %q, want the first encoder 6", pos.NodeID)
			}
			if pos.Kind != comfyKindString {
				t.Errorf("prompt kind = %q, want string", pos.Kind)
			}
			neg, ok := bindingFor(bindings, "negative_prompt")
			if !ok {
				t.Fatalf("no negative_prompt binding detected, got %+v", bindings)
			}
			if neg.NodeID != tc.wantNegID {
				t.Errorf("negative_prompt node = %q, want %q", neg.NodeID, tc.wantNegID)
			}
		})
	}
}

// A graph with only one encoder has no negative to expose, and a third encoder
// in the two-encoder position is not something to guess at.
func TestAutoDetectComfyBindingsSingleEncoder(t *testing.T) {
	wf := comfyui.Workflow{
		"3": {ClassType: "KSampler", Inputs: map[string]any{"seed": float64(1), "steps": float64(8)}},
		"6": {ClassType: "CLIPTextEncode", Inputs: map[string]any{"text": "a dog"}},
		"7": {ClassType: "CLIPTextEncode", Inputs: map[string]any{"text": "a cat"}},
		"8": {ClassType: "CLIPTextEncode", Inputs: map[string]any{"text": "a caption"}},
		"9": {ClassType: "SaveImage", Inputs: map[string]any{"filename_prefix": "x"}},
	}
	bindings := autoDetectComfyBindings(wf)
	if pos, ok := bindingFor(bindings, "prompt"); !ok || pos.NodeID != "6" {
		t.Errorf("prompt = %+v, want node 6", pos)
	}
	if _, ok := bindingFor(bindings, "negative_prompt"); ok {
		t.Errorf("three unlabeled encoders should not produce a negative_prompt, got %+v", bindings)
	}
}

// Node titles are the one place ComfyUI itself records the role.
func TestAutoDetectComfyBindingsTitleWins(t *testing.T) {
	wf := stockSDGraph("some text")
	n7 := wf["7"]
	n7.Title = "Negative CLIPTextEncode"
	wf["7"] = n7
	n6 := wf["6"]
	n6.Title = "Positive CLIPTextEncode"
	wf["6"] = n6

	bindings := autoDetectComfyBindings(wf)
	if pos, ok := bindingFor(bindings, "prompt"); !ok || pos.NodeID != "6" {
		t.Errorf("prompt = %+v, want node 6 (titled Positive)", pos)
	}
	if neg, ok := bindingFor(bindings, "negative_prompt"); !ok || neg.NodeID != "7" {
		t.Errorf("negative_prompt = %+v, want node 7 (titled Negative)", neg)
	}
}

// Kinds come from the JSON type already in the graph, not from a table, so a
// float64 step count stays an integer and cfg stays fractional.
func TestAutoDetectComfyBindingsKindsFromGraph(t *testing.T) {
	bindings := autoDetectComfyBindings(stockSDGraph("bad hands"))
	want := map[string]string{
		"seed": "integer", "steps": "integer",
		"width": "integer", "height": "integer",
		"cfg": "number", "denoise": "number",
	}
	for param, kind := range want {
		b, ok := bindingFor(bindings, param)
		if !ok {
			t.Errorf("param %q not detected", param)
			continue
		}
		if b.Kind != kind {
			t.Errorf("param %q kind = %q, want %q", param, b.Kind, kind)
		}
	}
}

func TestParseComfyWorkflowRejectsUIFormat(t *testing.T) {
	ui := `{"nodes":[{"id":1,"type":"KSampler"}],"links":[],"last_node_id":1,"version":0.4}`
	if _, err := parseComfyWorkflow(json.RawMessage(ui)); err == nil {
		t.Fatal("expected the visual editor format to be rejected")
	}
}

// Envelopes are what people paste by accident, so they are unwrapped.
func TestParseComfyWorkflowUnwrapsEnvelope(t *testing.T) {
	env := `{"prompt":{"3":{"class_type":"KSampler","inputs":{"steps":20}}}}`
	wf, err := parseComfyWorkflow(json.RawMessage(env))
	if err != nil {
		t.Fatalf("envelope rejected: %v", err)
	}
	if len(wf) != 1 {
		t.Fatalf("got %d nodes, want 1", len(wf))
	}
	if _, ok := wf["3"]; !ok {
		t.Fatalf("node id lost: %+v", wf)
	}
}

// An unknown parameter has to fail loudly: silently dropping it would leave the
// model believing it changed the seed.
func TestPatchComfyWorkflowRejectsUnknownParam(t *testing.T) {
	wf := stockSDGraph("bad")
	bindings := autoDetectComfyBindings(wf)
	if _, err := patchComfyWorkflow(wf, bindings, map[string]any{"seed": 99}); err != nil {
		t.Fatalf("known param rejected: %v", err)
	}
	if _, err := patchComfyWorkflow(wf, bindings, map[string]any{"sampler": 99}); err == nil {
		t.Fatal("expected an unknown param to be rejected")
	}
}
