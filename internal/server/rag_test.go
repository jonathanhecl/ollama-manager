package server

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/gense/ollama-manager/internal/config"
	ragpkg "github.com/gense/ollama-manager/internal/rag"
)

type fakeOllamaRAG struct {
	srv *httptest.Server

	mu        sync.Mutex
	embeds    []map[string]any
	failAt    int
	dimSecond int
	nanAt     int
	failShow  map[string]bool
	caps      map[string][]string
	tags      []map[string]any
}

func newFakeOllamaRAG() *fakeOllamaRAG {
	f := &fakeOllamaRAG{
		caps: map[string][]string{
			"embed-model:latest":      {"embedding"},
			"embed-vision:latest":     {"embedding", "vision"},
			"embed-multimodal:latest": {"embedding", "vision", "audio"},
			"chat-model:latest":       {"completion"},
		},
	}
	f.tags = []map[string]any{
		{"name": "embed-model:latest", "digest": "sha256:embed1"},
		{"name": "embed-vision:latest", "digest": "sha256:embedv"},
		{"name": "embed-multimodal:latest", "digest": "sha256:embedm"},
		{"name": "chat-model:latest", "digest": "sha256:chat1"},
	}
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/tags":
			writeJSON(w, http.StatusOK, map[string]any{"models": f.tags})
		case "/api/show":
			var req struct {
				Name string `json:"name"`
			}
			_ = json.NewDecoder(r.Body).Decode(&req)
			caps, ok := f.caps[req.Name]
			if f.failShow[req.Name] {
				http.Error(w, "show boom", http.StatusInternalServerError)
				return
			}
			if !ok {
				http.Error(w, "not found", http.StatusNotFound)
				return
			}
			writeJSON(w, http.StatusOK, map[string]any{
				"capabilities": caps,
				"details":      map[string]any{"format": "safetensors"},
				"model_info":   map[string]any{},
			})
		case "/api/embed":
			var req map[string]any
			_ = json.NewDecoder(r.Body).Decode(&req)
			f.mu.Lock()
			f.embeds = append(f.embeds, req)
			call := len(f.embeds)
			failAt, dimSecond, nanAt := f.failAt, f.dimSecond, f.nanAt
			f.mu.Unlock()
			if failAt > 0 && call == failAt {
				http.Error(w, "boom", http.StatusInternalServerError)
				return
			}
			dims := 3
			if dimSecond > 0 && call > 1 {
				dims = dimSecond
			}
			if nanAt > 0 && call == nanAt {
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{"embedding":[NaN,0.1,0.2]}`))
				return
			}
			vec := make([]float64, dims)
			for i := range vec {
				vec[i] = float64(call)*10 + float64(i)/10
			}
			writeJSON(w, http.StatusOK, map[string]any{"embedding": vec})
		default:
			http.NotFound(w, r)
		}
	}))
	return f
}

func (f *fakeOllamaRAG) reset() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.embeds = nil
	f.failAt = 0
	f.dimSecond = 0
	f.nanAt = 0
	f.failShow = nil
}

func (f *fakeOllamaRAG) recordedEmbeds() []map[string]any {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]map[string]any, len(f.embeds))
	copy(out, f.embeds)
	return out
}

func (f *fakeOllamaRAG) Close() { f.srv.Close() }

func ragCall(t *testing.T, srv *Server, method, target string, payload any) (int, map[string]any) {
	t.Helper()
	code, _, raw := ragRawCall(t, srv, method, target, payload)
	var out map[string]any
	_ = json.Unmarshal(raw, &out)
	return code, out
}

func ragRawCall(t *testing.T, srv *Server, method, target string, payload any) (int, http.Header, []byte) {
	t.Helper()
	var body *bytes.Reader
	if payload != nil {
		raw, _ := json.Marshal(payload)
		body = bytes.NewReader(raw)
	} else {
		body = bytes.NewReader(nil)
	}
	req := httptest.NewRequest(method, target, body)
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	srv.Routes().ServeHTTP(rr, req)
	return rr.Code, rr.Header(), rr.Body.Bytes()
}

func ragDirOf(t *testing.T, srv *Server) string {
	t.Helper()
	dir, _ := srv.ragConfigSnapshot()
	return dir
}

func countDBFiles(t *testing.T, dir string) []string {
	t.Helper()
	fis, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var names []string
	for _, f := range fis {
		names = append(names, f.Name())
	}
	return names
}

var twoEntries = []any{
	map[string]any{"term": "apple", "content": "a fruit"},
	map[string]any{"term": "pear", "content": "another fruit"},
}

func TestRAGCreateAndList(t *testing.T) {
	fake := newFakeOllamaRAG()
	defer fake.Close()
	srv := newTestServer(t, fake.srv.URL)

	code, out := ragCall(t, srv, http.MethodPost, "/api/rags", map[string]any{
		"name":            "Fruits",
		"description":     "d",
		"embedding_model": "embed-model",
		"entries":         twoEntries,
	})
	if code != http.StatusCreated {
		t.Fatalf("create status = %d, body = %v", code, out)
	}
	ragMeta, _ := out["rag"].(map[string]any)
	if ragMeta["embedding_model"] != "embed-model:latest" || ragMeta["embedding_digest"] != "sha256:embed1" {
		t.Fatalf("unexpected created meta: %v", ragMeta)
	}
	if ragMeta["dimensions"] != float64(3) || ragMeta["entries"] != float64(2) {
		t.Fatalf("unexpected created meta: %v", ragMeta)
	}
	filename, _ := ragMeta["filename"].(string)
	if filename == "" || !strings.HasSuffix(filename, ".db") {
		t.Fatalf("bad filename %q", filename)
	}

	files := countDBFiles(t, ragDirOf(t, srv))
	if len(files) != 1 || files[0] != filename {
		t.Fatalf("dir files = %v", files)
	}

	embeds := fake.recordedEmbeds()
	if len(embeds) != 2 {
		t.Fatalf("embed calls = %d", len(embeds))
	}
	for i, e := range embeds {
		if e["model"] != "embed-model:latest" {
			t.Fatalf("embed %d model = %v", i, e["model"])
		}
	}
	if embeds[0]["input"] != "apple\n\na fruit" || embeds[1]["input"] != "pear\n\nanother fruit" {
		t.Fatalf("embed inputs = %v", embeds)
	}

	code, list := ragCall(t, srv, http.MethodGet, "/api/rags", nil)
	if code != http.StatusOK {
		t.Fatalf("list status = %d", code)
	}
	rags, _ := list["rags"].([]any)
	if len(rags) != 1 {
		t.Fatalf("rags = %v", list)
	}
	row := rags[0].(map[string]any)
	if row["filename"] != filename || row["name"] != "Fruits" ||
		row["description"] != "d" || row["entries"] != float64(2) {
		t.Fatalf("list row = %v", row)
	}
	if list["directory"] != ragDirOf(t, srv) {
		t.Fatalf("directory = %v", list["directory"])
	}

	code, det := ragCall(t, srv, http.MethodGet, "/api/rags/"+filename, nil)
	if code != http.StatusOK {
		t.Fatalf("detail status = %d, body = %v", code, det)
	}
	if det["size_bytes"].(float64) <= 0 {
		t.Fatalf("detail size_bytes = %v", det["size_bytes"])
	}
	entries, _ := det["entries"].([]any)
	if len(entries) != 2 {
		t.Fatalf("entries = %v", det)
	}
	first := entries[0].(map[string]any)
	if first["term"] != "apple" || first["content"] != "a fruit" {
		t.Fatalf("entry = %v", first)
	}
	if _, leaked := first["embedding"]; leaked {
		t.Fatalf("vector leaked in detail: %v", first)
	}
}

func TestRAGDownloadAndDelete(t *testing.T) {
	fake := newFakeOllamaRAG()
	defer fake.Close()
	srv := newTestServer(t, fake.srv.URL)

	code, out := ragCall(t, srv, http.MethodPost, "/api/rags", map[string]any{
		"name": "Portable", "embedding_model": "embed-model", "entries": twoEntries,
	})
	if code != http.StatusCreated {
		t.Fatalf("create status = %d, body = %v", code, out)
	}
	filename := out["rag"].(map[string]any)["filename"].(string)

	code, headers, raw := ragRawCall(t, srv, http.MethodGet, "/api/rags/"+filename+"/download", nil)
	if code != http.StatusOK {
		t.Fatalf("download status = %d", code)
	}
	stored, err := os.ReadFile(filepath.Join(ragDirOf(t, srv), filename))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(raw, stored) {
		t.Fatalf("download returned %d bytes, file has %d", len(raw), len(stored))
	}
	if headers.Get("Content-Type") != "application/vnd.sqlite3" ||
		!strings.Contains(headers.Get("Content-Disposition"), "attachment") ||
		!strings.Contains(headers.Get("Content-Disposition"), filename) {
		t.Fatalf("download headers = %v", headers)
	}

	code, out = ragCall(t, srv, http.MethodDelete, "/api/rags/"+filename, nil)
	if code != http.StatusOK || out["ok"] != true {
		t.Fatalf("delete = %d %v", code, out)
	}
	code, _ = ragCall(t, srv, http.MethodGet, "/api/rags/"+filename, nil)
	if code != http.StatusNotFound {
		t.Fatalf("deleted detail status = %d, want 404", code)
	}
	code, _ = ragCall(t, srv, http.MethodDelete, "/api/rags/"+filename, nil)
	if code != http.StatusNotFound {
		t.Fatalf("second delete status = %d, want 404", code)
	}
}

func ragImportCall(t *testing.T, srv *Server, filename string, raw []byte) (int, map[string]any) {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/api/rags/import?name="+filename, bytes.NewReader(raw))
	req.Header.Set("Content-Type", "application/octet-stream")
	rr := httptest.NewRecorder()
	srv.Routes().ServeHTTP(rr, req)
	var out map[string]any
	_ = json.Unmarshal(rr.Body.Bytes(), &out)
	return rr.Code, out
}

func TestRAGImportUpload(t *testing.T) {
	fake := newFakeOllamaRAG()
	defer fake.Close()
	srv := newTestServer(t, fake.srv.URL)

	code, out := ragCall(t, srv, http.MethodPost, "/api/rags", map[string]any{
		"name": "Portable", "embedding_model": "embed-model", "entries": twoEntries,
	})
	if code != http.StatusCreated {
		t.Fatalf("create status = %d, body = %v", code, out)
	}
	filename := out["rag"].(map[string]any)["filename"].(string)
	raw, err := os.ReadFile(filepath.Join(ragDirOf(t, srv), filename))
	if err != nil {
		t.Fatal(err)
	}

	code, out = ragImportCall(t, srv, "external.db", raw)
	if code != http.StatusCreated {
		t.Fatalf("import status = %d, body = %v", code, out)
	}
	imported, _ := out["rag"].(map[string]any)
	if imported["name"] != "Portable" || imported["entries"] != float64(2) {
		t.Fatalf("imported rag = %v", imported)
	}
	importedFile, _ := imported["filename"].(string)
	if !ragpkg.ValidFilename(importedFile) {
		t.Fatalf("import returned unsafe filename %q", importedFile)
	}
	if got := countDBFiles(t, ragDirOf(t, srv)); len(got) != 2 {
		t.Fatalf("files after import = %v", got)
	}

	code, _ = ragImportCall(t, srv, "broken.db", []byte("not sqlite"))
	if code != http.StatusBadRequest {
		t.Fatalf("invalid import status = %d, want 400", code)
	}
	if got := countDBFiles(t, ragDirOf(t, srv)); len(got) != 2 {
		t.Fatalf("invalid import left files = %v", got)
	}
}

func TestRAGCreateUsesDefaultModel(t *testing.T) {
	fake := newFakeOllamaRAG()
	defer fake.Close()
	srv := newTestServer(t, fake.srv.URL)

	code, _ := patchConfig(t, srv, map[string]any{
		"rag": map[string]any{"default_embedding": "embed-model:latest"},
	})
	if code != http.StatusOK {
		t.Fatalf("patch status = %d", code)
	}
	code, out := ragCall(t, srv, http.MethodPost, "/api/rags", map[string]any{
		"name":    "Default",
		"entries": twoEntries,
	})
	if code != http.StatusCreated {
		t.Fatalf("create status = %d, body = %v", code, out)
	}
	embeds := fake.recordedEmbeds()
	if len(embeds) == 0 || embeds[0]["model"] != "embed-model:latest" {
		t.Fatalf("embeds = %v", embeds)
	}

	code, out = ragCall(t, srv, http.MethodPost, "/api/rags", map[string]any{
		"name":            "Override",
		"embedding_model": "embed-vision:latest",
		"entries":         twoEntries,
	})
	if code != http.StatusCreated {
		t.Fatalf("override status = %d, body = %v", code, out)
	}
	embeds = fake.recordedEmbeds()
	if got := embeds[len(embeds)-1]["model"]; got != "embed-vision:latest" {
		t.Fatalf("override model = %v", got)
	}
}

func TestRAGCreateValidation(t *testing.T) {
	fake := newFakeOllamaRAG()
	defer fake.Close()
	srv := newTestServer(t, fake.srv.URL)
	dir := ragDirOf(t, srv)

	cases := []struct {
		name    string
		payload map[string]any
	}{
		{"no name", map[string]any{"embedding_model": "embed-model", "entries": twoEntries}},
		{"no entries", map[string]any{"name": "x", "embedding_model": "embed-model"}},
		{"no model", map[string]any{"name": "x", "entries": twoEntries}},
		{"not installed", map[string]any{"name": "x", "embedding_model": "ghost", "entries": twoEntries}},
		{"no embedding cap", map[string]any{"name": "x", "embedding_model": "chat-model", "entries": twoEntries}},
		{"blank term", map[string]any{"name": "x", "embedding_model": "embed-model",
			"entries": []any{map[string]any{"term": "  ", "content": "c"}}}},
		{"blank text content", map[string]any{"name": "x", "embedding_model": "embed-model",
			"entries": []any{map[string]any{"term": "t", "content": "   "}}}},
		{"bad media type", map[string]any{"name": "x", "embedding_model": "embed-model",
			"entries": []any{map[string]any{"term": "t", "content": "c", "media_type": "video", "media_base64": "AA=="}}}},
		{"unknown type no payload", map[string]any{"name": "x", "embedding_model": "embed-model",
			"entries": []any{map[string]any{"term": "t", "content": "c", "media_type": "video"}}}},
		{"image without payload", map[string]any{"name": "x", "embedding_model": "embed-vision",
			"entries": []any{map[string]any{"term": "t", "content": "c", "media_type": "image"}}}},
		{"text with payload", map[string]any{"name": "x", "embedding_model": "embed-model",
			"entries": []any{map[string]any{"term": "t", "content": "c", "media_base64": "AA=="}}}},
		{"image without vision cap", map[string]any{"name": "x", "embedding_model": "embed-model",
			"entries": []any{map[string]any{"term": "t", "media_type": "image", "media_base64": "AA=="}}}},
		{"invalid base64", map[string]any{"name": "x", "embedding_model": "embed-vision",
			"entries": []any{map[string]any{"term": "t", "media_type": "image", "media_base64": "!!!"}}}},
		{"audio without audio cap", map[string]any{"name": "x", "embedding_model": "embed-vision",
			"entries": []any{map[string]any{"term": "t", "media_type": "audio", "media_base64": "AA=="}}}},
		{"rune limit", map[string]any{"name": "x", "embedding_model": "embed-model",
			"entries": []any{map[string]any{"term": strings.Repeat("á", ragMaxTermLen+1), "content": "c"}}}},
		{"media name too long", map[string]any{"name": "x", "embedding_model": "embed-vision",
			"entries": []any{map[string]any{"term": "t", "media_type": "image",
				"media_name": strings.Repeat("n", ragMaxMediaName+1), "media_base64": "AA=="}}}},
		{"declared mime mismatch", map[string]any{"name": "x", "embedding_model": "embed-vision",
			"entries": []any{map[string]any{"term": "t", "media_type": "image",
				"media_mime": "image/png", "media_base64": base64.StdEncoding.EncodeToString([]byte("hello world this is not an image"))}}}},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			before := len(fake.recordedEmbeds())
			code, out := ragCall(t, srv, http.MethodPost, "/api/rags", tt.payload)
			if code == http.StatusCreated {
				t.Fatalf("status = 201, body = %v", out)
			}
			if code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400", code)
			}
			if len(fake.recordedEmbeds()) != before {
				t.Fatalf("embed ran on invalid request")
			}
			if files := countDBFiles(t, dir); len(files) != 0 {
				t.Fatalf("failure left files %v", files)
			}
		})
	}
}

func TestRAGMimeAliases(t *testing.T) {
	cases := []struct {
		name     string
		detected string
		declared string
		want     bool
	}{
		{"wav detector alias", "audio/wave", "audio/wav", true},
		{"wav browser alias", "audio/wav", "audio/x-wav", true},
		{"jpeg alias", "image/jpeg", "image/jpg", true},
		{"ogg detector alias", "application/ogg", "audio/ogg", true},
		{"different media", "audio/wav", "image/png", false},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			if got := mimeMatches(tt.detected, tt.declared); got != tt.want {
				t.Fatalf("mimeMatches(%q, %q) = %v, want %v", tt.detected, tt.declared, got, tt.want)
			}
		})
	}
}

func TestRAGCreateMedia(t *testing.T) {
	fake := newFakeOllamaRAG()
	defer fake.Close()
	srv := newTestServer(t, fake.srv.URL)

	png := []byte{0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A, 0, 0, 0, 0, 0x49, 0x48, 0x44, 0x52}
	b64 := base64.StdEncoding.EncodeToString(png)
	code, out := ragCall(t, srv, http.MethodPost, "/api/rags", map[string]any{
		"name":            "Pics",
		"embedding_model": "embed-vision:latest",
		"entries": []any{map[string]any{
			"term": "logo", "media_type": "image", "media_name": "logo.png",
			"media_base64": b64,
		}},
	})
	if code != http.StatusCreated {
		t.Fatalf("status = %d, body = %v", code, out)
	}
	embeds := fake.recordedEmbeds()
	if len(embeds) != 1 {
		t.Fatalf("embeds = %v", embeds)
	}
	input, _ := embeds[0]["input"].([]any)
	if len(input) != 1 {
		t.Fatalf("media input = %v", embeds[0]["input"])
	}
	item, _ := input[0].(map[string]any)
	if item["image"] != b64 || item["text"] != "logo" {
		t.Fatalf("media input item = %v", item)
	}

	filename := out["rag"].(map[string]any)["filename"].(string)
	_, det := ragCall(t, srv, http.MethodGet, "/api/rags/"+filename, nil)
	entry := det["entries"].([]any)[0].(map[string]any)
	media, _ := entry["media"].([]any)
	if entry["input_mode"] != "combined" || len(media) != 1 {
		t.Fatalf("entry = %v", entry)
	}
	m := media[0].(map[string]any)
	if m["type"] != "image" || m["mime"] != "image/png" || m["size"] != float64(len(png)) {
		t.Fatalf("media = %v", m)
	}
	entryID := int64(entry["id"].(float64))
	code, headers, raw := ragRawCall(t, srv, http.MethodGet,
		"/api/rags/"+filename+"/media/"+strconv.FormatInt(entryID, 10)+"/image", nil)
	if code != http.StatusOK || !bytes.Equal(raw, png) || headers.Get("Content-Type") != "image/png" {
		t.Fatalf("media endpoint = status %d content-type %q len %d", code, headers.Get("Content-Type"), len(raw))
	}
}

func TestRAGUpdateKeepsFileAndExistingMedia(t *testing.T) {
	fake := newFakeOllamaRAG()
	defer fake.Close()
	srv := newTestServer(t, fake.srv.URL)

	png := []byte{0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A, 0, 0, 0, 0, 0x49, 0x48, 0x44, 0x52}
	b64 := base64.StdEncoding.EncodeToString(png)
	code, out := ragCall(t, srv, http.MethodPost, "/api/rags", map[string]any{
		"name": "Pics", "embedding_model": "embed-vision:latest",
		"entries": []any{map[string]any{
			"term": "logo", "content": "old", "media": []any{
				map[string]any{"type": "image", "name": "logo.png", "base64": b64},
			},
		}},
	})
	if code != http.StatusCreated {
		t.Fatalf("create status = %d, body = %v", code, out)
	}
	filename := out["rag"].(map[string]any)["filename"].(string)
	_, det := ragCall(t, srv, http.MethodGet, "/api/rags/"+filename, nil)
	oldEntry := det["entries"].([]any)[0].(map[string]any)
	entryID := int64(oldEntry["id"].(float64))

	db, err := sql.Open("sqlite", filepath.Join(ragDirOf(t, srv), filename))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE entries SET created_at = 1000, updated_at = 1000 WHERE id = ?`, entryID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE metadata SET updated_at = 1000 WHERE key = 1`); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	code, out = ragCall(t, srv, http.MethodPut, "/api/rags/"+filename, map[string]any{
		"name": "Pics renamed", "embedding_model": "embed-vision:latest",
		"entries": []any{map[string]any{
			"id": entryID, "term": "logo2", "content": "updated", "media": []any{
				map[string]any{"type": "image", "entry_id": entryID, "existing": true},
			},
		}},
	})
	if code != http.StatusOK {
		t.Fatalf("update status = %d, body = %v", code, out)
	}
	if out["rag"].(map[string]any)["filename"] != filename {
		t.Fatalf("update changed filename: %v", out["rag"])
	}
	if files := countDBFiles(t, ragDirOf(t, srv)); len(files) != 1 || files[0] != filename {
		t.Fatalf("update files = %v", files)
	}
	_, det = ragCall(t, srv, http.MethodGet, "/api/rags/"+filename, nil)
	entry := det["entries"].([]any)[0].(map[string]any)
	if entry["term"] != "logo2" || len(entry["media"].([]any)) != 1 {
		t.Fatalf("updated entry = %v", entry)
	}
	if entry["created_at"] != float64(1000) || entry["updated_at"].(float64) <= 1000 {
		t.Fatalf("entry timestamps = %v", entry)
	}
	meta := det["meta"].(map[string]any)
	if meta["updated_at"].(float64) <= 1000 {
		t.Fatalf("base updated_at = %v", meta)
	}

	db, err = sql.Open("sqlite", filepath.Join(ragDirOf(t, srv), filename))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE entries SET updated_at = 1000 WHERE id = ?`, entryID); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	code, out = ragCall(t, srv, http.MethodPut, "/api/rags/"+filename, map[string]any{
		"name": "Pics renamed", "embedding_model": "embed-vision:latest",
		"entries": []any{map[string]any{
			"id": entryID, "term": "logo2", "content": "updated", "media": []any{
				map[string]any{"type": "image", "entry_id": entryID, "existing": true},
			},
		}},
	})
	if code != http.StatusOK {
		t.Fatalf("unchanged update status = %d, body = %v", code, out)
	}
	_, det = ragCall(t, srv, http.MethodGet, "/api/rags/"+filename, nil)
	entry = det["entries"].([]any)[0].(map[string]any)
	if entry["updated_at"] != float64(1000) {
		t.Fatalf("unchanged entry updated_at = %v", entry)
	}

	embeds := fake.recordedEmbeds()
	if len(embeds) != 3 {
		t.Fatalf("embed calls = %d", len(embeds))
	}
	item := embeds[1]["input"].([]any)[0].(map[string]any)
	if item["image"] != b64 || item["text"] != "logo2\n\nupdated" {
		t.Fatalf("updated embed input = %v", item)
	}
}

