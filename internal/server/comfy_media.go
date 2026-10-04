package server

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"image"
	"image/jpeg"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// comfyMediaDir is the default root for generated files. New() overrides it with
// a path next to config.json so the media travels with the transcripts.
const comfyMediaDir = "comfyui/media"

// comfyMediaBase is the resolved storage root, set once during startup.
var comfyMediaBase = comfyMediaDir

// comfyModelImageMaxDim bounds the copy attached to the model. Vision models
// cost tokens and time per pixel, and a 1024px JPEG is plenty to judge
// composition, colour and style.
const comfyModelImageMaxDim = 1024

// comfyModelImageQuality is the JPEG quality of the copy sent to the model.
const comfyModelImageQuality = 82

// comfyMediaMaxBytes is the largest single file that will be copied into the
// manager's storage. A 4K video from a video workflow easily exceeds this, and
// the original stays in ComfyUI anyway.
const comfyMediaMaxBytes = 512 << 20

// Media kinds. "image" covers still images the model can also look at.
const (
	comfyMediaImage = "image"
	comfyMediaVideo = "video"
	comfyMediaAudio = "audio"
	comfyMediaFile  = "file"
)

// ChatMedia is one file a workflow produced, stored on disk with the transcript
// only holding the reference. Keeping the bytes out of the session JSON matters:
// sessions are rewritten in full on every stream event.
type ChatMedia struct {
	// File is the path relative to comfyui/media/, e.g. "<session>/<msg>-0.jpg".
	File string `json:"file"`
	Name string `json:"name"`
	Kind string `json:"kind"`
	Mime string `json:"mime,omitempty"`
	// Width and Height are only known for images.
	Width  int   `json:"width,omitempty"`
	Height int   `json:"height,omitempty"`
	Bytes  int64 `json:"bytes,omitempty"`
	// PromptID links the file back to its ComfyUI run so the full-resolution
	// original stays reachable.
	PromptID string `json:"prompt_id,omitempty"`
	Source   string `json:"source,omitempty"`
}

// URL is the manager route that serves the stored copy.
func (m ChatMedia) URL() string {
	if m.File == "" {
		return ""
	}
	return "/api/comfyui/media/" + strings.TrimPrefix(filepath.ToSlash(m.File), "/")
}

// comfyMediaRoot returns the absolute storage root.
func comfyMediaRoot() string {
	abs, err := filepath.Abs(comfyMediaBase)
	if err != nil {
		return comfyMediaBase
	}
	return abs
}

// resolveComfyMediaFile resolves a transcript-relative path to an absolute one,
// refusing anything that escapes the storage root.
func resolveComfyMediaFile(rel string) (string, bool) {
	rel = strings.TrimSpace(filepath.FromSlash(rel))
	if rel == "" {
		return "", false
	}
	root := comfyMediaRoot()
	abs, err := filepath.Abs(filepath.Join(root, rel))
	if err != nil {
		return "", false
	}
	if abs != root && !strings.HasPrefix(abs, root+string(os.PathSeparator)) {
		return "", false
	}
	return abs, true
}

// normalizeComfyMime strips parameters and case from a Content-Type.
func normalizeComfyMime(v string) string {
	if v == "" {
		return ""
	}
	if i := strings.IndexByte(v, ';'); i >= 0 {
		v = v[:i]
	}
	return strings.ToLower(strings.TrimSpace(v))
}

