package server

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gense/ollama-manager/internal/jobs"
	"github.com/gense/ollama-manager/internal/ollama"
)

const hfRecRev = "0123456789abcdef0123456789abcdef01234567"

type hfReqRecord struct {
	method string
	host   string
	path   string
	auth   string
}

type hfRewriteTransport struct {
	target *url.URL
	mu     sync.Mutex
	reqs   []hfReqRecord
}

func (rt *hfRewriteTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	rt.mu.Lock()
	rt.reqs = append(rt.reqs, hfReqRecord{
		method: req.Method,
		host:   req.URL.Hostname(),
		path:   req.URL.Path,
		auth:   req.Header.Get("Authorization"),
	})
	rt.mu.Unlock()
	r2 := req.Clone(req.Context())
	u := *req.URL
	u.Scheme = rt.target.Scheme
	u.Host = rt.target.Host
	r2.URL = &u
	r2.Host = rt.target.Host
	return http.DefaultTransport.RoundTrip(r2)
}

func (rt *hfRewriteTransport) count(method, pathPrefix string) int {
	rt.mu.Lock()
	defer rt.mu.Unlock()
	n := 0
	for _, r := range rt.reqs {
		if r.method == method && strings.HasPrefix(r.path, pathPrefix) {
			n++
		}
	}
	return n
}

func useHFStub(t *testing.T, target *httptest.Server) *hfRewriteTransport {
	t.Helper()
	u, err := url.Parse(target.URL)
	if err != nil {
		t.Fatal(err)
	}
	rt := &hfRewriteTransport{target: u}
	orig := hfRecoveryHTTPClient
	hfRecoveryHTTPClient = &http.Client{Transport: rt, CheckRedirect: orig.CheckRedirect}
	t.Cleanup(func() { hfRecoveryHTTPClient = orig })
	return rt
}

func ggufFixture(content string) ([]byte, string) {
	data := []byte("GGUF" + content)
	sum := sha256.Sum256(data)
	return data, hex.EncodeToString(sum[:])
}

func hfTE(p string, size int64, oid string) any {
	return map[string]any{
		"type": "file",
		"path": p,
		"size": size,
		"lfs":  map[string]any{"oid": oid, "size": size},
	}
}

type fakeHFConfig struct {
	repo          string
	revision      string
	treePages     [][]any
	badNext       string
	rawLink       string
	alwaysLink    bool
	uniquePerPage bool
	files         map[string][]byte
	statusBy      map[string]int
	redirectTo    map[string]string
}