func TestRAGCreateMediaOnlyWithImageAndAudio(t *testing.T) {
	fake := newFakeOllamaRAG()
	defer fake.Close()
	srv := newTestServer(t, fake.srv.URL)

	png := []byte{0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A, 0, 0, 0, 0, 0x49, 0x48, 0x44, 0x52}
	wav := append([]byte("RIFF"), append([]byte{0x24, 0, 0, 0}, []byte("WAVEfmt ")...)...)
	imageB64 := base64.StdEncoding.EncodeToString(png)
	audioB64 := base64.StdEncoding.EncodeToString(wav)
	code, out := ragCall(t, srv, http.MethodPost, "/api/rags", map[string]any{
		"name":            "Media keys",
		"embedding_model": "embed-multimodal:latest",
		"entries": []any{map[string]any{
			"term": "", "input_mode": "media",
			"media": []any{
				map[string]any{"type": "image", "name": "logo.png", "base64": imageB64},
				map[string]any{"type": "audio", "name": "sonic.wav", "mime": "audio/wav", "base64": audioB64},
			},
		}},
	})
	if code != http.StatusCreated {
		t.Fatalf("status = %d, body = %v", code, out)
	}
	embeds := fake.recordedEmbeds()
	if len(embeds) != 1 {
		t.Fatalf("embeds = %v", embeds)
	}
	item := embeds[0]["input"].([]any)[0].(map[string]any)
	if _, hasText := item["text"]; hasText || item["image"] != imageB64 || item["audio"] != audioB64 {
		t.Fatalf("media-only input = %v", item)
	}
	filename := out["rag"].(map[string]any)["filename"].(string)
	_, det := ragCall(t, srv, http.MethodGet, "/api/rags/"+filename, nil)
	entry := det["entries"].([]any)[0].(map[string]any)
	if entry["input_mode"] != "media" || len(entry["media"].([]any)) != 2 {
		t.Fatalf("entry = %v", entry)
	}
	if entry["term"] != "logo.png" {
		t.Fatalf("generated media key = %v", entry["term"])
	}
}

