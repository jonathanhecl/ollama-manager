package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"unicode/utf8"
)

const maxSkillFileReadBytes = 64 * 1024

// DiscoveredSkill represents one validated Agent Skill found in a configured directory.
type DiscoveredSkill struct {
	Name          string `json:"name"`
	Description   string `json:"description"`
	License       string `json:"license,omitempty"`
	Compatibility string `json:"compatibility,omitempty"`
	AllowedTools  string `json:"allowed_tools,omitempty"`
	Dir           string `json:"dir"`
	SourceDir     string `json:"source_dir"`
	SkillFile     string `json:"skill_file"`
}

// SkillDirectoryStatus reports scan status for one configured skills directory.
type SkillDirectoryStatus struct {
	RawPath      string `json:"raw_path"`
	ResolvedPath string `json:"resolved_path"`
	Exists       bool   `json:"exists"`
	SkillCount   int    `json:"skill_count"`
}

// isSkillTool reports whether name is one of the Agent Skills consultation tools.
func isSkillTool(name string) bool {
	switch name {
	case "load_skill", "list_skill_files", "read_skill_file":
		return true
	default:
		return false
	}
}

func skillToolDefinitions() []any {
	return []any{
		map[string]any{
			"type": "function",
			"function": map[string]any{
				"name":        "load_skill",
				"description": "Load the full SKILL.md instructions and directory structure (references/, scripts/, assets/) for an Agent Skill listed in <available_skills>. Call this before following or applying a skill.",
				"parameters": map[string]any{
					"type":     "object",
					"required": []string{"name"},
					"properties": map[string]any{
						"name": map[string]any{
							"type":        "string",
							"description": "Exact skill name from <available_skills>",
						},
					},
				},
			},
		},
		map[string]any{
			"type": "function",
			"function": map[string]any{
				"name":        "list_skill_files",
				"description": "List files and subdirectories inside an Agent Skill's folder (e.g. references, scripts, assets). Read-only.",
				"parameters": map[string]any{
					"type":     "object",
					"required": []string{"skill"},
					"properties": map[string]any{
						"skill": map[string]any{
							"type":        "string",
							"description": "Exact skill name from <available_skills>",
						},
						"path": map[string]any{
							"type":        "string",
							"description": "Optional relative subdirectory path inside the skill folder (defaults to '.')",
						},
					},
				},
			},
		},
		map[string]any{
			"type": "function",
			"function": map[string]any{
				"name":        "read_skill_file",
				"description": "Read a file (such as references/*, scripts/*, or assets/*) inside an Agent Skill's directory. Read-only.",
				"parameters": map[string]any{
					"type":     "object",
					"required": []string{"skill", "path"},
					"properties": map[string]any{
						"skill": map[string]any{
							"type":        "string",
							"description": "Exact skill name from <available_skills>",
						},
						"path": map[string]any{
							"type":        "string",
							"description": "Relative file path inside the skill folder (e.g. references/REFERENCE.md)",
						},
					},
				},
			},
		},
	}
}

func skillToolUsageGuide(name string) string {
	switch name {
	case "load_skill":
		return "\n\nCorrect usage of 'load_skill':\n" +
			"- Description: Load the full SKILL.md instructions and file tree for a skill.\n" +
			"- Required Arguments:\n" +
			"  * 'name': string (exact skill name from <available_skills>)"
	case "list_skill_files":
		return "\n\nCorrect usage of 'list_skill_files':\n" +
			"- Description: List files and subdirectories inside a skill folder.\n" +
			"- Required Arguments:\n" +
			"  * 'skill': string (exact skill name from <available_skills>)\n" +
			"- Optional Arguments:\n" +
			"  * 'path': string (relative subdirectory path, default '.')"
	case "read_skill_file":
		return "\n\nCorrect usage of 'read_skill_file':\n" +
			"- Description: Read a bundled reference, script, or asset text file inside a skill folder.\n" +
			"- Required Arguments:\n" +
			"  * 'skill': string (exact skill name from <available_skills>)\n" +
			"  * 'path': string (relative file path inside the skill folder)"
	default:
		return ""
	}
}

