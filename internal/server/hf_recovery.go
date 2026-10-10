package server

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/gense/ollama-manager/internal/jobs"
	"github.com/gense/ollama-manager/internal/ollama"
)

const (
	hfRecoveryMaxFileBytes  = int64(512) << 30
	hfRecoveryMaxTotalBytes = int64(1) << 40
	hfRecoveryMaxMetaBytes  = int64(8) << 20
	hfRecoveryMaxPages      = 128
	hfRecoveryMetaTimeout   = 15 * time.Second
)

var (
	hfRevisionRegex = regexp.MustCompile(`^[0-9a-fA-F]{40}$`)
	hfSHA256Regex   = regexp.MustCompile(`^[0-9a-fA-F]{64}$`)
	hfRepoSegRegex  = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,190}$`)
	hfPartRegex     = regexp.MustCompile(`(?i)^(.+)-(\d{5})-of-(\d{5})\.gguf$`)
	hfErrURLRegex   = regexp.MustCompile(`https?://[^\s"'<>]+`)
)

var hfRecoveryHTTPClient = &http.Client{
	Timeout: 0,
	CheckRedirect: func(req *http.Request, via []*http.Request) error {
		if len(via) >= 8 {
			return errors.New("too many redirects")
		}
		if !isHFRecoveryURLAllowed(req.URL) {
			return errors.New("redirect to a non-HuggingFace host was blocked")
		}
		if len(via) > 0 && !strings.EqualFold(req.URL.Hostname(), via[0].URL.Hostname()) {
			req.Header.Del("Authorization")
		}
		return nil
	},
}

func isHFRecoveryURLAllowed(u *url.URL) bool {
	if u == nil || !strings.EqualFold(u.Scheme, "https") || u.User != nil {
		return false
	}
	if p := u.Port(); p != "" && p != "443" {
		return false
	}
	host := strings.ToLower(u.Hostname())
	if host == "" || net.ParseIP(host) != nil {
		return false
	}
	return host == "huggingface.co" || strings.HasSuffix(host, ".huggingface.co") ||
		host == "hf.co" || strings.HasSuffix(host, ".hf.co")
}

func parseHFJobName(name string) (repo, tag string, ok bool) {
	raw := strings.TrimSpace(name)
	lower := strings.ToLower(raw)
	for _, p := range []string{"https://", "http://"} {
		if strings.HasPrefix(lower, p) {
			raw = raw[len(p):]
			break
		}
	}
	raw = strings.TrimSpace(raw)
	var rest string
	switch {
	case strings.HasPrefix(strings.ToLower(raw), "huggingface.co/"):
		rest = raw[len("huggingface.co/"):]
	case strings.HasPrefix(strings.ToLower(raw), "hf.co/"):
		rest = raw[len("hf.co/"):]
	default:
		return "", "", false
	}
	if idx := strings.LastIndex(rest, ":"); idx >= 0 {
		tag = rest[idx+1:]
		rest = rest[:idx]
		if tag == "" {
			return "", "", false
		}
	}
	parts := strings.Split(rest, "/")
	if len(parts) != 2 || !hfRepoSegRegex.MatchString(parts[0]) || !hfRepoSegRegex.MatchString(parts[1]) {
		return "", "", false
	}
	if tag != "" {
		if strings.ContainsAny(tag, "/\\") || tag == ".." || strings.HasPrefix(tag, ".") {
			return "", "", false
		}
	}
	return parts[0] + "/" + parts[1], tag, true
}

func validHFFilePath(p string) bool {
	if p == "" || strings.Contains(p, "\\") || strings.HasPrefix(p, "/") {
		return false
	}
	if path.Clean("/"+p) != "/"+p {
		return false
	}
	for _, seg := range strings.Split(p, "/") {
		if seg == "" || seg == "." || seg == ".." {
			return false
		}
	}
	return true
}

type hfTreeEntry struct {
	Type string `json:"type"`
	Path string `json:"path"`
	Size int64  `json:"size"`
	LFS  *struct {
		OID  string `json:"oid"`
		Size int64  `json:"size"`
	} `json:"lfs"`
}

type hfRecoveryFileInfo struct {
	Filename string `json:"filename"`
	Size     int64  `json:"size"`
	Digest   string `json:"digest"`
	Exists   bool   `json:"exists"`
}