func TestRAGCreateFailureLeavesNoFile(t *testing.T) {
	fake := newFakeOllamaRAG()
	defer fake.Close()
	srv := newTestServer(t, fake.srv.URL)
	dir := ragDirOf(t, srv)

	fake.reset()
	fake.failAt = 2
	code, out := ragCall(t, srv, http.MethodPost, "/api/rags", map[string]any{
		"name": "partial", "embedding_model": "embed-model", "entries": twoEntries,
	})
	if code != http.StatusBadGateway {
		t.Fatalf("status = %d, body = %v", code, out)
	}
	if files := countDBFiles(t, dir); len(files) != 0 {
		t.Fatalf("failed create left %v", files)
	}

	fake.reset()
	fake.dimSecond = 5
	code, _ = ragCall(t, srv, http.MethodPost, "/api/rags", map[string]any{
		"name": "dims", "embedding_model": "embed-model", "entries": twoEntries,
	})
	if code != http.StatusBadGateway {
		t.Fatalf("dims status = %d", code)
	}
	if files := countDBFiles(t, dir); len(files) != 0 {
		t.Fatalf("failed create left %v", files)
	}
}

func TestRAGBodyTooLarge(t *testing.T) {
	fake := newFakeOllamaRAG()
	defer fake.Close()
	srv := newTestServer(t, fake.srv.URL)
	dir := ragDirOf(t, srv)

	huge := strings.Repeat("a", ragMaxContentLen+1)
	code, _ := ragCall(t, srv, http.MethodPost, "/api/rags", map[string]any{
		"name": "x", "embedding_model": "embed-model",
		"entries": []any{map[string]any{"term": "t", "content": huge}},
	})
	if code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", code)
	}

	raw := append([]byte(`{"name":"x","entries":`), bytes.Repeat([]byte(" "), ragMaxBody+1)...)
	req := httptest.NewRequest(http.MethodPost, "/api/rags", bytes.NewReader(raw))
	rr := httptest.NewRecorder()
	srv.Routes().ServeHTTP(rr, req)
	if rr.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("oversized body status = %d, want 413", rr.Code)
	}
	if files := countDBFiles(t, dir); len(files) != 0 {
		t.Fatalf("oversized left files %v", files)
	}
}