func fakeHF(t *testing.T, cfg fakeHFConfig) *httptest.Server {
	t.Helper()
	repoPath := "/api/models/" + cfg.repo
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == repoPath:
			writeJSON(w, http.StatusOK, map[string]any{"id": cfg.repo, "sha": cfg.revision})
		case strings.HasPrefix(r.URL.Path, repoPath+"/tree/"):
			page := 0
			if p := r.URL.Query().Get("p"); p != "" {
				fmt.Sscanf(p, "%d", &page)
			}
			if cfg.rawLink != "" && page == 0 {
				w.Header().Set("Link", cfg.rawLink)
			} else if cfg.badNext != "" && page == 0 {
				w.Header().Set("Link", fmt.Sprintf("<%s>; rel=\"next\"", cfg.badNext))
			} else if cfg.alwaysLink || page+1 < len(cfg.treePages) {
				next := fmt.Sprintf("https://huggingface.co%s?recursive=true&p=%d", r.URL.Path, page+1)
				w.Header().Set("Link", fmt.Sprintf("<%s>; rel=\"next\"", next))
			}
			var entries []any
			if cfg.uniquePerPage {
				_, oid := ggufFixture(fmt.Sprintf("page-%d", page))
				entries = []any{hfTE(fmt.Sprintf("m-%03d.gguf", page), 4, oid)}
			} else {
				entries = cfg.treePages[page]
			}
			writeJSON(w, http.StatusOK, entries)
		case strings.Contains(r.URL.Path, "/resolve/") || strings.HasPrefix(r.URL.Path, "/cdn-"):
			if loc, ok := cfg.redirectTo[r.URL.Path]; ok {
				w.Header().Set("Location", loc)
				w.WriteHeader(http.StatusFound)
				return
			}
			if code, ok := cfg.statusBy[r.URL.Path]; ok {
				w.WriteHeader(code)
				return
			}
			for name, data := range cfg.files {
				if strings.HasSuffix(r.URL.Path, "/"+name) {
					w.Header().Set("Content-Type", "application/octet-stream")
					w.WriteHeader(http.StatusOK)
					_, _ = w.Write(data)
					return
				}
			}
			w.WriteHeader(http.StatusNotFound)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

type fakeOllamaRec struct {
	mu          sync.Mutex
	blobs       map[string]bool
	headStatus  map[string]int
	uploads     map[string]int64
	creates     []map[string]any
	deletes     int
	installed   []string
	createEmpty bool
	createError string
	tagsStatus  int
}

func newFakeOllama() *fakeOllamaRec {
	return &fakeOllamaRec{
		blobs:      map[string]bool{},
		headStatus: map[string]int{},
		uploads:    map[string]int64{},
	}
}

func (f *fakeOllamaRec) server(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		switch {
		case r.URL.Path == "/api/pull":
			w.Header().Set("Content-Type", "application/x-ndjson")
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte(`{"error":"registry exploded"}`))
		case r.URL.Path == "/api/tags":
			if f.tagsStatus != 0 {
				w.WriteHeader(f.tagsStatus)
				return
			}
			models := make([]map[string]any, 0, len(f.installed))
			for _, m := range f.installed {
				models = append(models, map[string]any{"name": m})
			}
			writeJSON(w, http.StatusOK, map[string]any{"models": models})
		case r.URL.Path == "/api/create":
			var req map[string]any
			_ = json.NewDecoder(r.Body).Decode(&req)
			f.creates = append(f.creates, req)
			w.Header().Set("Content-Type", "application/x-ndjson")
			if f.createError != "" {
				b, _ := json.Marshal(map[string]string{"error": f.createError})
				_, _ = w.Write(append(b, '\n'))
			} else if !f.createEmpty {
				_, _ = w.Write([]byte(`{"status":"success"}` + "\n"))
			}
		case strings.HasPrefix(r.URL.Path, "/api/blobs/"):
			digest := strings.TrimPrefix(r.URL.Path, "/api/blobs/")
			if r.Method == http.MethodHead {
				if code, ok := f.headStatus[digest]; ok {
					w.WriteHeader(code)
					return
				}
				if f.blobs[digest] {
					w.WriteHeader(http.StatusOK)
				} else {
					w.WriteHeader(http.StatusNotFound)
				}
				return
			}
			var n int64
			buf := make([]byte, 32*1024)
			for {
				m, err := r.Body.Read(buf)
				n += int64(m)
				if err != nil {
					break
				}
			}
			f.uploads[digest] = n
			f.blobs[digest] = true
			w.WriteHeader(http.StatusCreated)
		case r.URL.Path == "/api/delete":
			f.deletes++
			w.WriteHeader(http.StatusOK)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func failHFJobAt(t *testing.T, srv *Server, name string) string {
	t.Helper()
	body := strings.NewReader(fmt.Sprintf(`{"name":%q}`, name))
	req := httptest.NewRequest(http.MethodPost, "/api/pull", body)
	rr := httptest.NewRecorder()
	srv.Routes().ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("pull status = %d body = %s", rr.Code, rr.Body.String())
	}
	var out struct {
		JobID string `json:"job_id"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &out); err != nil || out.JobID == "" {
		t.Fatalf("pull response = %s", rr.Body.String())
	}
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		if j, ok := srv.jobs.Get(out.JobID); ok && j.Status == jobs.StatusError {
			return out.JobID
		}
		time.Sleep(15 * time.Millisecond)
	}
	t.Fatalf("job %s never failed", out.JobID)
	return ""
}

func waitJobDone(t *testing.T, srv *Server, id string, want ...jobs.Status) jobs.Job {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		if j, ok := srv.jobs.Get(id); ok {
			for _, s := range want {
				if j.Status == s {
					return j
				}
			}
		}
		time.Sleep(15 * time.Millisecond)
	}
	j, _ := srv.jobs.Get(id)
	t.Fatalf("job %s never reached %v (now %s, err=%q)", id, want, j.Status, j.Error)
	return jobs.Job{}
}

func getHFPreview(t *testing.T, srv *Server, id string) (int, hfRecoveryPreview) {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/api/jobs/"+id+"/hf-recovery", nil)
	rr := httptest.NewRecorder()
	srv.Routes().ServeHTTP(rr, req)
	var p hfRecoveryPreview
	if rr.Code == http.StatusOK {
		if err := json.Unmarshal(rr.Body.Bytes(), &p); err != nil {
			t.Fatalf("preview decode: %v", err)
		}
	}
	return rr.Code, p
}

func postHFRecover(t *testing.T, srv *Server, id string, body string) (int, map[string]any) {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/api/jobs/"+id+"/hf-recovery", strings.NewReader(body))
	rr := httptest.NewRecorder()
	srv.Routes().ServeHTTP(rr, req)
	var out map[string]any
	_ = json.Unmarshal(rr.Body.Bytes(), &out)
	return rr.Code, out
}

func stdHFCfg(mainName string, mainData []byte, mainOID string, extra ...any) fakeHFConfig {
	entries := []any{hfTE(mainName, int64(len(mainData)), mainOID)}
	entries = append(entries, extra...)
	return fakeHFConfig{
		repo:      "owner/repo",
		revision:  hfRecRev,
		treePages: [][]any{entries},
		files:     map[string][]byte{mainName: mainData},
	}
}

func TestHFRecoveryPreviewAndReuse(t *testing.T) {
	data, oid := ggufFixture("main-model")
	hf := fakeHF(t, stdHFCfg("model-Q4_K_M.gguf", data, oid))
	rt := useHFStub(t, hf)
	oll := newFakeOllama()
	oll.blobs["sha256:"+oid] = true
	srv := newTestServer(t, oll.server(t).URL)

	id := failHFJobAt(t, srv, "huggingface.co/owner/repo:Q4_K_M")
	code, p := getHFPreview(t, srv, id)
	if code != http.StatusOK {
		t.Fatalf("preview status = %d", code)
	}
	if p.Revision != hfRecRev || p.SelectedFilename != "model-Q4_K_M.gguf" || len(p.Options) != 1 {
		t.Fatalf("preview = %+v", p)
	}
	if !p.Options[0].Files[0].Exists || p.Options[0].MissingBytes != 0 {
		t.Fatalf("expected existing blob, got %+v", p.Options[0])
	}

	code, out := postHFRecover(t, srv, id, fmt.Sprintf(`{"revision":%q,"filename":"model-Q4_K_M.gguf"}`, hfRecRev))
	if code != http.StatusOK {
		t.Fatalf("post status = %d body = %v", code, out)
	}
	j := waitJobDone(t, srv, id, jobs.StatusDone)
	if j.Status != jobs.StatusDone {
		t.Fatalf("job status = %s", j.Status)
	}
	if n := rt.count(http.MethodGet, "/owner/repo/resolve/"); n != 0 {
		t.Fatalf("weight GETs = %d, want 0", n)
	}
	oll.mu.Lock()
	defer oll.mu.Unlock()
	if len(oll.uploads) != 0 {
		t.Fatalf("uploads = %v, want none (blob reused)", oll.uploads)
	}
	if len(oll.creates) != 1 {
		t.Fatalf("creates = %d", len(oll.creates))
	}
	files, _ := oll.creates[0]["files"].(map[string]any)
	if files["model-Q4_K_M.gguf"] != "sha256:"+oid || len(files) != 1 {
		t.Fatalf("files map = %v", files)
	}
	if oll.creates[0]["model"] != "huggingface.co/owner/repo:Q4_K_M" {
		t.Fatalf("model = %v", oll.creates[0]["model"])
	}
	if oll.deletes != 0 {
		t.Fatal("recovery must not delete anything")
	}
}

func TestHFRecoveryDownloadsMissingBlob(t *testing.T) {
	data, oid := ggufFixture("missing-main")
	hf := fakeHF(t, stdHFCfg("model-Q4_K_M.gguf", data, oid))
	rt := useHFStub(t, hf)
	oll := newFakeOllama()
	srv := newTestServer(t, oll.server(t).URL)
	srv.cfg.HFToken = "hf_test_token"

	id := failHFJobAt(t, srv, "huggingface.co/owner/repo")
	code, p := getHFPreview(t, srv, id)
	if code != http.StatusOK || !p.HasToken {
		t.Fatalf("preview status=%d hasToken=%v", code, p.HasToken)
	}
	if p.SelectedFilename != "model-Q4_K_M.gguf" || p.Options[0].Files[0].Exists {
		t.Fatalf("preview = %+v", p.Options[0])
	}
	code, out := postHFRecover(t, srv, id, fmt.Sprintf(`{"revision":%q,"filename":"model-Q4_K_M.gguf"}`, hfRecRev))
	if code != http.StatusOK {
		t.Fatalf("post status = %d body = %v", code, out)
	}
	waitJobDone(t, srv, id, jobs.StatusDone)

	var authReq *hfReqRecord
	rt.mu.Lock()
	for i := range rt.reqs {
		if rt.reqs[i].method == http.MethodGet && strings.Contains(rt.reqs[i].path, "/resolve/") {
			authReq = &rt.reqs[i]
		}
	}
	rt.mu.Unlock()
	if authReq == nil || authReq.auth != "Bearer hf_test_token" {
		t.Fatalf("resolve request auth = %+v", authReq)
	}
	oll.mu.Lock()
	defer oll.mu.Unlock()
	if oll.uploads["sha256:"+oid] != int64(len(data)) {
		t.Fatalf("upload = %v, want %d bytes", oll.uploads, len(data))
	}
	if len(oll.creates) != 1 {
		t.Fatalf("creates = %d", len(oll.creates))
	}
}

func TestHFRecoveryProjectorSelected(t *testing.T) {
	mainData, mainOID := ggufFixture("main")
	projData, projOID := ggufFixture("proj")
	cfg := stdHFCfg("model-Q4_K_M.gguf", mainData, mainOID,
		hfTE("mmproj-model-f16.gguf", int64(len(projData)), projOID))
	cfg.files["mmproj-model-f16.gguf"] = projData
	hf := fakeHF(t, cfg)
	rt := useHFStub(t, hf)
	oll := newFakeOllama()
	oll.blobs["sha256:"+mainOID] = true
	srv := newTestServer(t, oll.server(t).URL)

	id := failHFJobAt(t, srv, "huggingface.co/owner/repo:Q4_K_M")
	code, p := getHFPreview(t, srv, id)
	if code != http.StatusOK || len(p.Projectors) != 1 {
		t.Fatalf("preview status=%d projectors=%v", code, p.Projectors)
	}
	code, out := postHFRecover(t, srv, id, fmt.Sprintf(`{"revision":%q,"filename":"model-Q4_K_M.gguf","projector":"mmproj-model-f16.gguf"}`, hfRecRev))
	if code != http.StatusOK {
		t.Fatalf("post status = %d body = %v", code, out)
	}
	waitJobDone(t, srv, id, jobs.StatusDone)
	if n := rt.count(http.MethodGet, "/owner/repo/resolve/hfrec-never"); n != 0 {
		t.Fatal("unexpected resolve")
	}
	if n := rt.count(http.MethodGet, "/owner/repo/resolve/"); n != 1 {
		t.Fatalf("weight GETs = %d, want exactly the projector", n)
	}
	oll.mu.Lock()
	defer oll.mu.Unlock()
	if oll.uploads["sha256:"+projOID] != int64(len(projData)) {
		t.Fatalf("projector upload = %v", oll.uploads)
	}
	files, _ := oll.creates[0]["files"].(map[string]any)
	if files["model-Q4_K_M.gguf"] != "sha256:"+mainOID || files["mmproj-model-f16.gguf"] != "sha256:"+projOID {
		t.Fatalf("files = %v", files)
	}
}

func TestHFRecoveryLargeFileNotProjectorCapped(t *testing.T) {
	oid := strings.Repeat("ab", 32)
	big := int64(9) << 30
	cfg := fakeHFConfig{
		repo:     "owner/repo",
		revision: hfRecRev,
		treePages: [][]any{
			{hfTE("model-Q4_K_M.gguf", big, oid)},
		},
	}
	hf := fakeHF(t, cfg)
	rt := useHFStub(t, hf)
	oll := newFakeOllama()
	oll.blobs["sha256:"+oid] = true
	srv := newTestServer(t, oll.server(t).URL)

	id := failHFJobAt(t, srv, "huggingface.co/owner/repo:Q4_K_M")
	code, p := getHFPreview(t, srv, id)
	if code != http.StatusOK {
		t.Fatalf("preview status = %d (9GiB file should not be rejected)", code)
	}
	if p.Options[0].TotalBytes != big || !p.Options[0].Files[0].Exists {
		t.Fatalf("option = %+v", p.Options[0])
	}
	code, out := postHFRecover(t, srv, id, fmt.Sprintf(`{"revision":%q,"filename":"model-Q4_K_M.gguf"}`, hfRecRev))
	if code != http.StatusOK {
		t.Fatalf("post status = %d body = %v", code, out)
	}
	waitJobDone(t, srv, id, jobs.StatusDone)
	if n := rt.count(http.MethodGet, "/owner/repo/resolve/"); n != 0 {
		t.Fatalf("no weight download expected, got %d", n)
	}
}

func TestHFRecoveryBadDownloadsRejected(t *testing.T) {
	cases := []struct {
		name    string
		content []byte
		declOID string
		declSz  int64
	}{
		{name: "sha mismatch", content: []byte("GGUFxxxx")},
		{name: "truncated", content: []byte("GGUFshort"), declSz: 100},
		{name: "non gguf", content: []byte("NOPEnot-a-gguf")},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			data := tc.content
			sum := sha256.Sum256(data)
			oid := hex.EncodeToString(sum[:])
			if tc.declOID != "" {
				oid = tc.declOID
			}
			if tc.name == "sha mismatch" {
				oid = strings.Repeat("00", 32)
			}
			sz := int64(len(data))
			if tc.declSz != 0 {
				sz = tc.declSz
			}
			cfg := fakeHFConfig{
				repo:      "owner/repo",
				revision:  hfRecRev,
				treePages: [][]any{{hfTE("model.gguf", sz, oid)}},
				files:     map[string][]byte{"model.gguf": data},
			}
			hf := fakeHF(t, cfg)
			useHFStub(t, hf)
			oll := newFakeOllama()
			srv := newTestServer(t, oll.server(t).URL)

			id := failHFJobAt(t, srv, "huggingface.co/owner/repo")
			code, out := postHFRecover(t, srv, id, fmt.Sprintf(`{"revision":%q,"filename":"model.gguf"}`, hfRecRev))
			if code != http.StatusOK {
				t.Fatalf("post status = %d body = %v", code, out)
			}
			j := waitJobDone(t, srv, id, jobs.StatusError)
			if j.Error == "" || j.OriginalError == "" {
				t.Fatalf("expected combined error, got %+v", j)
			}
			oll.mu.Lock()
			defer oll.mu.Unlock()
			if len(oll.uploads) != 0 || len(oll.creates) != 0 {
				t.Fatalf("bad download reached upload/create: %v %v", oll.uploads, oll.creates)
			}
		})
	}
}

func TestHFRecoveryOversizedDownloadRejected(t *testing.T) {
	data, oid := ggufFixture("small")
	cfg := fakeHFConfig{
		repo:      "owner/repo",
		revision:  hfRecRev,
		treePages: [][]any{{hfTE("model.gguf", int64(len(data)), oid)}},
		files:     map[string][]byte{"model.gguf": append(data, "extra-bytes"...)},
	}
	hf := fakeHF(t, cfg)
	useHFStub(t, hf)
	oll := newFakeOllama()
	srv := newTestServer(t, oll.server(t).URL)

	id := failHFJobAt(t, srv, "huggingface.co/owner/repo")
	code, _ := postHFRecover(t, srv, id, fmt.Sprintf(`{"revision":%q,"filename":"model.gguf"}`, hfRecRev))
	if code != http.StatusOK {
		t.Fatalf("post status = %d", code)
	}
	j := waitJobDone(t, srv, id, jobs.StatusError)
	if !strings.Contains(j.Error, "more data") && !strings.Contains(j.Error, "sha256") {
		t.Fatalf("error = %q", j.Error)
	}
	oll.mu.Lock()
	defer oll.mu.Unlock()
	if len(oll.uploads) != 0 {
		t.Fatal("oversized body must not be uploaded")
	}
}

func TestHFRecoveryAuthErrorSanitized(t *testing.T) {
	data, oid := ggufFixture("gated")
	cfg := stdHFCfg("model.gguf", data, oid)
	cfg.statusBy = map[string]int{"/owner/repo/resolve/" + hfRecRev + "/model.gguf": http.StatusUnauthorized}
	hf := fakeHF(t, cfg)
	useHFStub(t, hf)
	oll := newFakeOllama()
	srv := newTestServer(t, oll.server(t).URL)
	srv.cfg.HFToken = "hf_supersecret"

	id := failHFJobAt(t, srv, "huggingface.co/owner/repo")
	code, _ := postHFRecover(t, srv, id, fmt.Sprintf(`{"revision":%q,"filename":"model.gguf"}`, hfRecRev))
	if code != http.StatusOK {
		t.Fatalf("post status = %d", code)
	}
	j := waitJobDone(t, srv, id, jobs.StatusError)
	if !strings.Contains(j.Error, "Settings") || strings.Contains(j.Error, "hf_supersecret") {
		t.Fatalf("error = %q", j.Error)
	}
	oll.mu.Lock()
	defer oll.mu.Unlock()
	if len(oll.uploads) != 0 || len(oll.creates) != 0 {
		t.Fatal("401 must not reach upload/create")
	}
}

func TestHFRecoveryRedirectPolicy(t *testing.T) {
	data, oid := ggufFixture("cdn-body")
	resolvePath := "/owner/repo/resolve/" + hfRecRev + "/model.gguf"

	t.Run("cdn redirect strips auth", func(t *testing.T) {
		cfg := stdHFCfg("model.gguf", data, oid)
		cfg.redirectTo = map[string]string{resolvePath: "https://cdn.hf.co/cdn-owner/repo/model.gguf?sig=abc"}
		hf := fakeHF(t, cfg)
		rt := useHFStub(t, hf)
		oll := newFakeOllama()
		srv := newTestServer(t, oll.server(t).URL)
		srv.cfg.HFToken = "hf_tok"

		id := failHFJobAt(t, srv, "huggingface.co/owner/repo")
		code, _ := postHFRecover(t, srv, id, fmt.Sprintf(`{"revision":%q,"filename":"model.gguf"}`, hfRecRev))
		if code != http.StatusOK {
			t.Fatalf("post status = %d", code)
		}
		j := waitJobDone(t, srv, id, jobs.StatusDone, jobs.StatusError)
		rt.mu.Lock()
		var cdnReq *hfReqRecord
		for i := range rt.reqs {
			if rt.reqs[i].host == "cdn.hf.co" {
				cdnReq = &rt.reqs[i]
			}
		}
		rt.mu.Unlock()
		if cdnReq == nil {
			t.Fatalf("no cdn.hf.co request recorded; job=%+v", j)
		}
		if cdnReq.auth != "" {
			t.Fatalf("Authorization leaked to CDN host: %q", cdnReq.auth)
		}
		if j.Status != jobs.StatusDone {
			t.Fatalf("job = %+v", j)
		}
	})

	t.Run("non-HF redirect blocked", func(t *testing.T) {
		cfg := stdHFCfg("model.gguf", data, oid)
		cfg.redirectTo = map[string]string{resolvePath: "https://evil.example.com/steal"}
		hf := fakeHF(t, cfg)
		rt := useHFStub(t, hf)
		oll := newFakeOllama()
		srv := newTestServer(t, oll.server(t).URL)

		id := failHFJobAt(t, srv, "huggingface.co/owner/repo")
		code, _ := postHFRecover(t, srv, id, fmt.Sprintf(`{"revision":%q,"filename":"model.gguf"}`, hfRecRev))
		if code != http.StatusOK {
			t.Fatalf("post status = %d", code)
		}
		j := waitJobDone(t, srv, id, jobs.StatusError)
		if n := rt.count(http.MethodGet, "/steal"); n != 0 {
			t.Fatal("request to evil host went out")
		}
		if !strings.Contains(j.Error, "HF") {
			t.Fatalf("error = %q", j.Error)
		}
	})

	t.Run("ip literal redirect blocked", func(t *testing.T) {
		cfg := stdHFCfg("model.gguf", data, oid)
		cfg.redirectTo = map[string]string{resolvePath: "https://127.0.0.1:9/x"}
		hf := fakeHF(t, cfg)
		rt := useHFStub(t, hf)
		oll := newFakeOllama()
		srv := newTestServer(t, oll.server(t).URL)

		id := failHFJobAt(t, srv, "huggingface.co/owner/repo")
		code, _ := postHFRecover(t, srv, id, fmt.Sprintf(`{"revision":%q,"filename":"model.gguf"}`, hfRecRev))
		if code != http.StatusOK {
			t.Fatalf("post status = %d", code)
		}
		waitJobDone(t, srv, id, jobs.StatusError)
		if n := rt.count(http.MethodGet, "/x"); n != 0 {
			t.Fatal("request to IP literal went out")
		}
	})
}

func TestHFRecoveryPagination(t *testing.T) {
	d1, o1 := ggufFixture("one")
	d2, o2 := ggufFixture("two")
	cfg := fakeHFConfig{
		repo:     "owner/repo",
		revision: hfRecRev,
		treePages: [][]any{
			{hfTE("a-Q4_K_M.gguf", int64(len(d1)), o1)},
			{hfTE("b-Q8_0.gguf", int64(len(d2)), o2)},
		},
		files: map[string][]byte{"a-Q4_K_M.gguf": d1, "b-Q8_0.gguf": d2},
	}
	hf := fakeHF(t, cfg)
	useHFStub(t, hf)
	oll := newFakeOllama()
	srv := newTestServer(t, oll.server(t).URL)

	id := failHFJobAt(t, srv, "huggingface.co/owner/repo:Q8_0")
	code, p := getHFPreview(t, srv, id)
	if code != http.StatusOK {
		t.Fatalf("preview status = %d", code)
	}
	if len(p.Options) != 2 || p.SelectedFilename != "b-Q8_0.gguf" {
		t.Fatalf("preview = %+v", p)
	}
}

func TestHFRecoveryMaliciousNextLinkRejected(t *testing.T) {
	_, o1 := ggufFixture("one")
	cfg := fakeHFConfig{
		repo:      "owner/repo",
		revision:  hfRecRev,
		treePages: [][]any{{hfTE("a.gguf", 4, o1)}},
		badNext:   "https://evil.example.com/api/models/owner/repo/tree/x",
	}
	hf := fakeHF(t, cfg)
	useHFStub(t, hf)
	oll := newFakeOllama()
	srv := newTestServer(t, oll.server(t).URL)

	id := failHFJobAt(t, srv, "huggingface.co/owner/repo")
	code, _ := getHFPreview(t, srv, id)
	if code != http.StatusOK {
		return
	}
	t.Fatal("malicious next link should fail the preview")
}

func TestParseHFJobName(t *testing.T) {
	ok := []struct{ name, repo, tag string }{
		{"huggingface.co/owner/repo", "owner/repo", ""},
		{"hf.co/owner/repo:Q4_K_M", "owner/repo", "Q4_K_M"},
		{"https://huggingface.co/owner/repo:model-Q8_0.gguf", "owner/repo", "model-Q8_0.gguf"},
	}
	for _, c := range ok {
		repo, tag, good := parseHFJobName(c.name)
		if !good || repo != c.repo || tag != c.tag {
			t.Errorf("parseHFJobName(%q) = %q,%q,%v", c.name, repo, tag, good)
		}
	}
	bad := []string{
		"ollama.com/library/llama3",
		"llama3:8b",
		"huggingface.co/onlyowner",
		"huggingface.co/o/r/extra",
		"https://evil.com/o/r",
		"huggingface.co/../repo",
		"huggingface.co/o/r:../etc/passwd",
		"huggingface.co/o/r:",
		"huggingface.co/o%/r",
		"hf.co",
		"http://hf.co/o/r:tag:with:colons",
	}
	for _, name := range bad {
		if repo, tag, good := parseHFJobName(name); good {
			t.Errorf("parseHFJobName(%q) unexpectedly ok: %q %q", name, repo, tag)
		}
	}
}

func TestHFRecoveryEndpointStateValidation(t *testing.T) {
	data, oid := ggufFixture("v")
	hf := fakeHF(t, stdHFCfg("model.gguf", data, oid))
	useHFStub(t, hf)
	oll := newFakeOllama()
	srv := newTestServer(t, oll.server(t).URL)

	nonHF := failHFJobAt(t, srv, "llama3:latest")
	if code, _ := getHFPreview(t, srv, nonHF); code != http.StatusBadRequest {
		t.Fatalf("non-HF job preview status = %d", code)
	}
	if code, _ := getHFPreview(t, srv, "deadbeef"); code != http.StatusNotFound {
		t.Fatalf("missing job status = %d", code)
	}
	srv.jobs.PauseQueue()
	req := httptest.NewRequest(http.MethodPost, "/api/pull", strings.NewReader(`{"name":"huggingface.co/owner/repo:Q4_K_M"}`))
	rr := httptest.NewRecorder()
	srv.Routes().ServeHTTP(rr, req)
	var out struct {
		JobID string `json:"job_id"`
	}
	_ = json.Unmarshal(rr.Body.Bytes(), &out)
	if code, _ := getHFPreview(t, srv, out.JobID); code != http.StatusConflict {
		t.Fatalf("queued job preview status = %d", code)
	}
	srv.jobs.ResumeQueue()
	waitJobDone(t, srv, out.JobID, jobs.StatusError)
	if code, _ := postHFRecover(t, srv, out.JobID, `{"revision":"zzz","filename":"model.gguf"}`); code != http.StatusBadRequest {
		t.Fatalf("bad revision status = %d", code)
	}
	if code, _ := postHFRecover(t, srv, out.JobID, fmt.Sprintf(`{"revision":%q,"filename":"nope.gguf"}`, hfRecRev)); code != http.StatusBadRequest {
		t.Fatalf("bad filename status = %d", code)
	}
}

func TestHFRecoveryAlreadyInstalledConflict(t *testing.T) {
	data, oid := ggufFixture("installed")
	hf := fakeHF(t, stdHFCfg("model-Q4_K_M.gguf", data, oid))
	useHFStub(t, hf)
	oll := newFakeOllama()
	oll.installed = []string{"huggingface.co/owner/repo:Q4_K_M"}
	srv := newTestServer(t, oll.server(t).URL)

	id := failHFJobAt(t, srv, "huggingface.co/owner/repo:Q4_K_M")
	code, out := postHFRecover(t, srv, id, fmt.Sprintf(`{"revision":%q,"filename":"model-Q4_K_M.gguf"}`, hfRecRev))
	if code != http.StatusConflict {
		t.Fatalf("status = %d body = %v", code, out)
	}
}

func TestHFRecoveryHeadErrorSurfaces(t *testing.T) {
	data, oid := ggufFixture("head")
	hf := fakeHF(t, stdHFCfg("model.gguf", data, oid))
	rt := useHFStub(t, hf)
	oll := newFakeOllama()
	oll.headStatus["sha256:"+oid] = http.StatusInternalServerError
	srv := newTestServer(t, oll.server(t).URL)

	id := failHFJobAt(t, srv, "huggingface.co/owner/repo")
	code, _ := getHFPreview(t, srv, id)
	if code == http.StatusOK {
		t.Fatal("preview should fail when blob store HEAD returns 500")
	}
	if n := rt.count(http.MethodGet, "/owner/repo/resolve/"); n != 0 {
		t.Fatalf("HEAD 500 must not become a re-download (%d GETs)", n)
	}
}

func TestHFRecoverySplitShards(t *testing.T) {
	d1, o1 := ggufFixture("s1")
	d2, o2 := ggufFixture("s2")
	d3, o3 := ggufFixture("s3")
	shard := func(i int, data []byte, oid string) any {
		return hfTE(fmt.Sprintf("big-0000%d-of-00003.gguf", i), int64(len(data)), oid)
	}
	cfg := fakeHFConfig{
		repo:      "owner/repo",
		revision:  hfRecRev,
		treePages: [][]any{{shard(1, d1, o1), shard(2, d2, o2), shard(3, d3, o3)}},
		files:     map[string][]byte{"big-00001-of-00003.gguf": d1, "big-00002-of-00003.gguf": d2, "big-00003-of-00003.gguf": d3},
	}
	hf := fakeHF(t, cfg)
	useHFStub(t, hf)
	oll := newFakeOllama()
	srv := newTestServer(t, oll.server(t).URL)

	id := failHFJobAt(t, srv, "huggingface.co/owner/repo")
	code, p := getHFPreview(t, srv, id)
	if code != http.StatusOK {
		t.Fatalf("preview status = %d", code)
	}
	if len(p.Options) != 1 || p.Options[0].Filename != "big-00001-of-00003.gguf" || len(p.Options[0].Files) != 3 {
		t.Fatalf("split option = %+v", p.Options)
	}
	code, out := postHFRecover(t, srv, id, fmt.Sprintf(`{"revision":%q,"filename":"big-00001-of-00003.gguf"}`, hfRecRev))
	if code != http.StatusOK {
		t.Fatalf("post status = %d body = %v", code, out)
	}
	waitJobDone(t, srv, id, jobs.StatusDone)
	oll.mu.Lock()
	for i, oid := range []string{o1, o2, o3} {
		if _, ok := oll.uploads["sha256:"+oid]; !ok {
			t.Fatalf("shard %d not uploaded: %v", i+1, oll.uploads)
		}
	}
	files, _ := oll.creates[0]["files"].(map[string]any)
	for i := 1; i <= 3; i++ {
		name := fmt.Sprintf("big-0000%d-of-00003.gguf", i)
		if _, ok := files[name]; !ok {
			t.Fatalf("files map missing %s: %v", name, files)
		}
	}
	oll.mu.Unlock()
}

func TestHFRecoverySplitMissingShardFails(t *testing.T) {
	d1, o1 := ggufFixture("s1")
	_, o3 := ggufFixture("s3")
	cfg := fakeHFConfig{
		repo:     "owner/repo",
		revision: hfRecRev,
		treePages: [][]any{{
			hfTE("big-00001-of-00003.gguf", int64(len(d1)), o1),
			hfTE("big-00003-of-00003.gguf", 8, o3),
		}},
	}
	hf := fakeHF(t, cfg)
	useHFStub(t, hf)
	oll := newFakeOllama()
	srv := newTestServer(t, oll.server(t).URL)

	id := failHFJobAt(t, srv, "huggingface.co/owner/repo")
	if code, _ := getHFPreview(t, srv, id); code == http.StatusOK {
		t.Fatal("incomplete split group must fail the preview")
	}
}

func TestHFRecoverySelectionRules(t *testing.T) {
	d1, o1 := ggufFixture("q4")
	d2, o2 := ggufFixture("q8")
	cfg := fakeHFConfig{
		repo:     "owner/repo",
		revision: hfRecRev,
		treePages: [][]any{{
			hfTE("model-Q4_K_M.gguf", int64(len(d1)), o1),
			hfTE("model-Q8_0.gguf", int64(len(d2)), o2),
			hfTE("mmproj-f16.gguf", 10, strings.Repeat("cd", 32)),
			hfTE("model.imatrix.gguf", 10, strings.Repeat("ef", 32)),
			hfTE("draft-q4_0.gguf", 10, strings.Repeat("ab", 32)),
		}},
	}
	hf := fakeHF(t, cfg)
	useHFStub(t, hf)
	oll := newFakeOllama()
	srv := newTestServer(t, oll.server(t).URL)

	id := failHFJobAt(t, srv, "huggingface.co/owner/repo")
	code, p := getHFPreview(t, srv, id)
	if code != http.StatusOK || p.SelectedFilename != "" || len(p.Options) != 2 {
		t.Fatalf("ambiguous preview = %d %+v", code, p)
	}
	if len(p.Projectors) != 1 || p.Projectors[0].Filename != "mmproj-f16.gguf" {
		t.Fatalf("projectors = %+v", p.Projectors)
	}
	id = failHFJobAt(t, srv, "huggingface.co/owner/repo:Q8_0")
	code, p = getHFPreview(t, srv, id)
	if code != http.StatusOK || p.SelectedFilename != "model-Q8_0.gguf" {
		t.Fatalf("quant preview = %d %+v", code, p)
	}
	id = failHFJobAt(t, srv, "huggingface.co/owner/repo:bfg9000")
	_, p = getHFPreview(t, srv, id)
	if p.SelectedFilename != "" {
		t.Fatalf("unknown tag selected %q", p.SelectedFilename)
	}
	id = failHFJobAt(t, srv, "huggingface.co/owner/repo:model-Q4_K_M.gguf")
	_, p = getHFPreview(t, srv, id)
	if p.SelectedFilename != "model-Q4_K_M.gguf" {
		t.Fatalf("filename tag preview = %+v", p)
	}
}

func TestHFRecoveryNoVerifiableSHA(t *testing.T) {
	cfg := fakeHFConfig{
		repo:     "owner/repo",
		revision: hfRecRev,
		treePages: [][]any{{
			map[string]any{"type": "file", "path": "model.gguf", "size": 100, "xetHash": "deadbeef"},
			map[string]any{"type": "file", "path": "model2.gguf", "size": 100, "oid": "notasha"},
		}},
	}
	hf := fakeHF(t, cfg)
	useHFStub(t, hf)
	oll := newFakeOllama()
	srv := newTestServer(t, oll.server(t).URL)

	id := failHFJobAt(t, srv, "huggingface.co/owner/repo")
	if code, _ := getHFPreview(t, srv, id); code == http.StatusOK {
		t.Fatal("preview without any verifiable sha256 should fail")
	}
}

func TestHFRecoveryCreateWithoutSuccessFails(t *testing.T) {
	data, oid := ggufFixture("nosuccess")
	hf := fakeHF(t, stdHFCfg("model.gguf", data, oid))
	useHFStub(t, hf)
	oll := newFakeOllama()
	oll.createEmpty = true
	oll.blobs["sha256:"+oid] = true
	srv := newTestServer(t, oll.server(t).URL)

	id := failHFJobAt(t, srv, "huggingface.co/owner/repo")
	code, _ := postHFRecover(t, srv, id, fmt.Sprintf(`{"revision":%q,"filename":"model.gguf"}`, hfRecRev))
	if code != http.StatusOK {
		t.Fatalf("post status = %d", code)
	}
	j := waitJobDone(t, srv, id, jobs.StatusError)
	if !strings.Contains(j.Error, "success") {
		t.Fatalf("error = %q", j.Error)
	}
}

func TestHFRecoveryRequiresAuth(t *testing.T) {
	data, oid := ggufFixture("auth")
	hf := fakeHF(t, stdHFCfg("model.gguf", data, oid))
	useHFStub(t, hf)
	oll := newFakeOllama()
	srv := newTestServer(t, oll.server(t).URL)
	srv.cfg.PasswordHash = "$2a$10$0123456789abcdef0123456789abcdef0123456789abcdef"

	req := httptest.NewRequest(http.MethodGet, "/api/jobs/whatever/hf-recovery", nil)
	rr := httptest.NewRecorder()
	srv.Routes().ServeHTTP(rr, req)
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("GET status = %d", rr.Code)
	}
	req = httptest.NewRequest(http.MethodPost, "/api/jobs/whatever/hf-recovery", strings.NewReader(`{}`))
	rr = httptest.NewRecorder()
	srv.Routes().ServeHTTP(rr, req)
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("POST status = %d", rr.Code)
	}
}

func TestHFRecoveryInstalledGuard(t *testing.T) {
	data, oid := ggufFixture("guard")
	mk := func() (*Server, *hfRewriteTransport, *fakeOllamaRec) {
		hf := fakeHF(t, stdHFCfg("model.gguf", data, oid))
		rt := useHFStub(t, hf)
		oll := newFakeOllama()
		return newTestServer(t, oll.server(t).URL), rt, oll
	}
	body := fmt.Sprintf(`{"revision":%q,"filename":"model.gguf"}`, hfRecRev)

	t.Run("tags 500 aborts with 502", func(t *testing.T) {
		srv, rt, oll := mk()
		oll.tagsStatus = http.StatusInternalServerError
		id := failHFJobAt(t, srv, "huggingface.co/owner/repo")
		if code, _ := postHFRecover(t, srv, id, body); code != http.StatusBadGateway {
			t.Fatalf("status = %d", code)
		}
		if n := rt.count(http.MethodGet, "/owner/repo/resolve/"); n != 0 {
			t.Fatalf("weight GETs = %d", n)
		}
	})
	t.Run("untagged vs installed latest", func(t *testing.T) {
		srv, _, oll := mk()
		oll.installed = []string{"huggingface.co/owner/repo:latest"}
		id := failHFJobAt(t, srv, "huggingface.co/owner/repo")
		if code, _ := postHFRecover(t, srv, id, body); code != http.StatusConflict {
			t.Fatalf("status = %d", code)
		}
	})
	t.Run("hf.co alias", func(t *testing.T) {
		srv, _, oll := mk()
		oll.installed = []string{"hf.co/owner/repo:latest"}
		id := failHFJobAt(t, srv, "huggingface.co/owner/repo")
		if code, _ := postHFRecover(t, srv, id, body); code != http.StatusConflict {
			t.Fatalf("status = %d", code)
		}
	})
	t.Run("runner rechecks before downloads", func(t *testing.T) {
		srv, rt, oll := mk()
		id := failHFJobAt(t, srv, "huggingface.co/owner/repo")
		srv.jobs.PauseQueue()
		if code, out := postHFRecover(t, srv, id, body); code != http.StatusOK {
			t.Fatalf("post = %d %v", code, out)
		}
		oll.mu.Lock()
		oll.installed = []string{"huggingface.co/owner/repo:latest"}
		oll.mu.Unlock()
		srv.jobs.ResumeQueue()
		j := waitJobDone(t, srv, id, jobs.StatusError)
		if !strings.Contains(j.Error, "already installed") {
			t.Fatalf("error = %q", j.Error)
		}
		if n := rt.count(http.MethodGet, "/owner/repo/resolve/"); n != 0 {
			t.Fatalf("weight GETs = %d", n)
		}
	})
}

func TestHFRecoveryRunnerRejectsBadSpec(t *testing.T) {
	data, oid := ggufFixture("spec")
	hf := fakeHF(t, stdHFCfg("model.gguf", data, oid))
	rt := useHFStub(t, hf)
	oll := newFakeOllama()
	srv := newTestServer(t, oll.server(t).URL)
	progress := func(ollama.PullProgress) error { return nil }

	for _, spec := range []jobs.HFRecoverySpec{
		{Repo: "other/repo", Revision: hfRecRev, Files: []jobs.HFRecoveryFile{{Filename: "model.gguf", Size: int64(len(data)), Digest: oid}}},
		{Repo: "../evil", Revision: hfRecRev, Files: []jobs.HFRecoveryFile{{Filename: "model.gguf", Size: int64(len(data)), Digest: oid}}},
		{Repo: "owner/repo", Revision: "nothex", Files: []jobs.HFRecoveryFile{{Filename: "model.gguf", Size: int64(len(data)), Digest: oid}}},
		{Repo: "owner/repo", Revision: hfRecRev, Files: []jobs.HFRecoveryFile{{Filename: "model.gguf", Size: -1, Digest: oid}}},
		{Repo: "owner/repo", Revision: hfRecRev, Files: []jobs.HFRecoveryFile{{Filename: "model.gguf", Size: 1, Digest: "zz"}}},
		{Repo: "owner/repo", Revision: hfRecRev, Files: []jobs.HFRecoveryFile{{Filename: "a/../b.gguf", Size: 1, Digest: oid}}},
		{Repo: "owner/repo", Revision: hfRecRev, Files: []jobs.HFRecoveryFile{
			{Filename: "model.gguf", Size: 1, Digest: oid},
			{Filename: "model.gguf", Size: 2, Digest: oid},
		}},
	} {
		if err := srv.runHFRecovery(context.Background(), "huggingface.co/owner/repo", spec, progress); err == nil {
			t.Fatalf("bad spec accepted: %+v", spec)
		}
	}
	rt.mu.Lock()
	defer rt.mu.Unlock()
	for _, r := range rt.reqs {
		if strings.Contains(r.path, "/resolve/") {
			t.Fatalf("corrupted spec caused a weight request: %v", r)
		}
	}
}

func TestHFRecoveryPaginationFailures(t *testing.T) {
	mkSrv := func(cfg fakeHFConfig) *Server {
		hf := fakeHF(t, cfg)
		useHFStub(t, hf)
		oll := newFakeOllama()
		return newTestServer(t, oll.server(t).URL)
	}
	_, o1 := ggufFixture("one")

	t.Run("cap reached with continuation", func(t *testing.T) {
		srv := mkSrv(fakeHFConfig{repo: "owner/repo", revision: hfRecRev, alwaysLink: true, uniquePerPage: true})
		id := failHFJobAt(t, srv, "huggingface.co/owner/repo")
		if code, _ := getHFPreview(t, srv, id); code == http.StatusOK {
			t.Fatal("128 continuing pages must fail")
		}
	})
	t.Run("duplicate path", func(t *testing.T) {
		srv := mkSrv(fakeHFConfig{repo: "owner/repo", revision: hfRecRev,
			treePages: [][]any{{hfTE("model.gguf", 4, o1)}, {hfTE("model.gguf", 4, o1)}}})
		id := failHFJobAt(t, srv, "huggingface.co/owner/repo")
		if code, _ := getHFPreview(t, srv, id); code == http.StatusOK {
			t.Fatal("duplicate path must fail")
		}
	})
	t.Run("malformed next relation", func(t *testing.T) {
		srv := mkSrv(fakeHFConfig{repo: "owner/repo", revision: hfRecRev,
			treePages: [][]any{{hfTE("model.gguf", 4, o1)}}, rawLink: `rel="next"`})
		id := failHFJobAt(t, srv, "huggingface.co/owner/repo")
		if code, _ := getHFPreview(t, srv, id); code == http.StatusOK {
			t.Fatal("malformed rel=next must fail")
		}
	})
	t.Run("next link userinfo", func(t *testing.T) {
		srv := mkSrv(fakeHFConfig{repo: "owner/repo", revision: hfRecRev,
			treePages: [][]any{{hfTE("model.gguf", 4, o1)}},
			rawLink:   fmt.Sprintf(`<https://u:p@huggingface.co/api/models/owner/repo/tree/%s?recursive=true&p=1>; rel="next"`, hfRecRev)})
		id := failHFJobAt(t, srv, "huggingface.co/owner/repo")
		if code, _ := getHFPreview(t, srv, id); code == http.StatusOK {
			t.Fatal("userinfo next link must fail")
		}
	})
	t.Run("next link non-443 port", func(t *testing.T) {
		srv := mkSrv(fakeHFConfig{repo: "owner/repo", revision: hfRecRev,
			treePages: [][]any{{hfTE("model.gguf", 4, o1)}},
			rawLink:   fmt.Sprintf(`<https://huggingface.co:8443/api/models/owner/repo/tree/%s?recursive=true&p=1>; rel="next"`, hfRecRev)})
		id := failHFJobAt(t, srv, "huggingface.co/owner/repo")
		if code, _ := getHFPreview(t, srv, id); code == http.StatusOK {
			t.Fatal("non-443 next link must fail")
		}
	})
	t.Run("redirect userinfo blocked", func(t *testing.T) {
		data, oid := ggufFixture("r")
		resolvePath := "/owner/repo/resolve/" + hfRecRev + "/model.gguf"
		cfg := stdHFCfg("model.gguf", data, oid)
		cfg.redirectTo = map[string]string{resolvePath: "https://u:p@cdn.hf.co/x"}
		srv := mkSrv(cfg)
		id := failHFJobAt(t, srv, "huggingface.co/owner/repo")
		postHFRecover(t, srv, id, fmt.Sprintf(`{"revision":%q,"filename":"model.gguf"}`, hfRecRev))
		waitJobDone(t, srv, id, jobs.StatusError)
	})
	t.Run("redirect non-443 port blocked", func(t *testing.T) {
		data, oid := ggufFixture("r")
		resolvePath := "/owner/repo/resolve/" + hfRecRev + "/model.gguf"
		cfg := stdHFCfg("model.gguf", data, oid)
		cfg.redirectTo = map[string]string{resolvePath: "https://cdn.hf.co:8443/x"}
		srv := mkSrv(cfg)
		id := failHFJobAt(t, srv, "huggingface.co/owner/repo")
		postHFRecover(t, srv, id, fmt.Sprintf(`{"revision":%q,"filename":"model.gguf"}`, hfRecRev))
		waitJobDone(t, srv, id, jobs.StatusError)
	})
	t.Run("redirect http blocked", func(t *testing.T) {
		data, oid := ggufFixture("r")
		resolvePath := "/owner/repo/resolve/" + hfRecRev + "/model.gguf"
		cfg := stdHFCfg("model.gguf", data, oid)
		cfg.redirectTo = map[string]string{resolvePath: "http://cdn.hf.co/x"}
		srv := mkSrv(cfg)
		id := failHFJobAt(t, srv, "huggingface.co/owner/repo")
		postHFRecover(t, srv, id, fmt.Sprintf(`{"revision":%q,"filename":"model.gguf"}`, hfRecRev))
		waitJobDone(t, srv, id, jobs.StatusError)
	})
}

