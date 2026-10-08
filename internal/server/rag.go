package server

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"os"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/gense/ollama-manager/internal/ollama"
	"github.com/gense/ollama-manager/internal/rag"
)

const (
	ragCreateTimeout = 10 * time.Minute
	ragMaxBody       = 24 << 20
	ragMaxEntries    = 100
	ragMaxTermLen    = 256
	ragMaxNameLen    = 256
	ragMaxDescLen    = 8192
	ragMaxContentLen = 32000
	ragMaxMediaName  = 256
	ragMaxMediaBytes = 8 << 20
	ragMaxMediaTotal = 16 << 20
)

type ragEntryBody struct {
	Term        string `json:"term"`
	Content     string `json:"content"`
	MediaType   string `json:"media_type"`
	MediaName   string `json:"media_name"`
	MediaMIME   string `json:"media_mime"`
	MediaBase64 string `json:"media_base64"`
}

type ragCreateBody struct {
	Name           string         `json:"name"`
	Description    string         `json:"description"`
	EmbeddingModel string         `json:"embedding_model"`
	Entries        []ragEntryBody `json:"entries"`
}

func ragModelCaps(show *ollama.ShowResponse) []string {
	return withoutProjectorCaps(show.Capabilities, len(show.ProjectorInfo) > 0, show.Details.Format)
}

func findInstalledModel(models []ollama.Model, name string) (ollama.Model, bool) {
	for _, m := range models {
		if m.Name == name {
			return m, true
		}
	}
	for _, m := range models {
		if m.Name == name+":latest" || strings.TrimSuffix(m.Name, ":latest") == name {
			return m, true
		}
	}
	return ollama.Model{}, false
}

var ragMimeAliases = map[string][]string{
	"image/jpeg": {"image/jpg", "image/pjpeg"},
	"audio/mpeg": {"audio/mp3", "audio/mpeg3", "audio/x-mpeg"},
	"audio/wav":  {"audio/x-wav", "audio/wave", "audio/vnd.wave"},
	"audio/ogg":  {"application/ogg", "audio/x-ogg"},
	"audio/webm": {"video/webm"},
	"audio/mp4":  {"audio/m4a", "audio/x-m4a"},
	"audio/flac": {"audio/x-flac"},
}

func normalizeMime(m string) string {
	m = strings.ToLower(strings.TrimSpace(m))
	if i := strings.IndexByte(m, ';'); i >= 0 {
		m = strings.TrimSpace(m[:i])
	}
	return m
}

func mimeMatches(detected, declared string) bool {
	if declared == "" {
		return true
	}
	if declared == detected {
		return true
	}
	for _, alias := range ragMimeAliases[detected] {
		if declared == alias {
			return true
		}
	}
	return false
}

func detectRAGMediaMIME(declared string, raw []byte, declaredMime string) (string, error) {
	detected := normalizeMime(http.DetectContentType(raw))
	switch detected {
	case "application/ogg":
		detected = "audio/ogg"
	case "video/webm":
		detected = "audio/webm"
	}
	allowed := false
	switch declared {
	case "image":
		switch detected {
		case "image/png", "image/jpeg", "image/gif", "image/webp", "image/bmp":
			allowed = true
		}
	case "audio":
		switch detected {
		case "audio/wav", "audio/x-wav", "audio/wave", "audio/mpeg", "audio/mp3",
			"audio/ogg", "audio/webm", "audio/mp4", "audio/m4a", "audio/flac":
			allowed = true
		}
	}
	if !allowed {
		return "", fmt.Errorf("unsupported or mismatched %s media (detected %q)", declared, detected)
	}
	if declaredMime != "" && !mimeMatches(detected, normalizeMime(declaredMime)) {
		return "", fmt.Errorf("declared media_mime %q does not match detected %q", declaredMime, detected)
	}
	return detected, nil
}

func (s *Server) ragConfigSnapshot() (dir string, defaultModel string) {
	s.cfgMu.RLock()
	defer s.cfgMu.RUnlock()
	return s.cfg.RAGDirectory(), strings.TrimSpace(s.cfg.RAG.DefaultEmbedding)
}