func TestRAGTrailingJSON(t *testing.T) {
	fake := newFakeOllamaRAG()
	defer fake.Close()
	srv := newTestServer(t, fake.srv.URL)

	raw := `{"name":"x","embedding_model":"embed-model","entries":[{"term":"t","content":"c"}]} {}`
	req := httptest.NewRequest(http.MethodPost, "/api/rags", strings.NewReader(raw))
	rr := httptest.NewRecorder()
	srv.Routes().ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rr.Code)
	}
}

func TestRAGDetailGuards(t *testing.T) {
	fake := newFakeOllamaRAG()
	defer fake.Close()
	srv := newTestServer(t, fake.srv.URL)

	for _, bad := range []string{"..%2F..%2Fconfig.json", "a%2Fb.db", "a%5Cb.db", "x.txt", ".hidden.db"} {
		for _, tc := range []struct{ method, suffix string }{
			{http.MethodGet, ""},
			{http.MethodDelete, ""},
			{http.MethodGet, "/download"},
		} {
			code, _ := ragCall(t, srv, tc.method, "/api/rags/"+bad+tc.suffix, nil)
			if code != http.StatusBadRequest {
				t.Fatalf("%s %q status = %d, want 400", tc.method, bad+tc.suffix, code)
			}
		}
	}
	for _, tc := range []struct{ method, suffix string }{
		{http.MethodGet, ""},
		{http.MethodDelete, ""},
		{http.MethodGet, "/download"},
	} {
		code, _ := ragCall(t, srv, tc.method, "/api/rags/missing.db"+tc.suffix, nil)
		if code != http.StatusNotFound {
			t.Fatalf("missing %s %q status = %d, want 404", tc.method, tc.suffix, code)
		}
	}
}