func TestHFRecoverySanitizeError(t *testing.T) {
	uerr := &url.Error{Op: "Get", URL: "https://cdn.hf.co/x?sig=SECRET&token=hf_tok", Err: errors.New("refused")}
	msg := sanitizeHFErr(uerr).Error()
	if strings.Contains(msg, "SECRET") || strings.Contains(msg, "hf_tok") || strings.Contains(msg, "cdn.hf.co") {
		t.Fatalf("url.Error leaked: %q", msg)
	}
	if !strings.Contains(msg, "refused") {
		t.Fatalf("lost cause: %q", msg)
	}
	msg = sanitizeHFErr(errors.New(`Get "https://huggingface.co/x?sig=SECRET": EOF`)).Error()
	if strings.Contains(msg, "SECRET") || !strings.Contains(msg, "[redacted URL]") {
		t.Fatalf("generic error leaked: %q", msg)
	}
	msg = hfStatusError("x", http.StatusUnauthorized).Error()
	if !strings.Contains(msg, "Settings") {
		t.Fatalf("401 guidance missing: %q", msg)
	}
}

func TestHFRecoverySplitSortDeterministic(t *testing.T) {
	var entries []any
	for i := 4; i >= 1; i-- {
		for s := 3; s >= 1; s-- {
			_, oid := ggufFixture(fmt.Sprintf("g%d-s%d", i, s))
			entries = append(entries, hfTE(fmt.Sprintf("grp%d-0000%d-of-00003.gguf", i, s), 4, oid))
		}
	}
	hf := fakeHF(t, fakeHFConfig{repo: "owner/repo", revision: hfRecRev, treePages: [][]any{entries}})
	useHFStub(t, hf)
	oll := newFakeOllama()
	srv := newTestServer(t, oll.server(t).URL)

	id := failHFJobAt(t, srv, "huggingface.co/owner/repo")
	code, p := getHFPreview(t, srv, id)
	if code != http.StatusOK || len(p.Options) != 4 {
		t.Fatalf("preview = %d %+v", code, p.Options)
	}
	for i, o := range p.Options {
		want := fmt.Sprintf("grp%d-00001-of-00003.gguf", i+1)
		if o.Filename != want {
			t.Fatalf("option %d = %q, want %q", i, o.Filename, want)
		}
		for s, f := range o.Files {
			wantF := fmt.Sprintf("grp%d-0000%d-of-00003.gguf", i+1, s+1)
			if f.Filename != wantF {
				t.Fatalf("option %d file %d = %q, want %q", i, s, f.Filename, wantF)
			}
		}
	}
}

