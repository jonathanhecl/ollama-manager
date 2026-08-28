package server

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// HFModelSummary represents a model returned from the search endpoint.
type HFModelSummary struct {
	ID           string    `json:"id"`
	Author       string    `json:"author"`
	Name         string    `json:"name"`
	Downloads    int       `json:"downloads"`
	Likes        int       `json:"likes"`
	LastModified time.Time `json:"last_modified"`
	Tags         []string  `json:"tags"`
	PipelineTag  string    `json:"pipeline_tag"`
	IsPrivate    bool      `json:"private"`
	HasGGUF      bool      `json:"has_gguf"`
	HasOllama    bool      `json:"has_ollama"`
	HasVision    bool      `json:"has_vision"`
	GGUFCount    int       `json:"gguf_count"`
}

// HFQuantFile represents a single GGUF or mmproj file in a repository.
type HFQuantFile struct {
	Filename      string `json:"filename"`
	Quant         string `json:"quant"`
	SizeBytes     int64  `json:"size_bytes"`
	IsVisionProj  bool   `json:"is_vision_proj"`
	PullName      string `json:"pull_name"`
	IsInstalled   bool   `json:"is_installed"`
	IsDownloading bool   `json:"is_downloading"`
}

// HFModelDetail contains detailed repository metadata and files.
type HFModelDetail struct {
	ID             string        `json:"id"`
	Author         string        `json:"author"`
	Name           string        `json:"name"`
	Downloads      int           `json:"downloads"`
	Likes          int           `json:"likes"`
	LastModified   time.Time     `json:"last_modified"`
	Tags           []string      `json:"tags"`
	PipelineTag    string        `json:"pipeline_tag"`
	License        string        `json:"license,omitempty"`
	Description    string        `json:"description,omitempty"`
	GGUFFiles      []HFQuantFile `json:"gguf_files"`
	VisionFiles    []HFQuantFile `json:"vision_files"`
	HasVision      bool          `json:"has_vision"`
	TotalGGUFCount int           `json:"total_gguf_count"`
	SuggestedQuant string        `json:"suggested_quant,omitempty"`
}

var (
	quantRegex     = regexp.MustCompile(`(?i)(?:^|[-._])(q[0-9]+_[a-z0-9_]+|q[0-9]+_[0-9]+|q[0-9]+|iq[0-9]+_[a-z0-9_]+|ud-iq[0-9]+_[a-z0-9_]+|f16|f32|bf16)(?:[-._]|$)`)
	mmprojRegex    = regexp.MustCompile(`(?i)mmproj`)
	imatrixRegex   = regexp.MustCompile(`(?i)(?:^|[-._])imatrix(?:[-._]|$)`)
	// The leading [a-z]* catches vendor-prefixed variants such as "FastMTP" or
	// "SpecDraft"; the trailing digit allows suffixes like "FastMTP32K".
	mtpRegex   = regexp.MustCompile(`(?i)(?:^|[-._])[a-z]*mtp(?:[-._0-9]|$)`)
	draftRegex = regexp.MustCompile(`(?i)(?:^|[-._])[a-z]*draft(?:[-._0-9]|$)`)
	paramSizeRegex = regexp.MustCompile(`(?i)(?:^|[-._])([0-9]+(?:\.[0-9]+)?[bm])(?:[-._]|$)`)
)

// IsImatrixFile checks if the filename corresponds to an imatrix calibration data file.
func IsImatrixFile(filename string) bool {
	lower := strings.ToLower(filename)
	return imatrixRegex.MatchString(lower) || strings.Contains(lower, "imatrix")
}

// IsAuxiliaryGGUF checks if the file is an auxiliary, draft, or calibration file (imatrix, mtp, draft).
func IsAuxiliaryGGUF(filename string) bool {
	lower := strings.ToLower(filename)
	if IsImatrixFile(lower) {
		return true
	}
	return mtpRegex.MatchString(lower) || draftRegex.MatchString(lower)
}