// classifyComfyMedia decides what a produced file is and which mime type the
// chat will serve it under.
//
// The bytes are sniffed first, because both other signals can lie: ComfyUI
// serves whatever type the filename implies, and animated video arrives under
// the "gifs" key with a .webp name, which a naive extension check would treat
// as a still image even though it has to play as a video.
func classifyComfyMedia(filename string, contentType string, data []byte) (kind, mime string) {
	// The bytes win over the declared header. ComfyUI serves whatever type the
	// filename implies, so "ComfyUI_00001_.png" arrives labelled image/png even
	// when the render is a JPEG, and a copy stored under the wrong type shows up
	// as a broken image in the chat.
	sniffed := normalizeComfyMime(http.DetectContentType(data))
	if sniffed == "" || sniffed == "application/octet-stream" {
		// Containers carry no signature Go recognizes, so the declared type and
		// then the extension are what identify video and audio.
		sniffed = normalizeComfyMime(contentType)
	}

	switch {
	case sniffed == "image/webp":
		if isAnimatedWebP(data) {
			return comfyMediaVideo, "image/webp"
		}
		return comfyMediaImage, "image/webp"
	case strings.HasPrefix(sniffed, "image/"):
		return comfyMediaImage, sniffed
	case strings.HasPrefix(sniffed, "video/"):
		return comfyMediaVideo, sniffed
	case strings.HasPrefix(sniffed, "audio/"):
		return comfyMediaAudio, sniffed
	}
	// http.DetectContentType does not know .mov or .mkv containers.
	switch strings.ToLower(filepath.Ext(filename)) {
	case ".png":
		return comfyMediaImage, "image/png"
	case ".jpg", ".jpeg":
		return comfyMediaImage, "image/jpeg"
	case ".gif":
		return comfyMediaImage, "image/gif"
	case ".webp":
		return comfyMediaVideo, "image/webp"
	case ".mp4":
		return comfyMediaVideo, "video/mp4"
	case ".webm":
		return comfyMediaVideo, "video/webm"
	case ".mov":
		return comfyMediaVideo, "video/quicktime"
	case ".mkv":
		return comfyMediaVideo, "video/x-matroska"
	case ".wav":
		return comfyMediaAudio, "audio/wav"
	case ".mp3":
		return comfyMediaAudio, "audio/mpeg"
	case ".flac":
		return comfyMediaAudio, "audio/flac"
	case ".ogg":
		return comfyMediaAudio, "audio/ogg"
	case ".m4a":
		return comfyMediaAudio, "audio/mp4"
	}
	if sniffed != "" && sniffed != "application/octet-stream" {
		return comfyMediaFile, sniffed
	}
	return comfyMediaFile, "application/octet-stream"
}

// isAnimatedWebP reports whether a RIFF/WEBP file carries the ANIM chunk, which
// makes it a video rather than a still image.
func isAnimatedWebP(data []byte) bool {
	if len(data) < 30 {
		return false
	}
	if !bytes.Equal(data[0:4], []byte("RIFF")) || !bytes.Equal(data[8:12], []byte("WEBP")) {
		return false
	}
	// An animated file always gets a VP8X container; a still VP8/VP8L does not.
	return bytes.Equal(data[12:16], []byte("VP8X")) && bytes.Contains(data[:min(len(data), 4096)], []byte("ANIM"))
}

// comfyMediaExt picks a safe extension for the stored file.
func comfyMediaExt(kind, mime, filename string) string {
	if ext := strings.ToLower(filepath.Ext(filename)); ext != "" && len(ext) <= 6 {
		if isAlnumExt(ext[1:]) {
			return ext
		}
	}
	switch kind {
	case comfyMediaImage:
		switch mime {
		case "image/png":
			return ".png"
		case "image/gif":
			return ".gif"
		case "image/webp":
			return ".webp"
		default:
			return ".jpg"
		}
	case comfyMediaVideo:
		switch mime {
		case "video/webm":
			return ".webm"
		case "video/quicktime":
			return ".mov"
		case "video/x-matroska":
			return ".mkv"
		case "image/webp":
			return ".webp"
		default:
			return ".mp4"
		}
	case comfyMediaAudio:
		switch mime {
		case "audio/mpeg":
			return ".mp3"
		case "audio/flac":
			return ".flac"
		case "audio/ogg":
			return ".ogg"
		case "audio/mp4":
			return ".m4a"
		default:
			return ".wav"
		}
	default:
		return ".bin"
	}
}

func isAlnumExt(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if !(r >= 'a' && r <= 'z') && !(r >= '0' && r <= '9') {
			return false
		}
	}
	return true
}