func TestHFRecoverySelectOptionRules(t *testing.T) {
	opt := func(name, quant string) hfRecoveryOption {
		return hfRecoveryOption{Filename: name, Quant: quant}
	}
	opts := []hfRecoveryOption{
		opt("a/model-Q4_K_M.gguf", "Q4_K_M"),
		opt("b/model-Q4_K_M.gguf", "Q4_K_M"),
		opt("model-Q8_0.gguf", "Q8_0"),
	}
	if got := hfSelectOption(opts, "a/model-Q4_K_M.gguf"); got != "a/model-Q4_K_M.gguf" {
		t.Fatalf("exact path = %q", got)
	}
	if got := hfSelectOption(opts, "model-Q4_K_M.gguf"); got != "" {
		t.Fatalf("ambiguous basename picked %q", got)
	}
	if got := hfSelectOption(opts, "MODEL-Q8_0.GGUF"); got != "model-Q8_0.gguf" {
		t.Fatalf("unique case-insensitive basename = %q", got)
	}
	if got := hfSelectOption(opts, "OTHER"); got != "" {
		t.Fatalf("OTHER tag picked %q", got)
	}
	other := []hfRecoveryOption{opt("model-oddball.gguf", "OTHER")}
	if got := hfSelectOption(other, "OTHER"); got != "" {
		t.Fatalf("OTHER tag picked %q", got)
	}
	if got := hfSelectOption(other, ""); got != "model-oddball.gguf" {
		t.Fatalf("unique default = %q", got)
	}
	aux := []hfRecoveryOption{opt("model-oddball.gguf", "OTHER"), opt("aux.gguf", "AUXILIARY")}
	if got := hfSelectOption(aux, "Q4_K_M"); got != "" {
		t.Fatalf("quant tag picked %q", got)
	}
	if got := hfSelectOption(aux, "AUXILIARY"); got != "" {
		t.Fatalf("AUXILIARY tag picked %q", got)
	}
}

