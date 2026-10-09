package server

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/gense/ollama-manager/internal/config"
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
		{http.MethodGet, "/api/rags/models"},
		{http.MethodGet, "/api/rags/x.db"},
		{http.MethodGet, "/api/rags/x.db/download"},
		{http.MethodGet, "/api/rags/x.db/media/1/image"},
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