// saveComfyMedia copies a produced file into the manager's storage. Images are
// downscaled before being written, because that same copy is what gets attached
// to the model and inlined in a restored session. Everything else is stored
// byte for byte, since re-encoding a compressed video or an audio track would
// only lose quality.
func saveComfyMedia(sessionID string, index int, filename, contentType string, data []byte) (ChatMedia, error) {
	if len(data) == 0 {
		return ChatMedia{}, fmt.Errorf("empty file")
	}
	if int64(len(data)) > comfyMediaMaxBytes {
		return ChatMedia{}, fmt.Errorf("file is %d MB, above the %d MB limit", len(data)>>20, comfyMediaMaxBytes>>20)
	}

	kind, mime := classifyComfyMedia(filename, contentType, data)
	// The stored copy is what the user downloads from the chat, so it is the file
	// ComfyUI produced, byte for byte. The re-encoded 1024px JPEG the model gets
	// is derived on demand in comfyModelImageFromMedia instead of being written
	// over the user's result.
	ext := comfyMediaExt(kind, mime, filename)
	dir := filepath.Join(comfyMediaRoot(), sanitizeMediaSegment(sessionID))
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return ChatMedia{}, err
	}
	name := fmt.Sprintf("%d-%d%s", time.Now().UnixMilli(), index, ext)
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, data, 0o600); err != nil {
		return ChatMedia{}, err
	}

	media := ChatMedia{
		File:   filepath.ToSlash(filepath.Join(sanitizeMediaSegment(sessionID), name)),
		Name:   filepath.Base(filename),
		Kind:   kind,
		Mime:   mime,
		Bytes:  int64(len(data)),
		Source: "comfyui",
	}
	if media.Name == "" || media.Name == "." {
		media.Name = name
	}
	if cfg, _, err := image.DecodeConfig(bytes.NewReader(data)); err == nil {
		media.Width, media.Height = cfg.Width, cfg.Height
	}
	return media, nil
}

func sanitizeMediaSegment(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return "unassigned"
	}
	var b strings.Builder
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_':
			b.WriteRune(r)
		default:
			b.WriteByte('_')
		}
	}
	return b.String()
}

// removeComfyMediaForSession deletes every file a session generated. It is
// called when a session is deleted so the storage does not outlive the
// transcript that references it.
func removeComfyMediaForSession(sessionID string) {
	dir := filepath.Join(comfyMediaRoot(), sanitizeMediaSegment(sessionID))
	if dir == "" {
		return
	}
	_ = os.RemoveAll(dir)
}

// sweepOrphanComfyMedia deletes media folders whose session no longer exists.
// Sessions can be removed through the API or by hand, and the file names only
// record the timestamp, not the session, so this needs the live id list.
func sweepOrphanComfyMedia(live map[string]bool) {
	root := comfyMediaRoot()
	entries, err := os.ReadDir(root)
	if err != nil {
		return
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		if live[e.Name()] {
			continue
		}
		_ = os.RemoveAll(filepath.Join(root, e.Name()))
	}
}

// comfyModelImageFromMedia loads a stored image and returns the base64 payload
// (no data URL prefix) for a tool message, plus its pixel dimensions. It is used
// when a restored session replays earlier results back to the model.
func comfyModelImageFromMedia(media ChatMedia, maxDim int) (string, int, int, bool) {
	if media.Kind != comfyMediaImage || media.File == "" {
		return "", 0, 0, false
	}
	path, ok := resolveComfyMediaFile(media.File)
	if !ok {
		return "", 0, 0, false
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return "", 0, 0, false
	}
	cfg, _, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return "", 0, 0, false
	}
	if maxDim <= 0 {
		maxDim = comfyModelImageMaxDim
	}
	if resized, _, ok := resizeForComfyModel(data); ok {
		data = resized
	}
	return base64.StdEncoding.EncodeToString(data), cfg.Width, cfg.Height, true
}

// resizeForComfyModel downscales an image so its longest side is at most
// comfyModelImageMaxDim and re-encodes it as JPEG. It reports false for formats
// the standard library cannot decode, which is the caller's signal to skip the
// model copy instead of failing the run.
func resizeForComfyModel(data []byte) ([]byte, string, bool) {
	src, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, "", false
	}
	b := src.Bounds()
	if b.Dx() <= 0 || b.Dy() <= 0 {
		return nil, "", false
	}
	if max(b.Dx(), b.Dy()) <= comfyModelImageMaxDim {
		// Already small enough; keep it as-is when it is already a format the
		// model path accepts without re-encoding.
		if isJPEGFriendly(data) {
			return data, "image/jpeg", true
		}
	}
	scaled := resizeImage(src, comfyModelImageMaxDim)
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, scaled, &jpeg.Options{Quality: comfyModelImageQuality}); err != nil {
		return nil, "", false
	}
	return buf.Bytes(), "image/jpeg", true
}

func isJPEGFriendly(data []byte) bool {
	return len(data) > 3 && data[0] == 0xFF && data[1] == 0xD8 && data[2] == 0xFF
}