func skillsSystemPromptSection(skills []DiscoveredSkill, artifactsEnabled bool, webEnabled bool) string {
	if len(skills) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("AGENT SKILLS (PROGRESSIVE DISCLOSURE):\n")
	b.WriteString("You have access to the full catalog of Agent Skills below. Each skill is a specialized package containing instructions in SKILL.md and optional bundled references/, scripts/, and assets/.\n")
	b.WriteString("How to use skills:\n")
	b.WriteString("1. Scan <available_skills> to check whether one or more skills match the user's request.\n")
	b.WriteString("2. Whenever a skill applies, call `load_skill` with its exact name BEFORE answering so you read the complete SKILL.md instructions and see its bundled files.\n")
	b.WriteString("3. When SKILL.md points to extra documentation, scripts, or templates (e.g. references/..., scripts/..., assets/...), use `list_skill_files` and `read_skill_file` on demand to inspect them.\n")
	b.WriteString("4. Treat skill instructions and bundled files as specialized guidance; never invent file contents you have not read.\n")
	if artifactsEnabled {
		b.WriteString("5. AGENT EXECUTION MODE (AGENT MODE ENABLED): You may consult skills in read-only mode and apply their instructions, templates, and assets to create/edit files and run commands inside your isolated workspace (`write_file`, `replace_in_file`, `read_file`, `list_dir`, `exec`, and `create_artifact` / `index.html` when building a live web preview). Never modify files inside the skill directories themselves.\n")
	} else {
		b.WriteString("5. READ-ONLY CONSULTATION MODE (AGENT MODE DISABLED): You can ONLY read and consult skills (`load_skill`, `list_skill_files`, `read_skill_file`). You do NOT have filesystem write or command execution tools and CANNOT create, modify, or delete files on disk. Do not claim to have created files on disk.\n")
	}
	if webEnabled {
		b.WriteString("6. INTERNET ACCESS ENABLED: If a consulted skill references external URLs, documentation sites, or APIs that need lookup, you may use `web_search` and `web_fetch` alongside skill consultation (still without writing files to disk unless Agent Mode is enabled).\n")
	}
	b.WriteString("\n<available_skills>\n")
	for _, sk := range skills {
		b.WriteString("  <skill>\n")
		b.WriteString(fmt.Sprintf("    <name>%s</name>\n", escapeXMLText(sk.Name)))
		b.WriteString(fmt.Sprintf("    <description>%s</description>\n", escapeXMLText(sk.Description)))
		if sk.Compatibility != "" {
			b.WriteString(fmt.Sprintf("    <compatibility>%s</compatibility>\n", escapeXMLText(sk.Compatibility)))
		}
		b.WriteString("  </skill>\n")
	}
	b.WriteString("</available_skills>")
	return b.String()
}

func escapeXMLText(s string) string {
	r := strings.NewReplacer(
		"&", "&amp;",
		"<", "&lt;",
		">", "&gt;",
	)
	return r.Replace(strings.TrimSpace(s))
}

func (s *Server) selectedSkillsForChat(body chatRequestBody) []DiscoveredSkill {
	if body.Skills == nil || !*body.Skills {
		return nil
	}
	all := s.listDiscoveredSkills()
	if len(all) == 0 {
		return nil
	}
	if body.SelectedSkills == nil {
		return all
	}
	if len(*body.SelectedSkills) == 0 {
		return nil
	}
	allowed := make(map[string]bool, len(*body.SelectedSkills))
	for _, raw := range *body.SelectedSkills {
		name := strings.ToLower(strings.TrimSpace(raw))
		if name != "" {
			allowed[name] = true
		}
	}
	if len(allowed) == 0 {
		return nil
	}
	out := make([]DiscoveredSkill, 0, len(all))
	for _, sk := range all {
		if allowed[strings.ToLower(strings.TrimSpace(sk.Name))] {
			out = append(out, sk)
		}
	}
	return out
}