// ExtractQuantization extracts the quant name from a GGUF filename.
func ExtractQuantization(filename string) string {
	lower := strings.ToLower(filename)
	// Remove .gguf extension
	base := strings.TrimSuffix(lower, ".gguf")

	// Check if this is an auxiliary non-standalone file (imatrix, mtp, draft)
	if IsAuxiliaryGGUF(base) {
		return "AUXILIARY"
	}

	// Check if this is a vision mmproj file
	if mmprojRegex.MatchString(base) {
		return "MMPROJ"
	}

	matches := quantRegex.FindStringSubmatch(base)
	if len(matches) > 1 {
		return strings.ToUpper(matches[1])
	}

	// Fallback to searching common quant patterns if prefix matching missed it
	for _, q := range []string{
		"q4_k_m", "q4_k_s", "q4_0", "q4_1",
		"q5_k_m", "q5_k_s", "q5_0", "q5_1",
		"q8_0", "q6_k", "q3_k_l", "q3_k_m", "q3_k_s",
		"q2_k", "iq4_xs", "iq4_nl", "iq3_m", "iq3_s", "iq3_xxs",
		"iq2_m", "iq2_s", "iq2_xxs", "iq1_s", "iq1_m",
		"f16", "f32", "bf16",
	} {
		if strings.Contains(base, q) {
			return strings.ToUpper(q)
		}
	}

	return "OTHER"
}

// IsVisionProjector checks if the filename corresponds to a multimodal vision projector.
func IsVisionProjector(filename string) bool {
	return mmprojRegex.MatchString(filename)
}