// resizeImage scales src so its longest side is maxDim, using bilinear
// interpolation. The standard library ships no resampler, and this only ever
// feeds a vision model and a chat thumbnail, where bilinear is indistinguishable
// from the alternatives.
func resizeImage(src image.Image, maxDim int) *image.RGBA {
	b := src.Bounds()
	w, h := b.Dx(), b.Dy()
	if w <= 0 || h <= 0 || (w <= maxDim && h <= maxDim) {
		rgba := image.NewRGBA(image.Rect(0, 0, w, h))
		drawInto(rgba, src)
		return rgba
	}
	scale := float64(maxDim) / float64(max(w, h))
	dw := int(float64(w)*scale + 0.5)
	dh := int(float64(h)*scale + 0.5)
	if dw < 1 {
		dw = 1
	}
	if dh < 1 {
		dh = 1
	}
	dst := image.NewRGBA(image.Rect(0, 0, dw, dh))
	srcX0, srcY0 := float64(b.Min.X), float64(b.Min.Y)
	xRatio := float64(w-1) / float64(max(dw-1, 1))
	yRatio := float64(h-1) / float64(max(dh-1, 1))

	for y := 0; y < dh; y++ {
		fy := float64(y) * yRatio
		y0 := int(fy)
		y1 := min(y0+1, h-1)
		wy := fy - float64(y0)
		for x := 0; x < dw; x++ {
			fx := float64(x) * xRatio
			x0 := int(fx)
			x1 := min(x0+1, w-1)
			wx := fx - float64(x0)

			c00 := srcRGBAAt(src, srcX0+float64(x0), srcY0+float64(y0))
			c10 := srcRGBAAt(src, srcX0+float64(x1), srcY0+float64(y0))
			c01 := srcRGBAAt(src, srcX0+float64(x0), srcY0+float64(y1))
			c11 := srcRGBAAt(src, srcX0+float64(x1), srcY0+float64(y1))

			o := dst.PixOffset(x, y)
			for i := 0; i < 4; i++ {
				top := float64(c00[i])*(1-wx) + float64(c10[i])*wx
				bot := float64(c01[i])*(1-wx) + float64(c11[i])*wx
				v := top*(1-wy) + bot*wy
				if v < 0 {
					v = 0
				}
				if v > 255 {
					v = 255
				}
				dst.Pix[o+i] = uint8(v + 0.5)
			}
		}
	}
	return dst
}

func drawInto(dst *image.RGBA, src image.Image) {
	b := src.Bounds()
	for y := 0; y < b.Dy(); y++ {
		for x := 0; x < b.Dx(); x++ {
			r, g, bb, a := src.At(b.Min.X+x, b.Min.Y+y).RGBA()
			o := dst.PixOffset(x, y)
			dst.Pix[o+0] = uint8(r >> 8)
			dst.Pix[o+1] = uint8(g >> 8)
			dst.Pix[o+2] = uint8(bb >> 8)
			dst.Pix[o+3] = uint8(a >> 8)
		}
	}
}

func srcRGBAAt(src image.Image, x, y float64) [4]float64 {
	b := src.Bounds()
	xi := int(x)
	yi := int(y)
	if xi < b.Min.X {
		xi = b.Min.X
	}
	if yi < b.Min.Y {
		yi = b.Min.Y
	}
	if xi > b.Max.X-1 {
		xi = b.Max.X - 1
	}
	if yi > b.Max.Y-1 {
		yi = b.Max.Y - 1
	}
	r, g, bb, a := src.At(xi, yi).RGBA()
	return [4]float64{float64(r >> 8), float64(g >> 8), float64(bb >> 8), float64(a >> 8)}
}

// describeComfyMedia renders one output for the model. The model must never be
// left guessing what a video contains, so video and audio results say plainly
// that they were shown to the user and are invisible to it.
func describeComfyMedia(m ChatMedia) string {
	size := fmt.Sprintf("%.1f MB", float64(m.Bytes)/(1<<20))
	if m.Bytes < 1<<20 {
		size = fmt.Sprintf("%d KB", m.Bytes>>10)
	}
	switch m.Kind {
	case comfyMediaImage:
		if m.Width > 0 && m.Height > 0 {
			return fmt.Sprintf("image (%dx%d, %s)", m.Width, m.Height, size)
		}
		return "image (" + size + ")"
	case comfyMediaVideo:
		return "video (" + size + ")"
	case comfyMediaAudio:
		return "audio (" + size + ")"
	default:
		return "file (" + size + ")"
	}
}
