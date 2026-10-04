package server

import (
	"bytes"
	"encoding/base64"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func testPNG(t *testing.T, w, h int) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.Set(x, y, color.RGBA{R: uint8(x), G: uint8(y), B: 128, A: 255})
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// withComfyMediaBase points storage at a temp dir for the duration of a test.
func withComfyMediaBase(t *testing.T, fn func()) {
	t.Helper()
	prev := comfyMediaBase
	comfyMediaBase = t.TempDir()
	t.Cleanup(func() { comfyMediaBase = prev })
	fn()
}

// A big render is stored whole, because the user downloads it from the chat.
func TestSaveComfyMediaKeepsOriginalBytes(t *testing.T) {
	withComfyMediaBase(t, func() {
		orig := testPNG(t, 600, 400)
		m, err := saveComfyMedia("sess", 0, "ComfyUI_00001_.png", "", orig)
		if err != nil {
			t.Fatal(err)
		}
		if m.Kind != comfyMediaImage {
			t.Errorf("kind = %q, want image", m.Kind)
		}
		if m.Mime != "image/png" {
			t.Errorf("mime = %q, want image/png", m.Mime)
		}
		if m.Width != 600 || m.Height != 400 {
			t.Errorf("size = %dx%d, want 600x400", m.Width, m.Height)
		}
		path, ok := resolveComfyMediaFile(m.File)
		if !ok {
			t.Fatalf("stored file %q does not resolve", m.File)
		}
		got, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(got, orig) {
			t.Errorf("stored file was re-encoded: %d bytes in, %d out", len(orig), len(got))
		}
	})
}

// What the model sees is capped, whatever was rendered.
func TestComfyModelImageIsCapped(t *testing.T) {
	withComfyMediaBase(t, func() {
		m, err := saveComfyMedia("sess", 0, "big.png", "", testPNG(t, 2048, 2048))
		if err != nil {
			t.Fatal(err)
		}
		b64, w, h, ok := comfyModelImageFromMedia(m, comfyModelImageMaxDim)
		if !ok {
			t.Fatal("expected a model copy")
		}
		// The reported size is the original's, so the transcript keeps the truth.
		if w != 2048 || h != 2048 {
			t.Errorf("reported size = %dx%d, want 2048x2048", w, h)
		}
		raw, err := base64.StdEncoding.DecodeString(b64)
		if err != nil {
			t.Fatalf("model copy is not valid base64: %v", err)
		}
		if strings.HasPrefix(b64, "data:") {
			t.Error("model copy must not carry a data URL prefix")
		}
		cfg, format, err := image.DecodeConfig(bytes.NewReader(raw))
		if err != nil {
			t.Fatal(err)
		}
		if format != "jpeg" {
			t.Errorf("model copy format = %q, want jpeg", format)
		}
		if cfg.Width != comfyModelImageMaxDim || cfg.Height != comfyModelImageMaxDim {
			t.Errorf("model copy is %dx%d, want it capped to %d", cfg.Width, cfg.Height, comfyModelImageMaxDim)
		}
	})
}

// An image already inside the cap is still re-encoded, so a PNG never reaches a
// vision model as a multi-megabyte base64 blob.
func TestComfyModelImageReencodesSmallImages(t *testing.T) {
	withComfyMediaBase(t, func() {
		m, err := saveComfyMedia("sess", 0, "small.png", "", testPNG(t, 128, 96))
		if err != nil {
			t.Fatal(err)
		}
		b64, _, _, ok := comfyModelImageFromMedia(m, comfyModelImageMaxDim)
		if !ok {
			t.Fatal("expected a model copy")
		}
		raw, err := base64.StdEncoding.DecodeString(b64)
		if err != nil {
			t.Fatal(err)
		}
		if _, format, err := image.DecodeConfig(bytes.NewReader(raw)); err != nil {
			t.Fatal(err)
		} else if format != "jpeg" {
			t.Errorf("format = %q, want jpeg", format)
		}
	})
}

// Animated WebP cannot be decoded by the standard library, so it is shown and
// stored but never handed to the model.
func TestAnimatedWebPIsVideoAndNotSentToModel(t *testing.T) {
	// A minimal RIFF/WEBP container carrying the ANIM chunk.
	var buf bytes.Buffer
	buf.WriteString("RIFF")
	buf.Write([]byte{0x20, 0, 0, 0})
	buf.WriteString("WEBPVP8X")
	buf.Write([]byte{0x0a, 0, 0, 0})
	buf.Write([]byte{0x10, 0, 0, 0, 0, 0, 0, 0})
	buf.WriteString("ANIM")
	buf.Write([]byte{0x10, 0, 0, 0})
	data := buf.Bytes()

	kind, mime := classifyComfyMedia("render.webp", "image/webp", data)
	if kind != comfyMediaVideo {
		t.Errorf("kind = %q, want video", kind)
	}
	if !strings.Contains(mime, "webp") {
		t.Errorf("mime = %q, want a webp type", mime)
	}

	withComfyMediaBase(t, func() {
		m, err := saveComfyMedia("sess", 0, "render.webp", "image/webp", data)
		if err != nil {
			t.Fatal(err)
		}
		if _, _, _, ok := comfyModelImageFromMedia(m, comfyModelImageMaxDim); ok {
			t.Error("an undecodable image must not be attached to the model")
		}
	})
}

// Video and audio arrive under outputs[*].gifs, not images[].
func TestClassifyComfyMediaByExtension(t *testing.T) {
	cases := []struct {
		name string
		file string
		mime string
		want string
	}{
		{"video", "clip.mp4", "video/mp4", comfyMediaVideo},
		{"audio", "track.wav", "audio/wave", comfyMediaAudio},
		{"image", "still.png", "image/png", comfyMediaImage},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			kind, _ := classifyComfyMedia(tc.file, tc.mime, nil)
			if kind != tc.want {
				t.Errorf("kind = %q, want %q", kind, tc.want)
			}
		})
	}
}

