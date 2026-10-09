package server

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"mime"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/gense/ollama-manager/internal/ollama"
	"github.com/gense/ollama-manager/internal/rag"
)

const (
	ragCreateTimeout = 10 * time.Minute
	ragMaxBody       = 24 << 20
	ragMaxImportBody = 256 << 20
	ragMaxEntries    = 100
	ragMaxTermLen    = 256
	ragMaxNameLen    = 256
	ragMaxDescLen    = 8192
	ragMaxContentLen = 32000
	ragMaxMediaName  = 256
	ragMaxMediaBytes = 8 << 20
	ragMaxMediaTotal = 16 << 20
)

type ragMediaBody struct {
	Type     string `json:"type"`
	Name     string `json:"name"`
	MIME     string `json:"mime"`
	Base64   string `json:"base64"`
	Existing bool   `json:"existing"`
	EntryID  int64  `json:"entry_id"`
}

type ragEntryBody struct {
	ID          int64          `json:"id"`
	Term        string         `json:"term"`
	Content     string         `json:"content"`
	InputMode   string         `json:"input_mode"`
	Media       []ragMediaBody `json:"media"`
	MediaType   string         `json:"media_type"`
	MediaName   string         `json:"media_name"`
	MediaMIME   string         `json:"media_mime"`
	MediaBase64 string         `json:"media_base64"`
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

func canonicalMime(m string) string {
	m = normalizeMime(m)
	for canonical, aliases := range ragMimeAliases {
		if m == canonical {
			return canonical
		}
		for _, alias := range aliases {
			if m == alias {
				return canonical
			}
		}
	}
	return m
}

func mimeMatches(detected, declared string) bool {
	if declared == "" {
		return true
	}
	return canonicalMime(detected) == canonicalMime(declared)
}

func detectRAGMediaMIME(declared string, raw []byte, declaredMime string) (string, error) {
	detected := canonicalMime(http.DetectContentType(raw))
	allowed := false
	switch declared {
	case "image":
		switch detected {
		case "image/png", "image/jpeg", "image/gif", "image/webp", "image/bmp":
			allowed = true
		}
	case "audio":
		switch detected {
		case "audio/wav", "audio/mpeg", "audio/ogg", "audio/webm", "audio/mp4", "audio/flac":
			allowed = true
		}
	}
	if !allowed {
		return "", fmt.Errorf("unsupported or mismatched %s media (detected %q)", declared, detected)
	}
	if declaredMime != "" && !mimeMatches(detected, declaredMime) {
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
	dir, defaultModel := s.ragConfigSnapshot()
	rags, warnings, err := rag.List(dir)
	if err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Errorf("could not read rag directory: %w", err))
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"rags":              rags,
		"directory":         dir,
		"default_embedding": defaultModel,
		"warnings":          warnings,
	})
}

// handleImportRAG accepts the raw .db body chosen in the browser file picker.
// The upload lands in a hidden temp file first and is only linked into the RAG
// directory after rag.Import validates the SQLite schema and embeddings.
func (s *Server) handleImportRAG(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, ragMaxImportBody)
	dir, _ := s.ragConfigSnapshot()
	name := strings.TrimSpace(r.URL.Query().Get("name"))
	if name == "" {
		name = strings.TrimSpace(r.Header.Get("X-RAG-Filename"))
	}
	info, err := rag.Import(dir, name, r.Body)
	if err != nil {
		var mbErr *http.MaxBytesError
		if errors.As(err, &mbErr) {
			writeError(w, http.StatusRequestEntityTooLarge, fmt.Errorf("rag file exceeds %d MiB", ragMaxImportBody>>20))
			return
		}
		writeError(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"rag": info})
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

func (s *Server) handleDownloadRAG(w http.ResponseWriter, r *http.Request) {
	filename := r.PathValue("id")
	dir, _ := s.ragConfigSnapshot()
	path, err := rag.Path(dir, filename)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			writeError(w, http.StatusNotFound, errors.New("rag base not found"))
			return
		}
		writeError(w, http.StatusBadRequest, err)
		return
	}
	w.Header().Set("Content-Type", "application/vnd.sqlite3")
	w.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": filename}))
	w.Header().Set("Cache-Control", "no-store")
	http.ServeFile(w, r, path)
}