func (s *Server) skillsEnabled(body chatRequestBody) bool {
	return len(s.selectedSkillsForChat(body)) > 0
}

func (s *Server) listDiscoveredSkills() []DiscoveredSkill {
	skills, _ := s.discoverSkills()
	return skills
}

// discoverSkills scans configured skill directories (in order) and returns
// deduplicated validated skills along with per-directory scan statuses.
func (s *Server) discoverSkills() ([]DiscoveredSkill, []SkillDirectoryStatus) {
	s.cfgMu.RLock()
	rawDirs := append([]string(nil), s.cfg.Skills.Directories...)
	resolvedDirs := s.cfg.SkillsDirectories()
	s.cfgMu.RUnlock()

	if len(resolvedDirs) == 0 {
		return nil, []SkillDirectoryStatus{}
	}

	statuses := make([]SkillDirectoryStatus, 0, len(resolvedDirs))
	skills := make([]DiscoveredSkill, 0)
	seenNames := make(map[string]bool)
	seenRealDirs := make(map[string]bool)

	for i, resolved := range resolvedDirs {
		raw := resolved
		if i < len(rawDirs) {
			raw = rawDirs[i]
		}
		st := SkillDirectoryStatus{
			RawPath:      raw,
			ResolvedPath: resolved,
		}
		info, err := os.Stat(resolved)
		if err != nil || !info.IsDir() {
			statuses = append(statuses, st)
			continue
		}
		st.Exists = true

		candidates := findSkillFilesInRoot(resolved)
		for _, skillFile := range candidates {
			skillDir := filepath.Dir(skillFile)
			realDir, err := filepath.EvalSymlinks(skillDir)
			if err != nil {
				realDir = filepath.Clean(skillDir)
			}
			if seenRealDirs[realDir] {
				continue
			}
			sk, ok := loadSkillMetadataFromFile(skillFile, skillDir, resolved)
			if !ok {
				continue
			}
			nameKey := strings.ToLower(strings.TrimSpace(sk.Name))
			if nameKey == "" || seenNames[nameKey] {
				continue
			}
			seenRealDirs[realDir] = true
			seenNames[nameKey] = true
			skills = append(skills, sk)
			st.SkillCount++
		}
		statuses = append(statuses, st)
	}

	return skills, statuses
}

// findSkillFilesInRoot scans a configured directory up to depth 2:
// - depth 0: <root>/SKILL.md (single skill pointed directly)
// - depth 1: <root>/<skill>/SKILL.md (standard Agent Skills layout)
// - depth 2: <root>/<group>/<skill>/SKILL.md (grouped skill packs)
// Hidden directories (starting with ".") and node_modules are skipped.
func findSkillFilesInRoot(root string) []string {
	var found []string

	if f, ok := findSkillMarkdownInDir(root); ok {
		found = append(found, f)
		return found
	}

	entries1, err := os.ReadDir(root)
	if err != nil {
		return nil
	}
	sort.Slice(entries1, func(i, j int) bool {
		return strings.ToLower(entries1[i].Name()) < strings.ToLower(entries1[j].Name())
	})
	for _, e1 := range entries1 {
		if !isScannableSkillDirEntry(e1) {
			continue
		}
		d1 := filepath.Join(root, e1.Name())
		if f, ok := findSkillMarkdownInDir(d1); ok {
			found = append(found, f)
			continue
		}
		entries2, err := os.ReadDir(d1)
		if err != nil {
			continue
		}
		sort.Slice(entries2, func(i, j int) bool {
			return strings.ToLower(entries2[i].Name()) < strings.ToLower(entries2[j].Name())
		})
		for _, e2 := range entries2 {
			if !isScannableSkillDirEntry(e2) {
				continue
			}
			d2 := filepath.Join(d1, e2.Name())
			if f, ok := findSkillMarkdownInDir(d2); ok {
				found = append(found, f)
			}
		}
	}
	return found
}