func TestRAGModelsEndpoint(t *testing.T) {
	fake := newFakeOllamaRAG()
	defer fake.Close()
	srv := newTestServer(t, fake.srv.URL)

	code, out := ragCall(t, srv, http.MethodGet, "/api/rags/models", nil)
	if code != http.StatusOK {
		t.Fatalf("status = %d, body = %v", code, out)
	}
	models, _ := out["models"].([]any)
	if len(models) != 3 {
		t.Fatalf("models = %v", out)
	}
	names := map[string]bool{}
	for _, m := range models {
		names[m.(map[string]any)["name"].(string)] = true
	}
	if !names["embed-model:latest"] || !names["embed-vision:latest"] ||
		!names["embed-multimodal:latest"] || names["chat-model:latest"] {
		t.Fatalf("models = %v", models)
	}
}

func TestRAGConfigPatchGet(t *testing.T) {
	fake := newFakeOllamaRAG()
	defer fake.Close()
	srv := newTestServer(t, fake.srv.URL)

	got := getConfig(t, srv)
	ragCfg, _ := got["rag"].(map[string]any)
	if ragCfg["default_embedding"] != "" && ragCfg["default_embedding"] != nil {
		t.Fatalf("default_embedding = %v", ragCfg["default_embedding"])
	}
	wantDir := filepath.Join(filepath.Dir(srv.cfg.Path()), "rags")
	if got["rag_directory"] != wantDir {
		t.Fatalf("rag_directory = %v, want %q", got["rag_directory"], wantDir)
	}

	code, out := patchConfig(t, srv, map[string]any{
		"rag": map[string]any{"default_embedding": " embed-model:latest ", "directory": " bases "},
	})
	if code != http.StatusOK {
		t.Fatalf("patch status = %d, body = %v", code, out)
	}
	if out["needs_restart"] != false {
		t.Fatalf("rag patch should not need restart: %v", out)
	}
	ragCfg, _ = out["rag"].(map[string]any)
	if ragCfg["default_embedding"] != "embed-model:latest" || ragCfg["directory"] != "bases" {
		t.Fatalf("rag = %v", ragCfg)
	}
	if out["rag_directory"] != filepath.Join(filepath.Dir(srv.cfg.Path()), "bases") {
		t.Fatalf("rag_directory = %v", out["rag_directory"])
	}

	code, out = patchConfig(t, srv, map[string]any{
		"rag": map[string]any{"directory": "bases2"},
	})
	if code != http.StatusOK {
		t.Fatalf("patch2 status = %d", code)
	}
	ragCfg, _ = out["rag"].(map[string]any)
	if ragCfg["default_embedding"] != "embed-model:latest" || ragCfg["directory"] != "bases2" {
		t.Fatalf("preserved rag = %v", ragCfg)
	}

	code, out = patchConfig(t, srv, map[string]any{
		"rag": map[string]any{"default_embedding": "", "directory": ""},
	})
	if code != http.StatusOK {
		t.Fatalf("clear status = %d", code)
	}
	ragCfg, _ = out["rag"].(map[string]any)
	if ragCfg["default_embedding"] != "" || ragCfg["directory"] != "" {
		t.Fatalf("cleared rag = %v", ragCfg)
	}
	if out["rag_directory"] != wantDir {
		t.Fatalf("cleared rag_directory = %v", out["rag_directory"])
	}
}

func TestRAGUpstreamInvalidVector(t *testing.T) {
	fake := newFakeOllamaRAG()
	defer fake.Close()
	srv := newTestServer(t, fake.srv.URL)
	dir := ragDirOf(t, srv)

	fake.nanAt = 1
	code, out := ragCall(t, srv, http.MethodPost, "/api/rags", map[string]any{
		"name": "bad", "embedding_model": "embed-model", "entries": twoEntries,
	})
	if code != http.StatusBadGateway {
		t.Fatalf("status = %d body = %v, want 502", code, out)
	}
	if files := countDBFiles(t, dir); len(files) != 0 {
		t.Fatalf("invalid vector left files %v", files)
	}
}

func TestRAGCreateCancelled(t *testing.T) {
	fake := newFakeOllamaRAG()
	defer fake.Close()
	srv := newTestServer(t, fake.srv.URL)
	dir := ragDirOf(t, srv)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	raw, _ := json.Marshal(map[string]any{
		"name": "x", "embedding_model": "embed-model", "entries": twoEntries,
	})
	req := httptest.NewRequest(http.MethodPost, "/api/rags", bytes.NewReader(raw)).WithContext(ctx)
	rr := httptest.NewRecorder()
	srv.Routes().ServeHTTP(rr, req)
	if rr.Code == http.StatusCreated {
		t.Fatal("cancelled create succeeded")
	}
	if files := countDBFiles(t, dir); len(files) != 0 {
		t.Fatalf("cancelled create left %v", files)
	}
}

func TestRAGListWarnings(t *testing.T) {
	fake := newFakeOllamaRAG()
	defer fake.Close()
	srv := newTestServer(t, fake.srv.URL)
	dir := ragDirOf(t, srv)

	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "corrupt.db"), []byte("garbage"), 0o644); err != nil {
		t.Fatal(err)
	}
	code, out := ragCall(t, srv, http.MethodGet, "/api/rags", nil)
	if code != http.StatusOK {
		t.Fatalf("status = %d", code)
	}
	warnings, _ := out["warnings"].([]any)
	if len(warnings) != 1 || !strings.Contains(warnings[0].(string), "corrupt.db") {
		t.Fatalf("warnings = %v", out["warnings"])
	}
}

func TestRAGModelsWarnings(t *testing.T) {
	fake := newFakeOllamaRAG()
	defer fake.Close()
	srv := newTestServer(t, fake.srv.URL)

	fake.failShow = map[string]bool{"embed-vision:latest": true}
	code, out := ragCall(t, srv, http.MethodGet, "/api/rags/models", nil)
	if code != http.StatusOK {
		t.Fatalf("status = %d", code)
	}
	models, _ := out["models"].([]any)
	warnings, _ := out["warnings"].([]any)
	if len(models) != 2 || len(warnings) != 1 {
		t.Fatalf("models = %v warnings = %v", models, warnings)
	}
	if !strings.Contains(warnings[0].(string), "embed-vision:latest") {
		t.Fatalf("warning = %v", warnings[0])
	}
}

func TestRAGRoutesRequireAuth(t *testing.T) {
	fake := newFakeOllamaRAG()
	defer fake.Close()
	srv := newTestServer(t, fake.srv.URL)

	srv.cfgMu.Lock()
	srv.cfg.PasswordHash = "set"
	srv.cfgMu.Unlock()
	defer func() {
		srv.cfgMu.Lock()
		srv.cfg.PasswordHash = ""
		srv.cfgMu.Unlock()
	}()

	for _, tc := range []struct{ method, path string }{
		{http.MethodGet, "/api/rags"},
		{http.MethodPost, "/api/rags"},
		{http.MethodPost, "/api/rags/import"},
		{http.MethodGet, "/api/rags/models"},
		{http.MethodGet, "/api/rags/x.db"},
		{http.MethodGet, "/api/rags/x.db/download"},
		{http.MethodGet, "/api/rags/x.db/media/1/image"},
		{http.MethodPost, "/api/rags/external/open"},
		{http.MethodPost, "/api/rags/external/save"},
		{http.MethodPut, "/api/rags/x.db"},
		{http.MethodDelete, "/api/rags/x.db"},
	} {
		req := httptest.NewRequest(tc.method, tc.path, nil)
		rr := httptest.NewRecorder()
		srv.Routes().ServeHTTP(rr, req)
		if rr.Code != http.StatusUnauthorized {
			t.Fatalf("%s %s status = %d, want 401", tc.method, tc.path, rr.Code)
		}
	}
}