// handleHFSearch handles GET /api/hf/search
// Query params:
//   - q: search query (e.g. "qwen", "llama")
//   - sort: "downloads" (default), "likes", "lastModified", "trending"
//   - limit: max items (default 30, max 100)
//   - cursor: opaque cursor for next-page pagination (from previous response)
func (s *Server) handleHFSearch(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	query := strings.TrimSpace(r.URL.Query().Get("q"))
	sortParam := strings.TrimSpace(r.URL.Query().Get("sort"))
	if sortParam == "" {
		sortParam = "trending"
	}
	limitParam := strings.TrimSpace(r.URL.Query().Get("limit"))
	limit := 30
	if n, err := strconv.Atoi(limitParam); err == nil && n > 0 && n <= 100 {
		limit = n
	}

	filterParam := strings.TrimSpace(r.URL.Query().Get("filter"))
	cursor := strings.TrimSpace(r.URL.Query().Get("cursor"))

	// Construct HuggingFace API search URL
	hfURL, err := url.Parse("https://huggingface.co/api/models")
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}

	q := hfURL.Query()
	if query != "" {
		q.Set("search", query)
	}

	if filterParam == "ollama" {
		q.Set("apps", "ollama")
		q.Set("filter", "gguf")
	} else if filterParam == "vision" {
		q.Set("pipeline_tag", "image-text-to-text")
		q.Set("filter", "gguf")
	} else {
		q.Set("filter", "gguf")
	}

	q.Set("limit", strconv.Itoa(limit))
	q.Set("full", "true")
	q.Set("config", "false")

	// If cursor is provided, add it to load the next page
	if cursor != "" {
		q.Set("cursor", cursor)
	}

	switch sortParam {
	case "downloads":
		q.Set("sort", "downloads")
		q.Set("direction", "-1")
	case "likes":
		q.Set("sort", "likes")
		q.Set("direction", "-1")
	case "lastModified", "modified", "recent":
		q.Set("sort", "lastModified")
		q.Set("direction", "-1")
	default: // "trending"
		q.Set("sort", "trendingScore")
		q.Set("direction", "-1")
	}

	hfURL.RawQuery = q.Encode()

	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, hfURL.String(), nil)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	req.Header.Set("User-Agent", "Ollama-Manager/0.1.0")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": fmt.Sprintf("failed to reach HuggingFace: %v", err)})
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		writeJSON(w, resp.StatusCode, map[string]string{"error": fmt.Sprintf("HuggingFace API returned %d: %s", resp.StatusCode, string(body))})
		return
	}

	// Extract next-page cursor from Link header
	// Format: <https://huggingface.co/api/models?...&cursor=XXXX>; rel="next"
	nextCursor := ""
	if linkHeader := resp.Header.Get("Link"); linkHeader != "" {
		nextCursor = extractNextCursor(linkHeader)
	}

	var rawModels []struct {
		ID           string    `json:"id"`
		Author       string    `json:"author"`
		Downloads    int       `json:"downloads"`
		Likes        int       `json:"likes"`
		LastModified time.Time `json:"lastModified"`
		Tags         []string  `json:"tags"`
		PipelineTag  string    `json:"pipeline_tag"`
		Private      bool      `json:"private"`
		Siblings     []struct {
			RFilename string `json:"rfilename"`
		} `json:"siblings"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&rawModels); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": fmt.Sprintf("failed to parse HuggingFace response: %v", err)})
		return
	}

	results := make([]HFModelSummary, 0, len(rawModels))
	for _, m := range rawModels {
		if m.Private {
			continue
		}

		ggufCount := 0
		hasVisionFile := false
		for _, s := range m.Siblings {
			baseName := path.Base(s.RFilename)
			fn := strings.ToLower(baseName)
			if strings.HasSuffix(fn, ".gguf") {
				if IsAuxiliaryGGUF(baseName) {
					continue
				}
				if IsVisionProjector(baseName) {
					hasVisionFile = true
				} else {
					ggufCount++
				}
			}
		}

		// If siblings list was provided and has 0 GGUF files, filter out this repo
		// to guarantee only actual GGUF models appear in the explorer.
		if len(m.Siblings) > 0 && ggufCount == 0 && !hasVisionFile {
			continue
		}

		author := m.Author
		name := m.ID
		if parts := strings.SplitN(m.ID, "/", 2); len(parts) == 2 {
			if author == "" {
				author = parts[0]
			}
			name = parts[1]
		}

		hasGGUF := ggufCount > 0
		hasOllama := filterParam == "ollama"
		hasVision := hasVisionFile

		for _, t := range m.Tags {
			tl := strings.ToLower(t)
			if tl == "gguf" {
				hasGGUF = true
			}
			if tl == "ollama" || strings.Contains(tl, "ollama") {
				hasOllama = true
			}
			if tl == "vision" || tl == "multimodal" || tl == "image-to-text" || tl == "image-text-to-text" {
				hasVision = true
			}
		}

		if m.PipelineTag == "image-to-text" || m.PipelineTag == "image-text-to-text" {
			hasVision = true
		}

		results = append(results, HFModelSummary{
			ID:           m.ID,
			Author:       author,
			Name:         name,
			Downloads:    m.Downloads,
			Likes:        m.Likes,
			LastModified: m.LastModified,
			Tags:         m.Tags,
			PipelineTag:  m.PipelineTag,
			IsPrivate:    m.Private,
			HasGGUF:      hasGGUF,
			HasOllama:    hasOllama,
			HasVision:    hasVision,
			GGUFCount:    ggufCount,
		})
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"models":      results,
		"count":       len(results),
		"next_cursor": nextCursor,
	})
}

// extractNextCursor parses the Link header to find the cursor for the next page.
// Format: <https://huggingface.co/api/models?...&cursor=XXXX>; rel="next"
func extractNextCursor(linkHeader string) string {
	// Find rel="next"
	parts := strings.Split(linkHeader, ",")
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if !strings.Contains(part, `rel="next"`) {
			continue
		}
		// Extract URL between < and >
		start := strings.Index(part, "<")
		end := strings.Index(part, ">")
		if start == -1 || end == -1 || end <= start {
			continue
		}
		linkURL := part[start+1 : end]
		// Parse the URL and extract the cursor parameter
		parsed, err := url.Parse(linkURL)
		if err != nil {
			continue
		}
		if c := parsed.Query().Get("cursor"); c != "" {
			return c
		}
	}
	return ""
}

func cleanRepoPath(repoID string) string {
	parts := strings.Split(repoID, "/")
	for i, p := range parts {
		parts[i] = url.PathEscape(strings.TrimSpace(p))
	}
	return strings.Join(parts, "/")
}

// handleHFModelDetails handles GET /api/hf/model?id={repo_id}
func (s *Server) handleHFModelDetails(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	repoID := strings.TrimSpace(r.URL.Query().Get("id"))
	if repoID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "missing model 'id' parameter"})
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()

	cleanedPath := cleanRepoPath(repoID)

	// 1. Fetch metadata from https://huggingface.co/api/models/{id}
	metaURL := fmt.Sprintf("https://huggingface.co/api/models/%s", cleanedPath)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, metaURL, nil)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	req.Header.Set("User-Agent", "Ollama-Manager/0.1.0")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": fmt.Sprintf("failed to fetch model details from HuggingFace: %v", err)})
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		writeJSON(w, resp.StatusCode, map[string]string{"error": fmt.Sprintf("HuggingFace returned %d: %s", resp.StatusCode, string(body))})
		return
	}

	var rawDetail struct {
		ID           string    `json:"id"`
		Author       string    `json:"author"`
		Downloads    int       `json:"downloads"`
		Likes        int       `json:"likes"`
		LastModified time.Time `json:"lastModified"`
		Tags         []string  `json:"tags"`
		PipelineTag  string    `json:"pipeline_tag"`
		Description  string    `json:"description"`
		CardData     struct {
			License string `json:"license"`
		} `json:"cardData"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&rawDetail); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": fmt.Sprintf("failed to parse model details: %v", err)})
		return
	}

	// 2. Fetch repository file tree from https://huggingface.co/api/models/{id}/tree/main?recursive=true
	treeURL := fmt.Sprintf("https://huggingface.co/api/models/%s/tree/main?recursive=true", cleanedPath)
	treeReq, err := http.NewRequestWithContext(ctx, http.MethodGet, treeURL, nil)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	treeReq.Header.Set("User-Agent", "Ollama-Manager/0.1.0")

	treeResp, err := http.DefaultClient.Do(treeReq)
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": fmt.Sprintf("failed to fetch tree from HuggingFace: %v", err)})
		return
	}
	defer treeResp.Body.Close()

	var treeFiles []struct {
		Type string `json:"type"`
		Path string `json:"path"`
		Size int64  `json:"size"`
	}

	if treeResp.StatusCode == http.StatusOK {
		_ = json.NewDecoder(treeResp.Body).Decode(&treeFiles)
	}

	// 3. Process GGUF files and mmproj files
	ggufFiles := make([]HFQuantFile, 0)
	visionFiles := make([]HFQuantFile, 0)
	hasVision := false

	// Check tags for vision
	for _, t := range rawDetail.Tags {
		tl := strings.ToLower(t)
		if tl == "vision" || tl == "multimodal" || tl == "image-to-text" || tl == "image-text-to-text" {
			hasVision = true
		}
	}
	if rawDetail.PipelineTag == "image-to-text" || rawDetail.PipelineTag == "image-text-to-text" {
		hasVision = true
	}

	for _, f := range treeFiles {
		if f.Type != "file" {
			continue
		}
		baseName := path.Base(f.Path)
		if !strings.HasSuffix(strings.ToLower(baseName), ".gguf") {
			continue
		}

		if IsAuxiliaryGGUF(baseName) {
			continue
		}

		if IsVisionProjector(baseName) {
			hasVision = true
			visionFiles = append(visionFiles, HFQuantFile{
				Filename:     f.Path,
				Quant:        "MMPROJ",
				SizeBytes:    f.Size,
				IsVisionProj: true,
				PullName:     fmt.Sprintf("hf.co/%s", rawDetail.ID),
			})
			continue
		}

		quant := ExtractQuantization(baseName)
		pullName := fmt.Sprintf("hf.co/%s:%s", rawDetail.ID, quant)
		if quant == "OTHER" {
			pullName = fmt.Sprintf("hf.co/%s", rawDetail.ID)
		}

		ggufFiles = append(ggufFiles, HFQuantFile{
			Filename:     f.Path,
			Quant:        quant,
			SizeBytes:    f.Size,
			IsVisionProj: false,
			PullName:     pullName,
		})
	}

	author := rawDetail.Author
	name := rawDetail.ID
	if parts := strings.SplitN(rawDetail.ID, "/", 2); len(parts) == 2 {
		if author == "" {
			author = parts[0]
		}
		name = parts[1]
	}

	detail := HFModelDetail{
		ID:             rawDetail.ID,
		Author:         author,
		Name:           name,
		Downloads:      rawDetail.Downloads,
		Likes:          rawDetail.Likes,
		LastModified:   rawDetail.LastModified,
		Tags:           rawDetail.Tags,
		PipelineTag:    rawDetail.PipelineTag,
		License:        rawDetail.CardData.License,
		Description:    rawDetail.Description,
		GGUFFiles:      ggufFiles,
		VisionFiles:    visionFiles,
		HasVision:      hasVision,
		TotalGGUFCount: len(ggufFiles),
	}

	writeJSON(w, http.StatusOK, detail)
}

// handleHFReadme handles GET /api/hf/readme?id={repo_id}
func (s *Server) handleHFReadme(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	repoID := strings.TrimSpace(r.URL.Query().Get("id"))
	if repoID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "missing model 'id' parameter"})
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()

	readmeURL := fmt.Sprintf("https://huggingface.co/%s/raw/main/README.md", cleanRepoPath(repoID))
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, readmeURL, nil)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	req.Header.Set("User-Agent", "Ollama-Manager/0.1.0")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": fmt.Sprintf("failed to fetch README from HuggingFace: %v", err)})
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		writeJSON(w, resp.StatusCode, map[string]string{"error": fmt.Sprintf("README not found (status %d)", resp.StatusCode)})
		return
	}

	// Limit README size to 1MB
	lr := io.LimitReader(resp.Body, 1024*1024)
	content, err := io.ReadAll(lr)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": fmt.Sprintf("failed to read README: %v", err)})
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{
		"id":      repoID,
		"content": string(content),
	})
}
