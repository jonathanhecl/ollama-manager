package tests

import (
	"encoding/base64"
	"os"
	"path/filepath"
	"testing"
)

const sidecarTestYAML = `id: sidecar-demo
name: Sidecar Demo
group_id: mygroup
active: true
order: 0
cases:
  - name: first
    prompt: "What color is it?"
    evaluation: {type: contains, expected: "red"}
  - name: second
    prompt: "Summarize the doc."
    evaluation: {type: contains, expected: "hello"}
# Legacy test-level attachments must be ignored, not fail the load.
attachments:
  - {id: old, kind: image, name: old.png, mime: image/png, data: eA==}
`

func writeSidecarFixture(t *testing.T) (dir string) {
	t.Helper()
	dir = t.TempDir()
	groupDir := filepath.Join(dir, "mygroup")
	if err := os.MkdirAll(groupDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(groupDir, "mytest.yaml"), []byte(sidecarTestYAML), 0o644); err != nil {
		t.Fatal(err)
	}
	files := map[string]string{
		"mytest-1.png": "fakepngbytes",
		"mytest-2.txt": "hello world",
		"mytest-3.png": "out of range",
		"mytest-x.png": "bad index",
		"notes.pdf":    "unsupported",
	}
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(groupDir, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func TestSidecarDiscovery(t *testing.T) {
	store := New(writeSidecarFixture(t))
	if err := store.Load(); err != nil {
		t.Fatalf("Load: %v", err)
	}
	got, ok := store.GetTest("sidecar-demo")
	if !ok {
		t.Fatalf("test not loaded")
	}
	if len(got.Sidecars) != 0 {
		t.Fatalf("legacy test-level attachments should be ignored, got %d sidecars", len(got.Sidecars))
	}
	if len(got.Cases) != 2 {
		t.Fatalf("expected 2 cases, got %d", len(got.Cases))
	}
	atts1 := got.Cases[0].Attachments
	if len(atts1) != 1 || atts1[0].Kind != "image" || atts1[0].Name != "mytest-1.png" || atts1[0].Mime != "image/png" {
		t.Fatalf("case 1 attachments wrong: %+v", atts1)
	}
	if atts1[0].ID != "mytest-1.png" {
		t.Fatalf("case 1 attachment id wrong: %q", atts1[0].ID)
	}
	atts2 := got.Cases[1].Attachments
	if len(atts2) != 1 || atts2[0].Kind != "text" || atts2[0].Name != "mytest-2.txt" {
		t.Fatalf("case 2 attachments wrong: %+v", atts2)
	}
	raw, err := base64.StdEncoding.DecodeString(atts2[0].Data)
	if err != nil || string(raw) != "hello world" {
		t.Fatalf("case 2 text content wrong: %q, %v", string(raw), err)
	}
}

func TestSidecarSaveDelete(t *testing.T) {
	store := New(writeSidecarFixture(t))
	if err := store.Load(); err != nil {
		t.Fatalf("Load: %v", err)
	}
	// Replace case-1 png with a jpg: old file must be gone.
	att, err := store.SaveSidecar("sidecar-demo", 1, "photo.jpg", []byte("jpegbytes"))
	if err != nil {
		t.Fatalf("SaveSidecar: %v", err)
	}
	if att.Kind != "image" || att.Name != "mytest-1.jpg" {
		t.Fatalf("saved attachment wrong: %+v", att)
	}
	if _, err := os.Stat(filepath.Join(store.Dir(), "mygroup", "mytest-1.png")); !os.IsNotExist(err) {
		t.Fatalf("old sidecar was not replaced")
	}
	got, _ := store.GetTest("sidecar-demo")
	if len(got.Cases[0].Attachments) != 1 || got.Cases[0].Attachments[0].Name != "mytest-1.jpg" {
		t.Fatalf("rediscovery after save wrong: %+v", got.Cases[0].Attachments)
	}
	// Unsupported type rejected.
	if _, err := store.SaveSidecar("sidecar-demo", 1, "evil.exe", []byte("x")); err == nil {
		t.Fatalf("expected error for unsupported extension")
	}
	// Delete case-2 sidecar.
	if err := store.DeleteSidecar("sidecar-demo", 2); err != nil {
		t.Fatalf("DeleteSidecar: %v", err)
	}
	got, _ = store.GetTest("sidecar-demo")
	if len(got.Cases[1].Attachments) != 0 {
		t.Fatalf("case 2 sidecar not deleted: %+v", got.Cases[1].Attachments)
	}
	if err := store.DeleteSidecar("sidecar-demo", 2); err == nil {
		t.Fatalf("expected error deleting missing sidecar")
	}
}

func TestNamedAndStepAttachments(t *testing.T) {
	dir := t.TempDir()
	groupDir := filepath.Join(dir, "mygroup")
	if err := os.MkdirAll(groupDir, 0o755); err != nil {
		t.Fatal(err)
	}
	const namedYAML = `id: named-demo
name: Named Demo
group_id: mygroup
active: true
order: 0
cases:
  - name: Case 1
    prompt: "Prompt 1"
    attachment: "shared.png"
  - name: Case 2
    prompt: "Prompt 2"
    attachment: "shared.png"
  - name: Case 3
    steps:
      - name: Step 1
        prompt: "Step prompt"
        attachment: "step_file.txt"
`
	if err := os.WriteFile(filepath.Join(groupDir, "named.yaml"), []byte(namedYAML), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(groupDir, "shared.png"), []byte("sharedpngbytes"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(groupDir, "step_file.txt"), []byte("step text content"), 0o644); err != nil {
		t.Fatal(err)
	}

	store := New(dir)
	if err := store.Load(); err != nil {
		t.Fatalf("Load: %v", err)
	}
	test, ok := store.GetTest("named-demo")
	if !ok {
		t.Fatalf("test not found")
	}
	if len(test.Cases) != 3 {
		t.Fatalf("expected 3 cases, got %d", len(test.Cases))
	}
	// Case 1 and Case 2 both use shared.png
	if len(test.Cases[0].Attachments) != 1 || test.Cases[0].Attachments[0].Name != "shared.png" {
		t.Fatalf("case 0 attachment wrong: %+v", test.Cases[0].Attachments)
	}
	if len(test.Cases[1].Attachments) != 1 || test.Cases[1].Attachments[0].Name != "shared.png" {
		t.Fatalf("case 1 attachment wrong: %+v", test.Cases[1].Attachments)
	}
	// Case 3 step has step_file.txt
	if len(test.Cases[2].Steps) != 1 || len(test.Cases[2].Steps[0].Attachments) != 1 || test.Cases[2].Steps[0].Attachments[0].Name != "step_file.txt" {
		t.Fatalf("case 2 step attachment wrong: %+v", test.Cases[2].Steps[0].Attachments)
	}
}