func (s *Server) handleListRAGs(w http.ResponseWriter, r *http.Request) {
	dir, _ := s.ragConfigSnapshot()
	rags, warnings, err := rag.List(dir)
	if err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Errorf("could not read rag directory: %w", err))
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"rags":      rags,
		"directory": dir,
		"warnings":  warnings,
	})
}

func (s *Server) handleGetRAG(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if !rag.ValidFilename(id) {
		writeError(w, http.StatusBadRequest, errors.New("invalid base name"))
		return
	}
	dir, _ := s.ragConfigSnapshot()
	detail, err := rag.Get(dir, id)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			writeError(w, http.StatusNotFound, errors.New("rag base not found"))
			return
		}
		writeError(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, detail)
}

func (s *Server) handleRAGModels(w http.ResponseWriter, r *http.Request) {
	dir, defaultModel := s.ragConfigSnapshot()
	ctx, cancel := context.WithTimeout(r.Context(), 60*time.Second)
	defer cancel()

	models, err := s.ollama.List(ctx)
	if err != nil {
		writeError(w, http.StatusBadGateway, fmt.Errorf("could not list models: %w", err))
		return
	}
	type ragModel struct {
		Name         string   `json:"name"`
		Digest       string   `json:"digest"`
		Capabilities []string `json:"capabilities"`
	}
	out := []ragModel{}
	warnings := []string{}
	for _, m := range models {
		show, err := s.ollama.Show(ctx, m.Name)
		if err != nil {
			warnings = append(warnings, fmt.Sprintf("%s: %v", m.Name, err))
			continue
		}
		caps := ragModelCaps(show)
		if !hasCapability(caps, "embedding") {
			continue
		}
		out = append(out, ragModel{Name: m.Name, Digest: m.Digest, Capabilities: caps})
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"models":            out,
		"default_embedding": defaultModel,
		"directory":         dir,
		"warnings":          warnings,
	})
}