func (s *Server) handleDeleteRAG(w http.ResponseWriter, r *http.Request) {
	filename := r.PathValue("id")
	dir, _ := s.ragConfigSnapshot()
	unlock, lockErr := s.lockRAGWrite(r.Context())
	if lockErr != nil {
		writeError(w, http.StatusRequestTimeout, errors.New("rag write is busy"))
		return
	}
	defer unlock()
	if err := rag.Delete(dir, filename); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			writeError(w, http.StatusNotFound, errors.New("rag base not found"))
			return
		}
		writeError(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) handleGetRAGMedia(w http.ResponseWriter, r *http.Request) {
	filename := r.PathValue("id")
	mediaType := r.PathValue("type")
	entryID, parseErr := strconv.ParseInt(r.PathValue("entry"), 10, 64)
	if !rag.ValidFilename(filename) || parseErr != nil || entryID <= 0 {
		writeError(w, http.StatusBadRequest, errors.New("invalid media path"))
		return
	}
	dir, _ := s.ragConfigSnapshot()
	media, err := rag.MediaAt(dir, filename, entryID, mediaType)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) || errors.Is(err, sql.ErrNoRows) {
			writeError(w, http.StatusNotFound, errors.New("media not found"))
			return
		}
		writeError(w, http.StatusBadRequest, err)
		return
	}
	contentType := media.MIME
	if contentType == "" {
		contentType = http.DetectContentType(media.Data)
	}
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Cache-Control", "private, max-age=3600")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(media.Data)
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
	s.handleSaveRAG(w, r, "")
}

func (s *Server) handleUpdateRAG(w http.ResponseWriter, r *http.Request) {
	s.handleSaveRAG(w, r, r.PathValue("id"))
}