func TestResolveComfyMediaFileRefusesEscape(t *testing.T) {
	withComfyMediaBase(t, func() {
		root := comfyMediaRoot()
		for _, rel := range []string{"../../etc/passwd", "a/../../../x", "../config.json"} {
			abs, ok := resolveComfyMediaFile(rel)
			if ok && (abs != root && !strings.HasPrefix(abs, root+string(os.PathSeparator))) {
				t.Errorf("%q resolved to %q, outside %q", rel, abs, root)
			}
		}
		if _, ok := resolveComfyMediaFile(""); ok {
			t.Error("an empty path should not resolve")
		}
		if _, ok := resolveComfyMediaFile("   "); ok {
			t.Error("a blank path should not resolve")
		}
		// A leading slash is treated as relative to the storage root rather than
		// as an absolute path, so it cannot reach the real /etc.
		abs, ok := resolveComfyMediaFile("/etc/passwd")
		if ok && strings.Contains(abs, "/etc/passwd") && !strings.HasPrefix(abs, root) {
			t.Errorf("/etc/passwd resolved outside the storage root: %q", abs)
		}
	})
}

func TestRemoveComfyMediaForSessionOnlyTouchesItsOwn(t *testing.T) {
	withComfyMediaBase(t, func() {
		a, err := saveComfyMedia("keep", 0, "a.png", "", testPNG(t, 8, 8))
		if err != nil {
			t.Fatal(err)
		}
		b, err := saveComfyMedia("drop", 0, "b.png", "", testPNG(t, 8, 8))
		if err != nil {
			t.Fatal(err)
		}
		removeComfyMediaForSession("drop")
		if _, err := os.Stat(filepath.Join(comfyMediaBase, "drop", filepath.Base(b.File))); !os.IsNotExist(err) {
			t.Error("the dropped session's file survived")
		}
		if _, err := os.Stat(filepath.Join(comfyMediaBase, "keep", filepath.Base(a.File))); err != nil {
			t.Errorf("another session's file was deleted: %v", err)
		}
	})
}

// The sweep runs at startup and must not delete media a live session references.
func TestSweepOrphanComfyMediaKeepsLiveFiles(t *testing.T) {
	withComfyMediaBase(t, func() {
		live, err := saveComfyMedia("live", 0, "a.png", "", testPNG(t, 8, 8))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := saveComfyMedia("gone", 0, "b.png", "", testPNG(t, 8, 8)); err != nil {
			t.Fatal(err)
		}
		sweepOrphanComfyMedia(map[string]bool{"live": true, live.File: true})
		if _, err := os.Stat(filepath.Join(comfyMediaBase, "live", filepath.Base(live.File))); err != nil {
			t.Errorf("live media was swept: %v", err)
		}
		if _, err := os.Stat(filepath.Join(comfyMediaBase, "gone")); !os.IsNotExist(err) {
			t.Error("orphan media survived the sweep")
		}
	})
}

// A JPEG from ComfyUI must be recognised even when the filename says png.
func TestClassifyComfyMediaTrustsContentOverExtension(t *testing.T) {
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, image.NewRGBA(image.Rect(0, 0, 4, 4)), nil); err != nil {
		t.Fatal(err)
	}
	kind, mime := classifyComfyMedia("ComfyUI_00001_.png", "image/png", buf.Bytes())
	if kind != comfyMediaImage {
		t.Fatalf("kind = %q, want image", kind)
	}
	if mime != "image/jpeg" {
		t.Errorf("mime = %q, want image/jpeg from the content", mime)
	}
}