func (s *Server) handleCreateRAG(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, ragMaxBody)
	var body ragCreateBody
	dec := json.NewDecoder(r.Body)
	if err := dec.Decode(&body); err != nil {
		var mbErr *http.MaxBytesError
		if errors.As(err, &mbErr) {
			writeError(w, http.StatusRequestEntityTooLarge, errors.New("request body too large"))
			return
		}
		writeError(w, http.StatusBadRequest, fmt.Errorf("invalid body: %w", err))
		return
	}
	if err := dec.Decode(&struct{}{}); err != io.EOF {
		writeError(w, http.StatusBadRequest, errors.New("invalid body: trailing data"))
		return
	}

	dir, defaultModel := s.ragConfigSnapshot()

	name := strings.TrimSpace(body.Name)
	if name == "" {
		writeError(w, http.StatusBadRequest, errors.New("name is required"))
		return
	}
	if utf8.RuneCountInString(name) > ragMaxNameLen {
		writeError(w, http.StatusBadRequest, fmt.Errorf("name must be at most %d characters", ragMaxNameLen))
		return
	}
	desc := strings.TrimSpace(body.Description)
	if utf8.RuneCountInString(desc) > ragMaxDescLen {
		writeError(w, http.StatusBadRequest, fmt.Errorf("description must be at most %d characters", ragMaxDescLen))
		return
	}
	if len(body.Entries) == 0 {
		writeError(w, http.StatusBadRequest, errors.New("at least one entry is required"))
		return
	}
	if len(body.Entries) > ragMaxEntries {
		writeError(w, http.StatusBadRequest, fmt.Errorf("at most %d entries per base", ragMaxEntries))
		return
	}

	modelName := strings.TrimSpace(body.EmbeddingModel)
	if modelName == "" {
		modelName = defaultModel
	}
	if modelName == "" {
		writeError(w, http.StatusBadRequest, errors.New("no embedding model selected; set a default in Settings"))
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), ragCreateTimeout)
	defer cancel()

	models, err := s.ollama.List(ctx)
	if err != nil {
		writeError(w, http.StatusBadGateway, fmt.Errorf("could not list models: %w", err))
		return
	}
	installed, ok := findInstalledModel(models, modelName)
	if !ok {
		writeError(w, http.StatusBadRequest, fmt.Errorf("embedding model %q is not installed", modelName))
		return
	}
	show, err := s.ollama.Show(ctx, installed.Name)
	if err != nil {
		writeError(w, http.StatusBadGateway, fmt.Errorf("could not inspect model %q: %w", installed.Name, err))
		return
	}
	caps := ragModelCaps(show)
	if !hasCapability(caps, "embedding") {
		writeError(w, http.StatusBadRequest, fmt.Errorf("model %q does not report the embedding capability", installed.Name))
		return
	}

	type preparedEntry struct {
		term      string
		content   string
		text      string
		mediaType string
		mediaName string
		mediaMIME string
		mediaB64  string
		media     []byte
	}
	prepared := make([]preparedEntry, 0, len(body.Entries))
	var totalMedia int
	for i, e := range body.Entries {
		term := strings.TrimSpace(e.Term)
		if term == "" {
			writeError(w, http.StatusBadRequest, fmt.Errorf("entry %d: term is required", i+1))
			return
		}
		if utf8.RuneCountInString(term) > ragMaxTermLen {
			writeError(w, http.StatusBadRequest, fmt.Errorf("entry %d: term must be at most %d characters", i+1, ragMaxTermLen))
			return
		}
		content := strings.TrimSpace(e.Content)
		if utf8.RuneCountInString(content) > ragMaxContentLen {
			writeError(w, http.StatusBadRequest, fmt.Errorf("entry %d: content must be at most %d characters", i+1, ragMaxContentLen))
			return
		}
		mediaName := strings.TrimSpace(e.MediaName)
		if utf8.RuneCountInString(mediaName) > ragMaxMediaName {
			writeError(w, http.StatusBadRequest, fmt.Errorf("entry %d: media_name must be at most %d characters", i+1, ragMaxMediaName))
			return
		}
		mediaType := strings.ToLower(strings.TrimSpace(e.MediaType))
		mediaB64 := strings.TrimSpace(e.MediaBase64)
		declaredMime := strings.TrimSpace(e.MediaMIME)
		switch mediaType {
		case "", "text":
			if mediaB64 != "" {
				writeError(w, http.StatusBadRequest, fmt.Errorf("entry %d: text entries cannot carry media", i+1))
				return
			}
			if declaredMime != "" || mediaName != "" {
				writeError(w, http.StatusBadRequest, fmt.Errorf("entry %d: media fields require a media payload", i+1))
				return
			}
			if content == "" {
				writeError(w, http.StatusBadRequest, fmt.Errorf("entry %d: content is required for text entries", i+1))
				return
			}
			prepared = append(prepared, preparedEntry{
				term: term, content: content, mediaType: "text",
				text: term + "\n\n" + content,
			})
		case "image", "audio":
			if mediaB64 == "" {
				writeError(w, http.StatusBadRequest, fmt.Errorf("entry %d: %s entries require media_base64", i+1, mediaType))
				return
			}
			needCap := "vision"
			if mediaType == "audio" {
				needCap = "audio"
			}
			if !hasCapability(caps, needCap) {
				writeError(w, http.StatusBadRequest, fmt.Errorf("entry %d: model %q lacks the %s capability for %s media", i+1, installed.Name, needCap, mediaType))
				return
			}
			raw, err := base64.StdEncoding.DecodeString(mediaB64)
			if err != nil {
				writeError(w, http.StatusBadRequest, fmt.Errorf("entry %d: invalid media_base64: %v", i+1, err))
				return
			}
			if len(raw) == 0 {
				writeError(w, http.StatusBadRequest, fmt.Errorf("entry %d: media is empty", i+1))
				return
			}
			if len(raw) > ragMaxMediaBytes {
				writeError(w, http.StatusBadRequest, fmt.Errorf("entry %d: media exceeds %d MiB", i+1, ragMaxMediaBytes>>20))
				return
			}
			totalMedia += len(raw)
			if totalMedia > ragMaxMediaTotal {
				writeError(w, http.StatusBadRequest, fmt.Errorf("media exceeds %d MiB in total", ragMaxMediaTotal>>20))
				return
			}
			mime, err := detectRAGMediaMIME(mediaType, raw, declaredMime)
			if err != nil {
				writeError(w, http.StatusBadRequest, fmt.Errorf("entry %d: %v", i+1, err))
				return
			}
			prepared = append(prepared, preparedEntry{
				term: term, content: content, text: term + "\n\n" + content,
				mediaType: mediaType, mediaName: mediaName,
				mediaMIME: mime, mediaB64: mediaB64, media: raw,
			})
		default:
			writeError(w, http.StatusBadRequest, fmt.Errorf("entry %d: unsupported media_type %q", i+1, e.MediaType))
			return
		}
	}

	storeEntries := make([]rag.Entry, 0, len(prepared))
	dims := 0
	for i, pe := range prepared {
		var input any = pe.text
		if pe.mediaType == "image" || pe.mediaType == "audio" {
			item := map[string]any{"text": pe.text}
			if pe.mediaType == "image" {
				item["image"] = pe.mediaB64
			} else {
				item["audio"] = pe.mediaB64
			}
			input = []map[string]any{item}
		}
		resp, err := s.ollama.Embed(ctx, installed.Name, input)
		if err != nil {
			if ctx.Err() != nil {
				writeError(w, http.StatusGatewayTimeout, errors.New("embedding timed out or was cancelled"))
				return
			}
			writeError(w, http.StatusBadGateway, fmt.Errorf("embedding entry %d failed: %w", i+1, err))
			return
		}
		vec := resp.Embedding
		if len(vec) == 0 {
			writeError(w, http.StatusBadGateway, fmt.Errorf("embedding entry %d returned an empty vector", i+1))
			return
		}
		for _, f := range vec {
			if math.IsNaN(f) || math.IsInf(f, 0) {
				writeError(w, http.StatusBadGateway, fmt.Errorf("embedding entry %d returned a non-finite vector", i+1))
				return
			}
		}
		if dims == 0 {
			dims = len(vec)
		} else if len(vec) != dims {
			writeError(w, http.StatusBadGateway, fmt.Errorf("entry %d embedding has %d dims, want %d", i+1, len(vec), dims))
			return
		}
		storeEntries = append(storeEntries, rag.Entry{
			Term:      pe.term,
			Content:   pe.content,
			MediaType: pe.mediaType,
			MediaName: pe.mediaName,
			MediaMIME: pe.mediaMIME,
			Media:     pe.media,
			Embedding: vec,
		})
	}
	if err := ctx.Err(); err != nil {
		writeError(w, http.StatusGatewayTimeout, errors.New("embedding timed out or was cancelled"))
		return
	}

	meta, filename, err := rag.Create(ctx, dir, rag.Meta{
		Name:            name,
		Description:     desc,
		EmbeddingModel:  installed.Name,
		EmbeddingDigest: installed.Digest,
		Dimensions:      dims,
	}, storeEntries)
	if err != nil {
		if ctx.Err() != nil {
			writeError(w, http.StatusGatewayTimeout, errors.New("embedding timed out or was cancelled"))
			return
		}
		writeError(w, http.StatusInternalServerError, fmt.Errorf("could not write rag base: %w", err))
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{
		"ok": true,
		"rag": map[string]any{
			"filename":         filename,
			"id":               meta.ID,
			"name":             meta.Name,
			"embedding_model":  meta.EmbeddingModel,
			"embedding_digest": meta.EmbeddingDigest,
			"dimensions":       meta.Dimensions,
			"entries":          len(storeEntries),
			"created_at":       meta.CreatedAt,
		},
	})
}