func isScannableSkillDirEntry(e fs.DirEntry) bool {
	name := e.Name()
	if strings.HasPrefix(name, ".") || name == "node_modules" || name == "__pycache__" {
		return false
	}
	if e.IsDir() {
		return true
	}
	if e.Type()&fs.ModeSymlink != 0 {
		return true
	}
	return false
}

func findSkillMarkdownInDir(dir string) (string, bool) {
	info, err := os.Stat(dir)
	if err != nil || !info.IsDir() {
		return "", false
	}
	for _, candidate := range []string{"SKILL.md", "skill.md"} {
		p := filepath.Join(dir, candidate)
		st, err := os.Stat(p)
		if err == nil && !st.IsDir() {
			return p, true
		}
	}
	return "", false
}

func loadSkillMetadataFromFile(skillFile, skillDir, sourceDir string) (DiscoveredSkill, bool) {
	data, err := os.ReadFile(skillFile)
	if err != nil || len(bytes.TrimSpace(data)) == 0 {
		return DiscoveredSkill{}, false
	}
	fallbackName := filepath.Base(skillDir)
	name, desc, license, compat, allowedTools, _ := parseSkillMarkdown(string(data), fallbackName)
	if strings.TrimSpace(name) == "" {
		return DiscoveredSkill{}, false
	}
	return DiscoveredSkill{
		Name:          name,
		Description:   desc,
		License:       license,
		Compatibility: compat,
		AllowedTools:  allowedTools,
		Dir:           filepath.Clean(skillDir),
		SourceDir:     filepath.Clean(sourceDir),
		SkillFile:     filepath.Clean(skillFile),
	}, true
}

// parseSkillMarkdown parses YAML frontmatter (delimited by --- lines) and returns
// metadata fields plus the markdown body. Falls back cleanly when frontmatter fields are absent.
func parseSkillMarkdown(content string, fallbackName string) (name, description, license, compatibility, allowedTools, body string) {
	norm := strings.ReplaceAll(content, "\r\n", "\n")
	norm = strings.TrimPrefix(norm, "\ufeff")
	body = norm

	trimmed := strings.TrimLeft(norm, " \t\n")
	if strings.HasPrefix(trimmed, "---\n") {
		rest := trimmed[len("---\n"):]
		endIdx := -1
		if strings.HasPrefix(rest, "---\n") || rest == "---" {
			endIdx = 0
		} else if idx := strings.Index(rest, "\n---"); idx >= 0 {
			after := rest[idx+len("\n---"):]
			if after == "" || strings.HasPrefix(after, "\n") || strings.HasPrefix(strings.TrimLeft(after, " \t"), "\n") {
				endIdx = idx
			}
		}
		if endIdx >= 0 {
			fmText := rest[:endIdx]
			afterClose := rest[endIdx:]
			if strings.HasPrefix(afterClose, "\n---") {
				afterClose = afterClose[len("\n---"):]
			} else if strings.HasPrefix(afterClose, "---") {
				afterClose = afterClose[len("---"):]
			}
			body = strings.TrimPrefix(afterClose, "\n")
			fm := parseSimpleYAMLFrontmatter(fmText)
			name = fm["name"]
			description = fm["description"]
			license = fm["license"]
			compatibility = fm["compatibility"]
			allowedTools = fm["allowed-tools"]
			if allowedTools == "" {
				allowedTools = fm["allowed_tools"]
			}
		}
	}

	if strings.TrimSpace(name) == "" {
		name = strings.TrimSpace(fallbackName)
	}
	if strings.TrimSpace(description) == "" {
		description = extractFirstMarkdownParagraph(body)
	}
	return strings.TrimSpace(name), strings.TrimSpace(description), strings.TrimSpace(license), strings.TrimSpace(compatibility), strings.TrimSpace(allowedTools), strings.TrimSpace(body)
}