func (s *Server) handleSaveRAG(w http.ResponseWriter, r *http.Request, updateFilename string) {
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

	ctx, cancel := context.WithTimeout(r.Context(), ragCreateTimeout)
	defer cancel()
	unlock, lockErr := s.lockRAGWrite(ctx)
	if lockErr != nil {
		writeError(w, http.StatusRequestTimeout, errors.New("rag write is busy"))
		return
	}
	defer unlock()

	dir, defaultModel := s.ragConfigSnapshot()
	var existing *rag.Detail
	existingEntries := map[int64]rag.EntryView{}
	if updateFilename != "" {
		if !rag.ValidFilename(updateFilename) {
			writeError(w, http.StatusBadRequest, errors.New("invalid base name"))
			return
		}
		var err error
		existing, err = rag.Get(dir, updateFilename)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				writeError(w, http.StatusNotFound, errors.New("rag base not found"))
				return
			}
			writeError(w, http.StatusBadRequest, err)
			return
		}
		defaultModel = existing.Meta.EmbeddingModel
		for _, entry := range existing.Entries {
			existingEntries[entry.ID] = entry
		}
	}

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

	type preparedMedia struct {
		mediaType string
		name      string
		mime      string
		b64       string
		raw       []byte
	}
	type preparedEntry struct {
		term      string
		content   string
		text      string
		inputMode string
		createdAt int64
		updatedAt int64
		media     []preparedMedia
		previous  *rag.EntryView
	}
	entryChanged := func(old *rag.EntryView, pe preparedEntry) bool {
		if old == nil || old.Term != pe.term || old.Content != pe.content || old.InputMode != pe.inputMode || len(old.Media) != len(pe.media) {
			return true
		}
		oldMedia := map[string]rag.MediaView{}
		for _, m := range old.Media {
			oldMedia[m.Type] = m
		}
		for _, m := range pe.media {
			old, ok := oldMedia[m.mediaType]
			if !ok || old.Name != m.name || old.MIME != m.mime || old.Size != int64(len(m.raw)) {
				return true
			}
		}
		return false
	}
	prepared := make([]preparedEntry, 0, len(body.Entries))
	var totalMedia int
	now := time.Now().Unix()
	seenEntryIDs := map[int64]bool{}
	for i, e := range body.Entries {
		var previous *rag.EntryView
		if e.ID != 0 {
			if existing == nil {
				writeError(w, http.StatusBadRequest, fmt.Errorf("entry %d: id is only valid when updating a base", i+1))
				return
			}
			if seenEntryIDs[e.ID] {
				writeError(w, http.StatusBadRequest, fmt.Errorf("entry %d: duplicate existing id %d", i+1, e.ID))
				return
			}
			seenEntryIDs[e.ID] = true
			old, ok := existingEntries[e.ID]
			if !ok {
				writeError(w, http.StatusBadRequest, fmt.Errorf("entry %d: unknown existing id %d", i+1, e.ID))
				return
			}
			previous = &old
		}
		term := strings.TrimSpace(e.Term)
		if utf8.RuneCountInString(term) > ragMaxTermLen {
			writeError(w, http.StatusBadRequest, fmt.Errorf("entry %d: term must be at most %d characters", i+1, ragMaxTermLen))
			return
		}
		content := strings.TrimSpace(e.Content)
		if utf8.RuneCountInString(content) > ragMaxContentLen {
			writeError(w, http.StatusBadRequest, fmt.Errorf("entry %d: content must be at most %d characters", i+1, ragMaxContentLen))
			return
		}
		mode := strings.ToLower(strings.TrimSpace(e.InputMode))
		if mode == "" {
			mode = "combined"
		}
		if mode != "combined" && mode != "media" {
			writeError(w, http.StatusBadRequest, fmt.Errorf("entry %d: unsupported input_mode %q", i+1, e.InputMode))
			return
		}
		mediaList := append([]ragMediaBody(nil), e.Media...)
		legacyUsed := e.MediaType != "" || e.MediaName != "" || e.MediaMIME != "" || e.MediaBase64 != ""
		if len(mediaList) > 0 && legacyUsed {
			writeError(w, http.StatusBadRequest, fmt.Errorf("entry %d: media must use either media[] or media_* fields", i+1))
			return
		}
		if legacyUsed {
			mediaList = append(mediaList, ragMediaBody{
				Type: e.MediaType, Name: e.MediaName, MIME: e.MediaMIME, Base64: e.MediaBase64,
			})
		}
		if len(mediaList) > 2 {
			writeError(w, http.StatusBadRequest, fmt.Errorf("entry %d: at most one image and one audio attachment are supported", i+1))
			return
		}
		text := term
		if content != "" {
			if text != "" {
				text += "\n\n"
			}
			text += content
		}
		pe := preparedEntry{term: term, content: content, text: text, inputMode: mode, previous: previous}
		if previous != nil {
			pe.createdAt = previous.CreatedAt
			pe.updatedAt = previous.UpdatedAt
		} else {
			pe.createdAt = now
			pe.updatedAt = now
		}
		seenMedia := map[string]bool{}
		for _, m := range mediaList {
			mediaType := strings.ToLower(strings.TrimSpace(m.Type))
			mediaName := strings.TrimSpace(m.Name)
			mediaB64 := strings.TrimSpace(m.Base64)
			declaredMime := strings.TrimSpace(m.MIME)
			if utf8.RuneCountInString(mediaName) > ragMaxMediaName {
				writeError(w, http.StatusBadRequest, fmt.Errorf("entry %d: media name must be at most %d characters", i+1, ragMaxMediaName))
				return
			}
			if mediaType != "image" && mediaType != "audio" {
				writeError(w, http.StatusBadRequest, fmt.Errorf("entry %d: unsupported media type %q", i+1, m.Type))
				return
			}
			if seenMedia[mediaType] {
				writeError(w, http.StatusBadRequest, fmt.Errorf("entry %d: duplicate %s attachment", i+1, mediaType))
				return
			}
			seenMedia[mediaType] = true
			if m.Existing && mediaB64 != "" {
				writeError(w, http.StatusBadRequest, fmt.Errorf("entry %d: existing %s media must not include base64", i+1, mediaType))
				return
			}
			if m.EntryID != 0 && !m.Existing {
				writeError(w, http.StatusBadRequest, fmt.Errorf("entry %d: media entry_id requires existing=true", i+1))
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

			var raw []byte
			if m.Existing {
				if updateFilename == "" || m.EntryID <= 0 {
					writeError(w, http.StatusBadRequest, fmt.Errorf("entry %d: existing %s media requires a valid entry_id", i+1, mediaType))
					return
				}
				stored, err := rag.MediaAt(dir, updateFilename, m.EntryID, mediaType)
				if err != nil {
					writeError(w, http.StatusBadRequest, fmt.Errorf("entry %d: could not read existing %s media", i+1, mediaType))
					return
				}
				raw = stored.Data
				if mediaName == "" {
					mediaName = stored.Name
				}
				if declaredMime == "" {
					declaredMime = stored.MIME
				}
				mediaB64 = base64.StdEncoding.EncodeToString(raw)
			} else {
				if mediaB64 == "" {
					writeError(w, http.StatusBadRequest, fmt.Errorf("entry %d: %s entries require base64 media", i+1, mediaType))
					return
				}
				var err error
				raw, err = base64.StdEncoding.DecodeString(mediaB64)
				if err != nil {
					writeError(w, http.StatusBadRequest, fmt.Errorf("entry %d: invalid %s base64: %v", i+1, mediaType, err))
					return
				}
			}
			if len(raw) == 0 {
				writeError(w, http.StatusBadRequest, fmt.Errorf("entry %d: %s media is empty", i+1, mediaType))
				return
			}
			if len(raw) > ragMaxMediaBytes {
				writeError(w, http.StatusBadRequest, fmt.Errorf("entry %d: %s media exceeds %d MiB", i+1, mediaType, ragMaxMediaBytes>>20))
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
			pe.media = append(pe.media, preparedMedia{
				mediaType: mediaType, name: mediaName, mime: mime, b64: mediaB64, raw: raw,
			})
		}
		if pe.term == "" {
			if len(pe.media) == 0 {
				writeError(w, http.StatusBadRequest, fmt.Errorf("entry %d: term is required for text entries", i+1))
				return
			}
			pe.term = pe.media[0].name
			if pe.term == "" {
				pe.term = fmt.Sprintf("Entry %d", i+1)
			}
		}
		if len(pe.media) == 0 && content == "" {
			writeError(w, http.StatusBadRequest, fmt.Errorf("entry %d: content is required for text entries", i+1))
			return
		}
		if mode == "media" && len(pe.media) == 0 {
			writeError(w, http.StatusBadRequest, fmt.Errorf("entry %d: media input mode requires an attachment", i+1))
			return
		}
		if entryChanged(pe.previous, pe) {
			pe.updatedAt = now
		}
		prepared = append(prepared, pe)
	}

	storeEntries := make([]rag.Entry, 0, len(prepared))
	dims := 0
	for i, pe := range prepared {
		var input any = pe.text
		if len(pe.media) > 0 {
			item := map[string]any{}
			if pe.inputMode != "media" && pe.text != "" {
				item["text"] = pe.text
			}
			for _, m := range pe.media {
				item[m.mediaType] = m.b64
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
		storeMedia := make([]rag.Media, 0, len(pe.media))
		for _, m := range pe.media {
			storeMedia = append(storeMedia, rag.Media{
				Type: m.mediaType, Name: m.name, MIME: m.mime, Data: m.raw,
			})
		}
		storeEntries = append(storeEntries, rag.Entry{
			Term:      pe.term,
			Content:   pe.content,
			InputMode: pe.inputMode,
			CreatedAt: pe.createdAt,
			UpdatedAt: pe.updatedAt,
			Media:     storeMedia,
			Embedding: vec,
		})
	}
	if err := ctx.Err(); err != nil {
		writeError(w, http.StatusGatewayTimeout, errors.New("embedding timed out or was cancelled"))
		return
	}

	metaIn := rag.Meta{
		Name:            name,
		Description:     desc,
		EmbeddingModel:  installed.Name,
		EmbeddingDigest: installed.Digest,
		Dimensions:      dims,
	}
	var meta rag.Meta
	var filename string
	var status int
	if existing != nil {
		metaIn.ID = existing.Meta.ID
		metaIn.CreatedAt = existing.Meta.CreatedAt
		meta, err = rag.Replace(ctx, dir, updateFilename, metaIn, storeEntries)
		filename = updateFilename
		status = http.StatusOK
	} else {
		meta, filename, err = rag.Create(ctx, dir, metaIn, storeEntries)
		status = http.StatusCreated
	}
	if err != nil {
		if ctx.Err() != nil {
			writeError(w, http.StatusGatewayTimeout, errors.New("embedding timed out or was cancelled"))
			return
		}
		writeError(w, http.StatusInternalServerError, fmt.Errorf("could not write rag base: %w", err))
		return
	}
	writeJSON(w, status, map[string]any{
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
			"updated_at":       meta.UpdatedAt,
		},
	})
}