func TestHFRecoveryUniqueOtherDefault(t *testing.T) {
	data, oid := ggufFixture("oddball")
	hf := fakeHF(t, fakeHFConfig{
		repo:      "owner/repo",
		revision:  hfRecRev,
		treePages: [][]any{{hfTE("model-exotic.gguf", int64(len(data)), oid), hfTE("imatrix.gguf", 8, strings.Repeat("ab", 32))}},
		files:     map[string][]byte{"model-exotic.gguf": data},
	})
	useHFStub(t, hf)
	oll := newFakeOllama()
	srv := newTestServer(t, oll.server(t).URL)

	id := failHFJobAt(t, srv, "huggingface.co/owner/repo")
	code, p := getHFPreview(t, srv, id)
	if code != http.StatusOK || len(p.Options) != 1 || p.SelectedFilename != "model-exotic.gguf" {
		t.Fatalf("preview = %d %+v", code, p)
	}
}

func TestHFRecoveryCreateErrorFails(t *testing.T) {
	data, oid := ggufFixture("c")
	hf := fakeHF(t, stdHFCfg("model.gguf", data, oid))
	useHFStub(t, hf)
	oll := newFakeOllama()
	oll.blobs["sha256:"+oid] = true
	oll.createError = "unsupported architecture"
	srv := newTestServer(t, oll.server(t).URL)

	id := failHFJobAt(t, srv, "huggingface.co/owner/repo")
	code, _ := postHFRecover(t, srv, id, fmt.Sprintf(`{"revision":%q,"filename":"model.gguf"}`, hfRecRev))
	if code != http.StatusOK {
		t.Fatalf("post = %d", code)
	}
	j := waitJobDone(t, srv, id, jobs.StatusError)
	if !strings.Contains(j.Error, "unsupported architecture") || j.OriginalError == "" {
		t.Fatalf("job = %+v", j)
	}
}