func TestRAGPatchSaveFailureRollback(t *testing.T) {
	fake := newFakeOllamaRAG()
	defer fake.Close()
	srv := newTestServer(t, fake.srv.URL)

	code, _ := patchConfig(t, srv, map[string]any{
		"rag": map[string]any{"default_embedding": "embed-model:latest"},
	})
	if code != http.StatusOK {
		t.Fatalf("initial patch = %d", code)
	}

	cfgDir := filepath.Dir(srv.cfg.Path())
	if err := os.Chmod(cfgDir, 0o555); err != nil {
		t.Fatal(err)
	}
	code, _ = patchConfig(t, srv, map[string]any{
		"rag": map[string]any{"default_embedding": "other-model:latest"},
	})
	_ = os.Chmod(cfgDir, 0o755)
	if code != http.StatusInternalServerError {
		t.Fatalf("save-failure patch = %d, want 500", code)
	}
	srv.cfgMu.RLock()
	got := srv.cfg.RAG.DefaultEmbedding
	srv.cfgMu.RUnlock()
	if got != "embed-model:latest" {
		t.Fatalf("in-memory default_embedding = %q, want rolled back", got)
	}
}

var externalPNG = []byte{0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A, 0, 0, 0, 0, 0x49, 0x48, 0x44, 0x52}

func makeExternalDB(t *testing.T) []byte {
	t.Helper()
	return makeExternalDBWith(t, []ragpkg.Entry{
		{Term: "alpha", Content: "first", InputMode: "combined",
			CreatedAt: 1000, UpdatedAt: 1000, Embedding: []float64{1, 2, 3},
			Media: []ragpkg.Media{{Type: "image", Name: "logo.png", MIME: "image/png", Data: externalPNG}}},
		{Term: "beta", Content: "second", InputMode: "combined",
			CreatedAt: 2000, UpdatedAt: 2000, Embedding: []float64{4, 5, 6}},
	})
}

func makeExternalDBWith(t *testing.T, entries []ragpkg.Entry) []byte {
	t.Helper()
	dir := t.TempDir()
	_, filename, err := ragpkg.Create(context.Background(), dir, ragpkg.Meta{
		Name: "External Base", EmbeddingModel: "embed-multimodal:latest",
		EmbeddingDigest: "sha256:ext", Dimensions: 3,
	}, entries)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(dir, filename))
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func ragExternalOpenCall(t *testing.T, srv *Server, name string, raw []byte) (int, map[string]any) {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/api/rags/external/open?name="+url.QueryEscape(name), bytes.NewReader(raw))
	req.Header.Set("Content-Type", "application/octet-stream")
	rr := httptest.NewRecorder()
	srv.Routes().ServeHTTP(rr, req)
	var out map[string]any
	_ = json.Unmarshal(rr.Body.Bytes(), &out)
	return rr.Code, out
}

func ragExternalSaveRaw(t *testing.T, srv *Server, filename string, dbBytes []byte, changes string, destination string, extra func(w *multipart.Writer)) (int, http.Header, []byte) {
	t.Helper()
	var buf bytes.Buffer
	wr := multipart.NewWriter(&buf)
	if dbBytes != nil {
		fw, err := wr.CreateFormFile("file", filename)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := fw.Write(dbBytes); err != nil {
			t.Fatal(err)
		}
	}
	if changes != "" {
		if err := wr.WriteField("changes", changes); err != nil {
			t.Fatal(err)
		}
	}
	if destination != "" {
		if err := wr.WriteField("destination", destination); err != nil {
			t.Fatal(err)
		}
	}
	if extra != nil {
		extra(wr)
	}
	if err := wr.Close(); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/rags/external/save", &buf)
	req.Header.Set("Content-Type", wr.FormDataContentType())
	rr := httptest.NewRecorder()
	srv.Routes().ServeHTTP(rr, req)
	return rr.Code, rr.Header(), rr.Body.Bytes()
}

func ragExternalSave(t *testing.T, srv *Server, filename string, dbBytes []byte, changes any, destination string) (int, http.Header, []byte) {
	t.Helper()
	raw, _ := json.Marshal(changes)
	return ragExternalSaveRaw(t, srv, filename, dbBytes, string(raw), destination, nil)
}

func readRAGFromBytes(t *testing.T, raw []byte) *ragpkg.Detail {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "check.db")
	if err := os.WriteFile(path, raw, 0o644); err != nil {
		t.Fatal(err)
	}
	d, err := ragpkg.Get(dir, "check.db")
	if err != nil {
		t.Fatal(err)
	}
	return d
}

func TestRAGExternalOpen(t *testing.T) {
	fake := newFakeOllamaRAG()
	defer fake.Close()
	srv := newTestServer(t, fake.srv.URL)
	dir := ragDirOf(t, srv)

	raw := makeExternalDB(t)
	code, det := ragExternalOpenCall(t, srv, "external.db", raw)
	if code != http.StatusOK {
		t.Fatalf("open status = %d, body = %v", code, det)
	}
	meta, _ := det["meta"].(map[string]any)
	if meta["name"] != "External Base" || meta["embedding_model"] != "embed-multimodal:latest" {
		t.Fatalf("meta = %v", meta)
	}
	entries, _ := det["entries"].([]any)
	if len(entries) != 2 {
		t.Fatalf("entries = %v", det)
	}
	first := entries[0].(map[string]any)
	media, _ := first["media"].([]any)
	if len(media) != 1 {
		t.Fatalf("media = %v", first)
	}
	m := media[0].(map[string]any)
	if m["type"] != "image" || m["base64"] != base64.StdEncoding.EncodeToString(externalPNG) {
		t.Fatalf("external media = %v", m)
	}
	if got := countDBFiles(t, dir); len(got) != 0 {
		t.Fatalf("external open leaked files into managed dir: %v", got)
	}

	if code, _ := ragExternalOpenCall(t, srv, "x.txt", raw); code != http.StatusBadRequest {
		t.Fatalf("bad name status = %d, want 400", code)
	}
	if code, _ := ragExternalOpenCall(t, srv, "broken.db", []byte("not sqlite")); code != http.StatusBadRequest {
		t.Fatalf("broken status = %d, want 400", code)
	}
	if code, _ := ragExternalOpenCall(t, srv, "empty.db", nil); code != http.StatusBadRequest {
		t.Fatalf("empty status = %d, want 400", code)
	}
	if got := countDBFiles(t, dir); len(got) != 0 {
		t.Fatalf("failed opens leaked files: %v", got)
	}
}