func parseSimpleYAMLFrontmatter(fm string) map[string]string {
	out := make(map[string]string)
	lines := strings.Split(fm, "\n")
	var curKey string
	var blockMode string // "", "|", ">"
	var buf []string

	flush := func() {
		if curKey == "" {
			return
		}
		if len(buf) > 0 {
			var joined string
			if blockMode == "|" {
				joined = strings.Join(buf, "\n")
			} else {
				joined = strings.Join(buf, " ")
			}
			out[curKey] = strings.TrimSpace(joined)
		}
		curKey = ""
		blockMode = ""
		buf = nil
	}

	for _, rawLine := range lines {
		trimLine := strings.TrimSpace(rawLine)
		if curKey != "" && (strings.HasPrefix(rawLine, " ") || strings.HasPrefix(rawLine, "\t")) {
			if trimLine != "" {
				buf = append(buf, trimLine)
			}
			continue
		}
		if curKey != "" && trimLine == "" && blockMode != "" {
			continue
		}
		flush()
		if trimLine == "" || strings.HasPrefix(trimLine, "#") {
			continue
		}
		colon := strings.IndexByte(rawLine, ':')
		if colon <= 0 {
			continue
		}
		key := strings.ToLower(strings.TrimSpace(rawLine[:colon]))
		val := strings.TrimSpace(rawLine[colon+1:])
		switch val {
		case "|", "|-", "|+", ">", ">-", ">+":
			curKey = key
			if strings.HasPrefix(val, "|") {
				blockMode = "|"
			} else {
				blockMode = ">"
			}
			buf = nil
		case "":
			curKey = key
			blockMode = ">"
			buf = nil
		default:
			out[key] = unquoteYAMLValue(val)
		}
	}
	flush()
	return out
}

func unquoteYAMLValue(v string) string {
	v = strings.TrimSpace(v)
	if len(v) >= 2 {
		if (v[0] == '"' && v[len(v)-1] == '"') || (v[0] == '\'' && v[len(v)-1] == '\'') {
			return v[1 : len(v)-1]
		}
	}
	return v
}

func extractFirstMarkdownParagraph(body string) string {
	lines := strings.Split(body, "\n")
	var para []string
	for _, line := range lines {
		t := strings.TrimSpace(line)
		if t == "" {
			if len(para) > 0 {
				break
			}
			continue
		}
		if strings.HasPrefix(t, "#") || strings.HasPrefix(t, "```") {
			if len(para) > 0 {
				break
			}
			continue
		}
		para = append(para, t)
	}
	return truncateRunes(strings.Join(para, " "), 240)
}

func (s *Server) findSkillByNameIn(name string, allowed []DiscoveredSkill) (DiscoveredSkill, bool) {
	target := strings.ToLower(strings.TrimSpace(name))
	if target == "" {
		return DiscoveredSkill{}, false
	}
	skills := allowed
	if skills == nil {
		skills = s.listDiscoveredSkills()
	}
	for _, sk := range skills {
		if strings.ToLower(sk.Name) == target {
			return sk, true
		}
	}
	return DiscoveredSkill{}, false
}

func (s *Server) findSkillByName(name string) (DiscoveredSkill, bool) {
	return s.findSkillByNameIn(name, nil)
}

// resolveSkillSafePath ensures relPath resolves strictly inside skillDir without
// directory traversal or escaping via symlinks.
func resolveSkillSafePath(skillDir, relPath string, allowRoot bool) (string, error) {
	realRoot, err := filepath.EvalSymlinks(skillDir)
	if err != nil {
		realRoot = filepath.Clean(skillDir)
	}
	cleanRel := strings.TrimSpace(relPath)
	if cleanRel == "" || cleanRel == "." || cleanRel == "./" {
		if !allowRoot {
			return "", errors.New("missing file path inside skill directory")
		}
		return realRoot, nil
	}
	if filepath.IsAbs(cleanRel) {
		return "", errors.New("path must be relative to the skill directory")
	}
	cleaned := filepath.Clean(filepath.FromSlash(cleanRel))
	if cleaned == ".." || strings.HasPrefix(cleaned, ".."+string(os.PathSeparator)) {
		return "", errors.New("path escapes skill directory")
	}
	candidate := filepath.Join(realRoot, cleaned)
	if !isPathSafe(realRoot, candidate) {
		return "", errors.New("path escapes skill directory")
	}
	realTarget, err := filepath.EvalSymlinks(candidate)
	if err != nil {
		return "", err
	}
	if !isPathSafe(realRoot, realTarget) {
		return "", errors.New("symlink target escapes skill directory")
	}
	return realTarget, nil
}