type hfRecoveryOption struct {
	Filename     string               `json:"filename"`
	Quant        string               `json:"quant"`
	Files        []hfRecoveryFileInfo `json:"files"`
	TotalBytes   int64                `json:"total_bytes"`
	ReusedBytes  int64                `json:"reused_bytes"`
	MissingBytes int64                `json:"missing_bytes"`
}

type hfRecoveryPreview struct {
	Repo             string               `json:"repo"`
	Revision         string               `json:"revision"`
	SelectedFilename string               `json:"selected_filename,omitempty"`
	Options          []hfRecoveryOption   `json:"options"`
	Projectors       []hfRecoveryFileInfo `json:"projectors"`
	HasToken         bool                 `json:"has_token"`
}

type hfJobError struct {
	status int
	err    error
}

func (e *hfJobError) Error() string { return e.err.Error() }

func hfBoundErr(s string) string {
	s = strings.Map(func(r rune) rune {
		if r == '\n' || r == '\r' || r == '\t' {
			return ' '
		}
		return r
	}, s)
	const max = 300
	if len(s) > max {
		s = s[:max] + "…"
	}
	return strings.TrimSpace(s)
}

func sanitizeHFErr(err error) error {
	if err == nil {
		return nil
	}
	msg := hfErrURLRegex.ReplaceAllString(err.Error(), "[redacted URL]")
	return errors.New(hfBoundErr(msg))
}

func hfStatusError(stage string, code int) error {
	switch code {
	case http.StatusUnauthorized, http.StatusForbidden:
		return fmt.Errorf("HF %s failed (HTTP %d): the repo may be gated or private — check the HuggingFace token in Settings", stage, code)
	case http.StatusNotFound:
		return fmt.Errorf("HF %s failed (HTTP 404): file not found at the pinned revision", stage)
	default:
		return fmt.Errorf("HF %s failed (HTTP %d)", stage, code)
	}
}

func (s *Server) hfRecoveryGet(ctx context.Context, u string) (*http.Response, error) {
	reqCtx, cancel := context.WithTimeout(ctx, hfRecoveryMetaTimeout)
	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, u, nil)
	if err != nil {
		cancel()
		return nil, sanitizeHFErr(err)
	}
	req.Header.Set("User-Agent", "Ollama-Manager/0.1.0")
	s.applyHFAuth(req)
	resp, err := hfRecoveryHTTPClient.Do(req)
	if err != nil {
		cancel()
		return nil, sanitizeHFErr(err)
	}
	resp.Body = &hfRecoveryCancelBody{ReadCloser: resp.Body, cancel: cancel}
	return resp, nil
}

type hfRecoveryCancelBody struct {
	io.ReadCloser
	cancel context.CancelFunc
}

func (b *hfRecoveryCancelBody) Close() error {
	err := b.ReadCloser.Close()
	b.cancel()
	return err
}

func hfNextPageURL(linkHeader, expectPath string) (string, error) {
	for _, part := range strings.Split(linkHeader, ",") {
		part = strings.TrimSpace(part)
		if !strings.Contains(part, `rel="next"`) {
			continue
		}
		start := strings.Index(part, "<")
		end := strings.Index(part, ">")
		if start == -1 || end <= start {
			return "", errors.New("malformed pagination link")
		}
		u, err := url.Parse(part[start+1 : end])
		if err != nil {
			return "", fmt.Errorf("invalid pagination link: %w", err)
		}
		if u.User != nil || !strings.EqualFold(u.Scheme, "https") ||
			!strings.EqualFold(u.Hostname(), "huggingface.co") ||
			(u.Port() != "" && u.Port() != "443") {
			return "", errors.New("pagination link points outside huggingface.co")
		}
		if u.Path != expectPath {
			return "", errors.New("pagination link points to an unexpected path")
		}
		return u.String(), nil
	}
	return "", nil
}