func TestRAGExternalSaveDownload(t *testing.T) {
	fake := newFakeOllamaRAG()
	defer fake.Close()
	srv := newTestServer(t, fake.srv.URL)
	dir := ragDirOf(t, srv)

	raw := makeExternalDB(t)
	origCopy := append([]byte(nil), raw...)
	code, det := ragExternalOpenCall(t, srv, "external.db", raw)
	if code != http.StatusOK {
		t.Fatalf("open status = %d", code)
	}
	metaID := det["meta"].(map[string]any)["id"]
	metaCreated := det["meta"].(map[string]any)["created_at"]
	entries, _ := det["entries"].([]any)
	e1 := entries[0].(map[string]any)
	entryID := int64(e1["id"].(float64))
	entry2ID := int64(entries[1].(map[string]any)["id"].(float64))

	changes := map[string]any{
		"name":            "External edited",
		"embedding_model": "embed-multimodal:latest",
		"entries": []any{
			map[string]any{
				"id": entryID, "term": "alpha2", "content": "edited content", "input_mode": "combined",
				"media": []any{map[string]any{"type": "image", "entry_id": entryID, "existing": true}},
			},
			map[string]any{"id": entry2ID, "term": "beta", "content": "second", "input_mode": "combined"},
			map[string]any{"term": "gamma", "content": "new entry"},
		},
	}
	code, headers, body := ragExternalSave(t, srv, "external.db", raw, changes, "download")
	if code != http.StatusOK {
		t.Fatalf("download status = %d, body = %s", code, body)
	}
	if headers.Get("Content-Type") != "application/vnd.sqlite3" ||
		!strings.Contains(headers.Get("Content-Disposition"), "external.db") {
		t.Fatalf("download headers = %v", headers)
	}
	out := readRAGFromBytes(t, body)
	if out.Meta.ID != metaID || out.Meta.CreatedAt != int64(metaCreated.(float64)) {
		t.Fatalf("meta not preserved: %+v", out.Meta)
	}
	if len(out.Entries) != 3 {
		t.Fatalf("entries = %+v", out.Entries)
	}
	got := out.Entries[0]
	if got.Term != "alpha2" || got.Content != "edited content" || got.ID == 0 {
		t.Fatalf("edited entry = %+v", got)
	}
	if got.CreatedAt != 1000 || got.UpdatedAt <= 2000 {
		t.Fatalf("edited entry timestamps = %v/%v", got.CreatedAt, got.UpdatedAt)
	}
	if kept := out.Entries[1]; kept.Term != "beta" || kept.CreatedAt != 2000 || kept.UpdatedAt != 2000 {
		t.Fatalf("unchanged entry timestamps mutated: %+v", kept)
	}
	if len(got.Media) != 1 || got.Media[0].Type != "image" {
		t.Fatalf("media refs = %+v", got.Media)
	}
	stored, err := ragpkg.MediaAt(filepath.Dir(mustWriteTemp(t, body)), "x.db", got.ID, "image")
	if err != nil || !bytes.Equal(stored.Data, externalPNG) {
		t.Fatalf("media bytes = %v %v", stored, err)
	}
	if len(fake.recordedEmbeds()) != 3 {
		t.Fatalf("embeds = %v", fake.recordedEmbeds())
	}
	if got := countDBFiles(t, dir); len(got) != 0 {
		t.Fatalf("download leaked managed files: %v", got)
	}
	if !bytes.Equal(raw, origCopy) {
		t.Fatal("source bytes mutated")
	}
}

func TestRAGExternalSaveDownloadLargeChanges(t *testing.T) {
	fake := newFakeOllamaRAG()
	defer fake.Close()
	srv := newTestServer(t, fake.srv.URL)
	dir := ragDirOf(t, srv)

	raw := makeExternalDB(t)
	changes := `{"name":"big","embedding_model":"embed-multimodal:latest","entries":[{"term":"t","content":"c"}]}` +
		strings.Repeat(" ", 19<<20)
	code, _, body := ragExternalSaveRaw(t, srv, "external.db", raw, changes, "download", nil)
	if code != http.StatusOK {
		t.Fatalf("19MiB changes download status = %d, body = %.200s", code, body)
	}
	out := readRAGFromBytes(t, body)
	if len(out.Entries) != 1 || out.Entries[0].Term != "t" {
		t.Fatalf("entries = %+v", out.Entries)
	}
	if files := countDBFiles(t, dir); len(files) != 0 {
		t.Fatalf("download leaked managed files %v", files)
	}
}