func listSkillRelativeTree(skillDir, subDir string, maxEntries int) ([]string, error) {
	targetDir, err := resolveSkillSafePath(skillDir, subDir, true)
	if err != nil {
		return nil, err
	}
	info, err := os.Stat(targetDir)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("%q is a file, not a directory", subDir)
	}
	realRoot, err := filepath.EvalSymlinks(skillDir)
	if err != nil {
		realRoot = filepath.Clean(skillDir)
	}

	var entries []string
	err = filepath.WalkDir(targetDir, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return nil
		}
		name := d.Name()
		if path != targetDir && (strings.HasPrefix(name, ".") || name == "node_modules" || name == "__pycache__") {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if path == targetDir {
			return nil
		}
		realPath, err := filepath.EvalSymlinks(path)
		if err != nil || !isPathSafe(realRoot, realPath) {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		rel, err := filepath.Rel(targetDir, path)
		if err != nil {
			return nil
		}
		relSlash := filepath.ToSlash(rel)
		if d.IsDir() {
			entries = append(entries, relSlash+"/")
		} else {
			entries = append(entries, relSlash)
		}
		if maxEntries > 0 && len(entries) >= maxEntries {
			return fs.SkipAll
		}
		return nil
	})
	if err != nil && !errors.Is(err, fs.SkipAll) {
		return nil, err
	}
	sort.Strings(entries)
	return entries, nil
}

func skillToolStartPayload(base map[string]any, name string, args json.RawMessage) map[string]any {
	if base == nil {
		base = map[string]any{"phase": "start", "name": name}
	}
	m := parseToolArgs(args)
	switch name {
	case "load_skill":
		if n, _ := m["name"].(string); strings.TrimSpace(n) != "" {
			base["skill"] = strings.TrimSpace(n)
		}
	case "list_skill_files":
		if s, _ := m["skill"].(string); strings.TrimSpace(s) != "" {
			base["skill"] = strings.TrimSpace(s)
		}
		if p, _ := m["path"].(string); strings.TrimSpace(p) != "" {
			base["path"] = strings.TrimSpace(p)
		} else {
			base["path"] = "."
		}
	case "read_skill_file":
		if s, _ := m["skill"].(string); strings.TrimSpace(s) != "" {
			base["skill"] = strings.TrimSpace(s)
		}
		if p, _ := m["path"].(string); strings.TrimSpace(p) != "" {
			base["path"] = strings.TrimSpace(p)
		}
	}
	return base
}

func (s *Server) runSkillTool(ctx context.Context, name string, rawArgs json.RawMessage) (string, error) {
	return s.runSkillToolWithAllowed(ctx, name, rawArgs, nil)
}