func (s *Server) hfFetchRepoRevision(ctx context.Context, repo string) (string, error) {
	u := fmt.Sprintf("https://huggingface.co/api/models/%s", cleanRepoPath(repo))
	resp, err := s.hfRecoveryGet(ctx, u)
	if err != nil {
		return "", fmt.Errorf("fetch repo metadata: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", hfStatusError("metadata lookup", resp.StatusCode)
	}
	var meta struct {
		ID  string `json:"id"`
		SHA string `json:"sha"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, hfRecoveryMaxMetaBytes)).Decode(&meta); err != nil {
		return "", errors.New("decode repo metadata failed")
	}
	if !hfRevisionRegex.MatchString(meta.SHA) {
		return "", errors.New("repo metadata did not include a valid commit sha")
	}
	return strings.ToLower(meta.SHA), nil
}

func (s *Server) hfFetchTree(ctx context.Context, repo, revision string) ([]hfTreeEntry, error) {
	basePath := fmt.Sprintf("/api/models/%s/tree/%s", cleanRepoPath(repo), url.PathEscape(revision))
	next := "https://huggingface.co" + basePath + "?recursive=true"
	var out []hfTreeEntry
	seen := make(map[string]bool)
	for page := 0; ; page++ {
		if page >= hfRecoveryMaxPages {
			return nil, fmt.Errorf("repo tree exceeds the %d page limit", hfRecoveryMaxPages)
		}
		resp, err := s.hfRecoveryGet(ctx, next)
		if err != nil {
			return nil, fmt.Errorf("fetch repo tree: %w", err)
		}
		if resp.StatusCode != http.StatusOK {
			code := resp.StatusCode
			resp.Body.Close()
			return nil, hfStatusError("tree lookup", code)
		}
		var entries []hfTreeEntry
		decErr := json.NewDecoder(io.LimitReader(resp.Body, hfRecoveryMaxMetaBytes)).Decode(&entries)
		link := resp.Header.Get("Link")
		resp.Body.Close()
		if decErr != nil {
			return nil, errors.New("decode repo tree failed")
		}
		for _, e := range entries {
			if seen[e.Path] {
				return nil, fmt.Errorf("repo tree returned duplicate path %q", e.Path)
			}
			seen[e.Path] = true
			out = append(out, e)
		}
		next = ""
		if link != "" {
			n, err := hfNextPageURL(link, basePath)
			if err != nil {
				return nil, err
			}
			next = n
		}
		if next == "" {
			break
		}
	}
	return out, nil
}

func hfEntryDigest(e hfTreeEntry) string {
	if e.LFS == nil || !hfSHA256Regex.MatchString(e.LFS.OID) {
		return ""
	}
	return strings.ToLower(e.LFS.OID)
}

func splitKeyOf(name string) string {
	base := path.Base(name)
	if m := hfPartRegex.FindStringSubmatch(base); m != nil {
		return name[:len(name)-len(base)] + m[1] + "-of-" + m[3]
	}
	return name
}

func hfRecoveryCandidates(entries []hfTreeEntry) ([]hfRecoveryOption, []hfRecoveryFileInfo, error) {
	type fileInfo struct {
		name   string
		size   int64
		digest string
	}
	var models []fileInfo
	var projs []hfRecoveryFileInfo
	type splitGroup struct {
		files []fileInfo
		total int
	}
	splits := map[string]*splitGroup{}

	for _, e := range entries {
		if e.Type != "file" || !validHFFilePath(e.Path) {
			continue
		}
		base := path.Base(e.Path)
		if !strings.HasSuffix(strings.ToLower(base), ".gguf") {
			continue
		}
		size := e.Size
		if e.LFS != nil && e.LFS.Size > 0 {
			size = e.LFS.Size
		}
		if size <= 0 || size > hfRecoveryMaxFileBytes {
			continue
		}
		digest := hfEntryDigest(e)
		if IsAuxiliaryGGUF(base) {
			continue
		}
		if IsVisionProjector(base) {
			if digest == "" {
				continue
			}
			projs = append(projs, hfRecoveryFileInfo{Filename: e.Path, Size: size, Digest: digest})
			continue
		}
		fi := fileInfo{name: e.Path, size: size, digest: digest}
		if m := hfPartRegex.FindStringSubmatch(base); m != nil {
			key := e.Path[:len(e.Path)-len(base)] + m[1] + "|" + m[3]
			g := splits[key]
			if g == nil {
				g = &splitGroup{}
				splits[key] = g
			}
			g.files = append(g.files, fi)
			var n int
			fmt.Sscanf(m[3], "%d", &n)
			g.total = n
			continue
		}
		models = append(models, fi)
	}

	splitKeys := make([]string, 0, len(splits))
	for k := range splits {
		splitKeys = append(splitKeys, k)
	}
	sort.Strings(splitKeys)
	for _, k := range splitKeys {
		g := splits[k]
		n := g.total
		if n <= 0 || len(g.files) != n {
			return nil, nil, fmt.Errorf("split GGUF %q is incomplete: expected %d contiguous shards, found %d", k, n, len(g.files))
		}
		sort.Slice(g.files, func(a, b int) bool { return g.files[a].name < g.files[b].name })
		stem := k[:strings.LastIndex(k, "|")]
		for i, f := range g.files {
			want := fmt.Sprintf("%s-%05d-of-%05d.gguf", stem, i+1, n)
			if !strings.EqualFold(f.name, want) {
				return nil, nil, fmt.Errorf("split GGUF %q has a non-contiguous shard set (got %q, want %q)", k, f.name, want)
			}
			if f.digest == "" {
				return nil, nil, fmt.Errorf("split GGUF shard %q has no verifiable sha256", f.name)
			}
		}
		models = append(models, g.files...)
	}

	sort.Slice(models, func(a, b int) bool {
		ka, kb := splitKeyOf(models[a].name), splitKeyOf(models[b].name)
		if ka != kb {
			return ka < kb
		}
		return models[a].name < models[b].name
	})
	sort.Slice(projs, func(a, b int) bool { return projs[a].Filename < projs[b].Filename })

	options := make([]hfRecoveryOption, 0)
	seen := map[string]bool{}
	for _, f := range models {
		key := splitKeyOf(f.name)
		if seen[key] {
			continue
		}
		seen[key] = true
		opt := hfRecoveryOption{Filename: f.name, Quant: ExtractQuantization(path.Base(f.name))}
		for _, g := range models {
			if splitKeyOf(g.name) != key {
				continue
			}
			if g.digest == "" {
				opt.Files = nil
				break
			}
			opt.Files = append(opt.Files, hfRecoveryFileInfo{Filename: g.name, Size: g.size, Digest: g.digest})
			opt.TotalBytes += g.size
		}
		if len(opt.Files) == 0 {
			continue
		}
		options = append(options, opt)
	}
	if len(options) == 0 {
		return nil, nil, errors.New("no recoverable GGUF files with verifiable sha256 found in the repo")
	}
	return options, projs, nil
}

func hfSelectOption(options []hfRecoveryOption, tag string) string {
	tag = strings.TrimSpace(tag)
	if tag != "" && !strings.EqualFold(tag, "latest") {
		for _, o := range options {
			if o.Filename == tag {
				return o.Filename
			}
		}
		basenameMatch := ""
		basenameCount := 0
		for _, o := range options {
			if path.Base(o.Filename) == tag {
				basenameMatch = o.Filename
				basenameCount++
			}
		}
		if basenameCount == 1 {
			return basenameMatch
		}
		if basenameCount > 1 {
			return ""
		}
		ciMatch := ""
		ciCount := 0
		for _, o := range options {
			if strings.EqualFold(o.Filename, tag) || strings.EqualFold(path.Base(o.Filename), tag) {
				ciMatch = o.Filename
				ciCount++
			}
		}
		if ciCount == 1 {
			return ciMatch
		}
		if ciCount > 1 {
			return ""
		}
		if tag == "OTHER" {
			return ""
		}
		var matched []hfRecoveryOption
		for _, o := range options {
			if o.Quant != "OTHER" && o.Quant != "AUXILIARY" && o.Quant != "MMPROJ" && strings.EqualFold(o.Quant, tag) {
				matched = append(matched, o)
			}
		}
		if len(matched) == 1 {
			return matched[0].Filename
		}
		return ""
	}
	if len(options) == 1 {
		return options[0].Filename
	}
	return ""
}

func (s *Server) hfMarkExisting(ctx context.Context, p *hfRecoveryPreview) error {
	mark := func(f *hfRecoveryFileInfo) error {
		exists, err := s.ollama.HeadBlob(ctx, "sha256:"+f.Digest)
		if err != nil {
			return fmt.Errorf("checking local blob store: %w", err)
		}
		f.Exists = exists
		return nil
	}
	for oi := range p.Options {
		opt := &p.Options[oi]
		for fi := range opt.Files {
			if err := mark(&opt.Files[fi]); err != nil {
				return err
			}
			if opt.Files[fi].Exists {
				opt.ReusedBytes += opt.Files[fi].Size
			} else {
				opt.MissingBytes += opt.Files[fi].Size
			}
		}
	}
	for i := range p.Projectors {
		if err := mark(&p.Projectors[i]); err != nil {
			return err
		}
	}
	return nil
}

func hfNormalizeModelName(name string) string {
	n := jobs.NormalizePullName(strings.TrimSpace(name))
	lower := strings.ToLower(n)
	for _, p := range []string{"https://", "http://"} {
		if strings.HasPrefix(lower, p) {
			n = n[len(p):]
			lower = strings.ToLower(n)
			break
		}
	}
	if strings.LastIndex(n, ":") < strings.LastIndex(n, "/") {
		n += ":latest"
	}
	return n
}

func (s *Server) hfRecoveryEnsureModelAbsent(ctx context.Context, name string) error {
	models, err := s.ollama.List(ctx)
	if err != nil {
		return fmt.Errorf("checking installed models: %w", sanitizeHFErr(err))
	}
	want := hfNormalizeModelName(name)
	for _, m := range models {
		if strings.EqualFold(hfNormalizeModelName(m.Name), want) ||
			strings.EqualFold(hfNormalizeModelName(m.Model), want) {
			return &hfJobError{http.StatusConflict, fmt.Errorf("model %q is already installed", name)}
		}
	}
	return nil
}

func (s *Server) hfRecoveryJob(id string) (jobs.Job, string, string, error) {
	j, ok := s.jobs.Get(id)
	if !ok {
		return jobs.Job{}, "", "", &hfJobError{http.StatusNotFound, errors.New("job not found")}
	}
	repo, tag, valid := parseHFJobName(j.Name)
	if !valid {
		return jobs.Job{}, "", "", &hfJobError{http.StatusBadRequest, errors.New("job is not a HuggingFace pull")}
	}
	if j.Status != jobs.StatusError {
		return jobs.Job{}, "", "", &hfJobError{http.StatusConflict, fmt.Errorf("job is %s, only failed downloads can be recovered", j.Status)}
	}
	return j, repo, tag, nil
}

func writeHFError(w http.ResponseWriter, err error, fallback int) {
	if he, ok := err.(*hfJobError); ok {
		writeError(w, he.status, he.err)
		return
	}
	writeError(w, fallback, err)
}

func (s *Server) handleHFRecovery(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		writeError(w, http.StatusBadRequest, errors.New("missing id"))
		return
	}
	switch r.Method {
	case http.MethodGet:
		s.handleHFRecoveryPreview(w, r, id)
	case http.MethodPost:
		s.handleHFRecoveryStart(w, r, id)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (s *Server) handleHFRecoveryPreview(w http.ResponseWriter, r *http.Request, id string) {
	_, repo, tag, err := s.hfRecoveryJob(id)
	if err != nil {
		writeHFError(w, err, http.StatusBadRequest)
		return
	}
	preview, err := s.buildHFRecoveryPreview(r.Context(), repo, tag)
	if err != nil {
		writeHFError(w, err, http.StatusBadGateway)
		return
	}
	writeJSON(w, http.StatusOK, preview)
}

func (s *Server) buildHFRecoveryPreview(ctx context.Context, repo, tag string) (*hfRecoveryPreview, error) {
	revision, err := s.hfFetchRepoRevision(ctx, repo)
	if err != nil {
		return nil, err
	}
	entries, err := s.hfFetchTree(ctx, repo, revision)
	if err != nil {
		return nil, err
	}
	options, projs, err := hfRecoveryCandidates(entries)
	if err != nil {
		return nil, err
	}
	preview := &hfRecoveryPreview{
		Repo:       repo,
		Revision:   revision,
		Options:    options,
		Projectors: projs,
		HasToken:   s.hfAuthToken() != "",
	}
	preview.SelectedFilename = hfSelectOption(options, tag)
	if err := s.hfMarkExisting(ctx, preview); err != nil {
		return nil, err
	}
	return preview, nil
}

type hfRecoveryStartBody struct {
	Revision  string `json:"revision"`
	Filename  string `json:"filename"`
	Projector string `json:"projector"`
}

func (s *Server) handleHFRecoveryStart(w http.ResponseWriter, r *http.Request, id string) {
	j, repo, _, err := s.hfRecoveryJob(id)
	if err != nil {
		writeHFError(w, err, http.StatusBadRequest)
		return
	}
	var body hfRecoveryStartBody
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, fmt.Errorf("invalid body: %w", err))
		return
	}
	body.Revision = strings.ToLower(strings.TrimSpace(body.Revision))
	body.Filename = strings.TrimSpace(body.Filename)
	body.Projector = strings.TrimSpace(body.Projector)
	if !hfRevisionRegex.MatchString(body.Revision) {
		writeError(w, http.StatusBadRequest, errors.New("invalid revision"))
		return
	}
	if body.Filename == "" {
		writeError(w, http.StatusBadRequest, errors.New("missing filename"))
		return
	}
	if err := s.hfRecoveryEnsureModelAbsent(r.Context(), j.Name); err != nil {
		writeHFError(w, err, http.StatusBadGateway)
		return
	}
	entries, err := s.hfFetchTree(r.Context(), repo, body.Revision)
	if err != nil {
		writeHFError(w, err, http.StatusBadGateway)
		return
	}
	options, projs, err := hfRecoveryCandidates(entries)
	if err != nil {
		writeHFError(w, err, http.StatusBadGateway)
		return
	}
	var selected *hfRecoveryOption
	for i := range options {
		if options[i].Filename == body.Filename {
			selected = &options[i]
			break
		}
	}
	if selected == nil {
		writeError(w, http.StatusBadRequest, errors.New("selected filename is not a recoverable option at this revision"))
		return
	}
	var projSpec *jobs.HFRecoveryFile
	if body.Projector != "" {
		for i := range projs {
			if projs[i].Filename == body.Projector {
				projSpec = &jobs.HFRecoveryFile{Filename: projs[i].Filename, Size: projs[i].Size, Digest: projs[i].Digest}
				break
			}
		}
		if projSpec == nil {
			writeError(w, http.StatusBadRequest, errors.New("selected projector is not available at this revision"))
			return
		}
	}
	spec := jobs.HFRecoverySpec{
		Repo:      repo,
		Revision:  body.Revision,
		Projector: projSpec,
	}
	var total int64
	for _, f := range selected.Files {
		total += f.Size
		if total > hfRecoveryMaxTotalBytes {
			writeError(w, http.StatusBadRequest, errors.New("selected files exceed the recovery size limit"))
			return
		}
		spec.Files = append(spec.Files, jobs.HFRecoveryFile{Filename: f.Filename, Size: f.Size, Digest: f.Digest})
	}
	if projSpec != nil {
		total += projSpec.Size
		if total > hfRecoveryMaxTotalBytes {
			writeError(w, http.StatusBadRequest, errors.New("selected files exceed the recovery size limit"))
			return
		}
	}
	job, err := s.jobs.Recover(id, spec)
	if err != nil {
		writeError(w, http.StatusConflict, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"job_id": job.ID,
		"status": job.Status,
		"name":   job.Name,
	})
}

func (s *Server) runHFRecovery(ctx context.Context, name string, spec jobs.HFRecoverySpec, onProgress func(ollama.PullProgress) error) error {
	jobRepo, _, nameOK := parseHFJobName(name)
	specRepo, _, specOK := parseHFJobName("huggingface.co/" + spec.Repo)
	if !specOK || !nameOK || !strings.EqualFold(jobRepo, specRepo) ||
		!hfRevisionRegex.MatchString(spec.Revision) || len(spec.Files) == 0 {
		return errors.New("invalid recovery spec")
	}
	token := s.hfAuthToken()

	emit := func(status string, completed, total int64) error {
		if onProgress == nil {
			return nil
		}
		return onProgress(ollama.PullProgress{Status: status, Completed: completed, Total: total})
	}

	all := make([]jobs.HFRecoveryFile, 0, len(spec.Files)+1)
	all = append(all, spec.Files...)
	if spec.Projector != nil {
		all = append(all, *spec.Projector)
	}
	var totalBytes int64
	seenNames := make(map[string]bool, len(all))
	for _, f := range all {
		if f.Size <= 0 || f.Size > hfRecoveryMaxFileBytes || !hfSHA256Regex.MatchString(f.Digest) ||
			!validHFFilePath(f.Filename) || seenNames[f.Filename] {
			return fmt.Errorf("invalid recovery file entry %q", f.Filename)
		}
		seenNames[f.Filename] = true
		totalBytes += f.Size
		if totalBytes > hfRecoveryMaxTotalBytes {
			return errors.New("recovery exceeds the total size limit")
		}
	}

	if err := emit("checking local blobs", 0, totalBytes); err != nil {
		return err
	}
	if err := s.hfRecoveryEnsureModelAbsent(ctx, name); err != nil {
		return err
	}
	var reused int64
	missing := make([]jobs.HFRecoveryFile, 0, len(all))
	for _, f := range all {
		exists, err := s.ollama.HeadBlob(ctx, "sha256:"+f.Digest)
		if err != nil {
			return fmt.Errorf("checking local blob store: %w", err)
		}
		if exists {
			reused += f.Size
			if err := emit("reusing "+path.Base(f.Filename), reused, totalBytes); err != nil {
				return err
			}
			continue
		}
		missing = append(missing, f)
	}

	for _, f := range missing {
		if err := s.hfDownloadAndStore(ctx, spec.Repo, spec.Revision, f, token, totalBytes, reused, emit); err != nil {
			return err
		}
		reused += f.Size
	}

	if err := emit("creating model", totalBytes, totalBytes); err != nil {
		return err
	}
	if err := s.hfRecoveryEnsureModelAbsent(ctx, name); err != nil {
		return err
	}
	files := make(map[string]string, len(all))
	for _, f := range all {
		files[f.Filename] = "sha256:" + f.Digest
	}
	createReq := ollama.CreateRequest{
		Model:      name,
		Files:      files,
		Parameters: map[string]any{"num_ctx": 4096},
	}
	sawSuccess := false
	err := s.ollama.CreateStream(ctx, createReq, func(ev ollama.CreateProgress) error {
		if ev.Status == "success" {
			sawSuccess = true
		}
		if onProgress != nil {
			st := ev.Status
			if st == "" {
				st = "creating model"
			}
			return onProgress(ollama.PullProgress{Status: st, Digest: ev.Digest, Total: ev.Total, Completed: ev.Completed})
		}
		return nil
	})
	if err != nil {
		return err
	}
	if !sawSuccess {
		return errors.New("create ended without a success event")
	}
	return nil
}

func (s *Server) hfDownloadAndStore(ctx context.Context, repo, revision string, f jobs.HFRecoveryFile, token string, totalBytes, alreadyDone int64, emit func(string, int64, int64) error) error {
	encParts := make([]string, 0, 4)
	for _, p := range strings.Split(f.Filename, "/") {
		encParts = append(encParts, url.PathEscape(p))
	}
	u := fmt.Sprintf("https://huggingface.co/%s/resolve/%s/%s",
		cleanRepoPath(repo), url.PathEscape(revision), strings.Join(encParts, "/"))

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return sanitizeHFErr(err)
	}
	req.Header.Set("User-Agent", "Ollama-Manager/0.1.0")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := hfRecoveryHTTPClient.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return fmt.Errorf("downloading %s: %w", path.Base(f.Filename), sanitizeHFErr(err))
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return hfStatusError("download of "+path.Base(f.Filename), resp.StatusCode)
	}

	tmp, err := os.CreateTemp("", "ollama-manager-hfrecv-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	defer tmp.Close()

	base := path.Base(f.Filename)
	if err := emit("downloading "+base, alreadyDone, totalBytes); err != nil {
		return err
	}
	h := sha256.New()
	pr := &progressReader{
		r:     resp.Body,
		total: f.Size,
		onProg: func(completed, total int64) {
			_ = emit("downloading "+base, alreadyDone+completed, totalBytes)
		},
		lastTime: time.Now(),
	}
	n, err := io.Copy(tmp, io.TeeReader(io.LimitReader(pr, f.Size+1), h))
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return fmt.Errorf("downloading %s: %w", base, sanitizeHFErr(err))
	}
	if n > f.Size {
		return fmt.Errorf("downloading %s: received more data than the declared %d bytes", base, f.Size)
	}
	if n != f.Size {
		return fmt.Errorf("downloading %s: truncated body (%d of %d bytes)", base, n, f.Size)
	}
	sum := hex.EncodeToString(h.Sum(nil))
	if !strings.EqualFold(sum, f.Digest) {
		return fmt.Errorf("downloading %s: sha256 mismatch", base)
	}
	if _, err := tmp.Seek(0, io.SeekStart); err != nil {
		return err
	}
	magic := make([]byte, 4)
	if _, err := io.ReadFull(tmp, magic); err != nil || string(magic) != "GGUF" {
		return fmt.Errorf("downloading %s: file is not a GGUF", base)
	}
	if _, err := tmp.Seek(0, io.SeekStart); err != nil {
		return err
	}

	if err := emit("uploading "+base, alreadyDone, totalBytes); err != nil {
		return err
	}
	if err := s.ollama.CreateBlob(ctx, "sha256:"+f.Digest, tmp); err != nil {
		return fmt.Errorf("storing %s in the blob store: %w", base, err)
	}
	return nil
}