func mustWriteTemp(t *testing.T, raw []byte) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "x.db")
	if err := os.WriteFile(path, raw, 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestRAGExternalSaveLocal(t *testing.T) {
	fake := newFakeOllamaRAG()
	defer fake.Close()
	srv := newTestServer(t, fake.srv.URL)
	dir := ragDirOf(t, srv)

	raw := makeExternalDB(t)
	origCopy := append([]byte(nil), raw...)
	code, det := ragExternalOpenCall(t, srv, "external.db", raw)
	if code != http.StatusOK {
		t.Fatalf("open status = %d", code)
	}
	entryID := int64(det["entries"].([]any)[0].(map[string]any)["id"].(float64))
	changes := map[string]any{
		"name":            "External local",
		"embedding_model": "embed-multimodal:latest",
		"entries": []any{map[string]any{
			"id": entryID, "term": "alpha2", "content": "edited", "input_mode": "combined",
			"media": []any{map[string]any{"type": "image", "entry_id": entryID, "existing": true}},
		}},
	}
	var firstFile string
	for i := 0; i < 2; i++ {
		code, headers, body := ragExternalSave(t, srv, "external.db", raw, changes, "local")
		if code != http.StatusCreated {
			t.Fatalf("save local %d status = %d, body = %s", i, code, body)
		}
		var out map[string]any
		_ = json.Unmarshal(body, &out)
		_ = headers
		info, _ := out["rag"].(map[string]any)
		fn, _ := info["filename"].(string)
		if !ragpkg.ValidFilename(fn) || fn == "external.db" {
			t.Fatalf("imported filename %q", fn)
		}
		if i == 0 {
			firstFile = fn
		} else if fn == firstFile {
			t.Fatalf("second save overwrote %q", fn)
		}
	}
	files := countDBFiles(t, dir)
	if len(files) != 2 {
		t.Fatalf("managed files = %v", files)
	}
	stored, err := ragpkg.Get(dir, firstFile)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Meta.Name != "External local" || len(stored.Entries) != 1 || stored.Entries[0].Term != "alpha2" {
		t.Fatalf("stored = %+v", stored.Meta)
	}
	media, err := ragpkg.MediaAt(dir, firstFile, stored.Entries[0].ID, "image")
	if err != nil || !bytes.Equal(media.Data, externalPNG) {
		t.Fatalf("stored media = %v %v", media, err)
	}
	if !bytes.Equal(raw, origCopy) {
		t.Fatal("source bytes mutated")
	}
}

func TestRAGExternalSaveValidation(t *testing.T) {
	fake := newFakeOllamaRAG()
	defer fake.Close()
	srv := newTestServer(t, fake.srv.URL)
	dir := ragDirOf(t, srv)

	raw := makeExternalDB(t)
	code, det := ragExternalOpenCall(t, srv, "external.db", raw)
	if code != http.StatusOK {
		t.Fatalf("open status = %d", code)
	}
	entryID := int64(det["entries"].([]any)[0].(map[string]any)["id"].(float64))
	valid := map[string]any{
		"name":            "ok",
		"embedding_model": "embed-multimodal:latest",
		"entries":         []any{map[string]any{"id": entryID, "term": "t", "content": "c"}},
	}
	validJSON, _ := json.Marshal(valid)

	cases := []struct {
		name string
		call func() (int, http.Header, []byte)
	}{
		{"bad destination", func() (int, http.Header, []byte) {
			return ragExternalSave(t, srv, "external.db", raw, valid, "nowhere")
		}},
		{"missing file", func() (int, http.Header, []byte) {
			return ragExternalSaveRaw(t, srv, "external.db", nil, string(validJSON), "local", nil)
		}},
		{"missing changes", func() (int, http.Header, []byte) {
			return ragExternalSaveRaw(t, srv, "external.db", raw, "", "local", nil)
		}},
		{"missing destination", func() (int, http.Header, []byte) {
			return ragExternalSaveRaw(t, srv, "external.db", raw, string(validJSON), "", nil)
		}},
		{"duplicate destination", func() (int, http.Header, []byte) {
			return ragExternalSaveRaw(t, srv, "external.db", raw, string(validJSON), "local", func(w *multipart.Writer) {
				_ = w.WriteField("destination", "download")
			})
		}},
		{"extra field", func() (int, http.Header, []byte) {
			return ragExternalSaveRaw(t, srv, "external.db", raw, string(validJSON), "local", func(w *multipart.Writer) {
				_ = w.WriteField("extra", "x")
			})
		}},
		{"malformed changes", func() (int, http.Header, []byte) {
			return ragExternalSaveRaw(t, srv, "external.db", raw, "{not json", "local", nil)
		}},
		{"trailing json", func() (int, http.Header, []byte) {
			return ragExternalSaveRaw(t, srv, "external.db", raw, string(validJSON)+" {}", "local", nil)
		}},
		{"unknown entry id", func() (int, http.Header, []byte) {
			bad := map[string]any{"name": "x", "embedding_model": "embed-multimodal:latest",
				"entries": []any{map[string]any{"id": 9999, "term": "t", "content": "c"}}}
			return ragExternalSave(t, srv, "external.db", raw, bad, "local")
		}},
		{"unknown media ref", func() (int, http.Header, []byte) {
			bad := map[string]any{"name": "x", "embedding_model": "embed-multimodal:latest",
				"entries": []any{map[string]any{"id": entryID, "term": "t", "content": "c",
					"media": []any{map[string]any{"type": "image", "entry_id": 9999, "existing": true}}}}}
			return ragExternalSave(t, srv, "external.db", raw, bad, "local")
		}},
		{"bad filename", func() (int, http.Header, []byte) {
			return ragExternalSave(t, srv, "x.txt", raw, valid, "local")
		}},
		{"not a db", func() (int, http.Header, []byte) {
			return ragExternalSave(t, srv, "external.db", []byte("junk"), valid, "local")
		}},
		{"changes too large", func() (int, http.Header, []byte) {
			return ragExternalSaveRaw(t, srv, "external.db", raw, strings.Repeat(" ", ragMaxBody+1), "local", nil)
		}},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			before := len(fake.recordedEmbeds())
			code, _, body := tt.call()
			if code != http.StatusBadRequest && code != http.StatusRequestEntityTooLarge {
				t.Fatalf("status = %d, body = %s", code, body)
			}
			if len(fake.recordedEmbeds()) != before {
				t.Fatalf("embed ran on invalid request")
			}
			if files := countDBFiles(t, dir); len(files) != 0 {
				t.Fatalf("failure left managed files %v", files)
			}
		})
	}
}

func TestRAGExternalOpenLimits(t *testing.T) {
	fake := newFakeOllamaRAG()
	defer fake.Close()
	srv := newTestServer(t, fake.srv.URL)
	dir := ragDirOf(t, srv)

	many := make([]ragpkg.Entry, ragMaxEntries+1)
	for i := range many {
		many[i] = ragpkg.Entry{Term: "t", Content: "c", InputMode: "combined", Embedding: []float64{1, 2, 3}}
	}
	if code, out := ragExternalOpenCall(t, srv, "many.db", makeExternalDBWith(t, many)); code != http.StatusBadRequest {
		t.Fatalf("101 entries status = %d, body = %v", code, out)
	}

	bigMedia := make([]byte, ragMaxMediaBytes+1)
	copy(bigMedia, externalPNG)
	code, out := ragExternalOpenCall(t, srv, "big.db", makeExternalDBWith(t, []ragpkg.Entry{
		{Term: "t", Content: "c", InputMode: "combined", Embedding: []float64{1, 2, 3},
			Media: []ragpkg.Media{{Type: "image", Name: "big.png", MIME: "image/png", Data: bigMedia}}},
	}))
	if code != http.StatusBadRequest {
		t.Fatalf("oversized media status = %d, body = %v", code, out)
	}

	full := make([]byte, ragMaxMediaBytes)
	copy(full, externalPNG)
	code, out = ragExternalOpenCall(t, srv, "total.db", makeExternalDBWith(t, []ragpkg.Entry{
		{Term: "a", Content: "c", InputMode: "combined", Embedding: []float64{1, 2, 3},
			Media: []ragpkg.Media{{Type: "image", Name: "a.png", MIME: "image/png", Data: full}}},
		{Term: "b", Content: "c", InputMode: "combined", Embedding: []float64{1, 2, 3},
			Media: []ragpkg.Media{{Type: "image", Name: "b.png", MIME: "image/png", Data: full}}},
		{Term: "c", Content: "c", InputMode: "combined", Embedding: []float64{1, 2, 3},
			Media: []ragpkg.Media{{Type: "image", Name: "c.png", MIME: "image/png", Data: []byte{0}}}},
	}))
	if code != http.StatusBadRequest {
		t.Fatalf("oversized total media status = %d, body = %v", code, out)
	}
	if errStr, _ := out["error"].(string); !strings.Contains(errStr, "in total") {
		t.Fatalf("expected total-media error, got %v", out)
	}
	if files := countDBFiles(t, dir); len(files) != 0 {
		t.Fatalf("limit rejections leaked files %v", files)
	}
}

func TestRAGExternalSaveModelFailure(t *testing.T) {
	fake := newFakeOllamaRAG()
	defer fake.Close()
	srv := newTestServer(t, fake.srv.URL)
	dir := ragDirOf(t, srv)

	raw := makeExternalDB(t)
	fake.failAt = 1
	changes := map[string]any{
		"name":            "x",
		"embedding_model": "embed-multimodal:latest",
		"entries":         []any{map[string]any{"term": "t", "content": "c"}},
	}
	code, _, _ := ragExternalSave(t, srv, "external.db", raw, changes, "download")
	if code != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502", code)
	}
	if files := countDBFiles(t, dir); len(files) != 0 {
		t.Fatalf("failure left files %v", files)
	}
}

func TestRAGConfigSurvivesReload(t *testing.T) {
	fake := newFakeOllamaRAG()
	defer fake.Close()
	srv := newTestServer(t, fake.srv.URL)

	code, _ := patchConfig(t, srv, map[string]any{
		"rag": map[string]any{"default_embedding": "embed-model:latest", "directory": "my-bases"},
	})
	if code != http.StatusOK {
		t.Fatalf("patch = %d", code)
	}
	reloaded, err := config.Load(srv.cfg.Path())
	if err != nil {
		t.Fatal(err)
	}
	if reloaded.RAG.DefaultEmbedding != "embed-model:latest" || reloaded.RAG.Directory != "my-bases" {
		t.Fatalf("reloaded rag = %+v", reloaded.RAG)
	}
}