func (s *Server) runSkillToolWithAllowed(_ context.Context, name string, rawArgs json.RawMessage, allowed []DiscoveredSkill) (string, error) {
	m := parseToolArgs(rawArgs)
	switch name {
	case "load_skill":
		skillName, _ := m["name"].(string)
		if strings.TrimSpace(skillName) == "" {
			return "Error: missing 'name' argument for load_skill", nil
		}
		sk, ok := s.findSkillByNameIn(skillName, allowed)
		if !ok {
			return fmt.Sprintf("Error: skill %q not found in <available_skills>", skillName), nil
		}
		data, err := os.ReadFile(sk.SkillFile)
		if err != nil {
			return "", fmt.Errorf("read %s: %w", sk.Name, err)
		}
		files, _ := listSkillRelativeTree(sk.Dir, ".", 200)
		var out strings.Builder
		out.WriteString(fmt.Sprintf("# Skill: %s\n", sk.Name))
		if sk.Compatibility != "" {
			out.WriteString(fmt.Sprintf("Compatibility: %s\n", sk.Compatibility))
		}
		if sk.License != "" {
			out.WriteString(fmt.Sprintf("License: %s\n", sk.License))
		}
		out.WriteString("\n## Files in Skill Directory\n")
		if len(files) == 0 {
			out.WriteString("(only SKILL.md)\n")
		} else {
			for _, f := range files {
				out.WriteString("- " + f + "\n")
			}
		}
		out.WriteString("\n## SKILL.md Content\n\n")
		out.WriteString(string(data))
		return out.String(), nil

	case "list_skill_files":
		skillName, _ := m["skill"].(string)
		if strings.TrimSpace(skillName) == "" {
			return "Error: missing 'skill' argument for list_skill_files", nil
		}
		sk, ok := s.findSkillByNameIn(skillName, allowed)
		if !ok {
			return fmt.Sprintf("Error: skill %q not found in <available_skills>", skillName), nil
		}
		relPath, _ := m["path"].(string)
		if strings.TrimSpace(relPath) == "" {
			relPath = "."
		}
		files, err := listSkillRelativeTree(sk.Dir, relPath, 400)
		if err != nil {
			return "Error: " + err.Error(), nil
		}
		if len(files) == 0 {
			return "(empty directory)", nil
		}
		return strings.Join(files, "\n"), nil

	case "read_skill_file":
		skillName, _ := m["skill"].(string)
		relPath, _ := m["path"].(string)
		if strings.TrimSpace(skillName) == "" {
			return "Error: missing 'skill' argument for read_skill_file", nil
		}
		if strings.TrimSpace(relPath) == "" {
			return "Error: missing 'path' argument for read_skill_file", nil
		}
		sk, ok := s.findSkillByNameIn(skillName, allowed)
		if !ok {
			return fmt.Sprintf("Error: skill %q not found in <available_skills>", skillName), nil
		}
		targetPath, err := resolveSkillSafePath(sk.Dir, relPath, false)
		if err != nil {
			return "Error: " + err.Error(), nil
		}
		info, err := os.Stat(targetPath)
		if err != nil {
			return "Error: " + err.Error(), nil
		}
		if info.IsDir() {
			return fmt.Sprintf("Error: %q is a directory; use list_skill_files instead", relPath), nil
		}
		data, err := os.ReadFile(targetPath)
		if err != nil {
			return "Error: " + err.Error(), nil
		}
		truncated := false
		if len(data) > maxSkillFileReadBytes {
			data = data[:maxSkillFileReadBytes]
			truncated = true
		}
		if bytes.IndexByte(data, 0) >= 0 || !utf8.Valid(data) {
			return fmt.Sprintf("Error: %q appears to be a binary file (%d bytes) and cannot be read as text", relPath, info.Size()), nil
		}
		res := string(data)
		if truncated {
			res += fmt.Sprintf("\n…(truncated at %d bytes; full file is %d bytes)", maxSkillFileReadBytes, info.Size())
		}
		return res, nil

	default:
		return fmt.Sprintf("Error: tool %q is not a skill tool", name), nil
	}
}

// handleListSkills returns the configured Agent Skills directory statuses and
// the full deduplicated catalog of discovered skills.
func (s *Server) handleListSkills(w http.ResponseWriter, r *http.Request) {
	skills, dirs := s.discoverSkills()
	if skills == nil {
		skills = []DiscoveredSkill{}
	}
	if dirs == nil {
		dirs = []SkillDirectoryStatus{}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"directories": dirs,
		"skills":      skills,
		"count":       len(skills),
	})
}
