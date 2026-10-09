package rag

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"math"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

const SchemaVersion = 3
const InputFormatV1 = "term-content-v1"
const InputFormatV2 = "entry-inputs-v2"
const InputFormat = "entry-inputs-v3"
const EmbeddingProvider = "ollama"
const maxDimensions = 65536

type Meta struct {
	ID                string `json:"id"`
	Name              string `json:"name"`
	Description       string `json:"description"`
	CreatedAt         int64  `json:"created_at"`
	UpdatedAt         int64  `json:"updated_at"`
	EmbeddingProvider string `json:"embedding_provider"`
	EmbeddingModel    string `json:"embedding_model"`
	EmbeddingDigest   string `json:"embedding_digest"`
	Dimensions        int    `json:"dimensions"`
	InputFormat       string `json:"input_format"`
}

type Media struct {
	Type string
	Name string
	MIME string
	Data []byte
}

type Entry struct {
	Term      string
	Content   string
	InputMode string
	CreatedAt int64
	UpdatedAt int64
	Media     []Media
	Embedding []float64
}

type Info struct {
	Filename    string `json:"filename"`
	Name        string `json:"name"`
	Description string `json:"description"`
	Model       string `json:"embedding_model"`
	Digest      string `json:"embedding_digest"`
	Dimensions  int    `json:"dimensions"`
	Entries     int    `json:"entries"`
	CreatedAt   int64  `json:"created_at"`
	UpdatedAt   int64  `json:"updated_at"`
	SizeBytes   int64  `json:"size_bytes"`
}

type MediaView struct {
	Type string `json:"type"`
	Name string `json:"name"`
	MIME string `json:"mime"`
	Size int64  `json:"size"`
}

type EntryView struct {
	ID        int64       `json:"id"`
	Term      string      `json:"term"`
	Content   string      `json:"content"`
	InputMode string      `json:"input_mode"`
	CreatedAt int64       `json:"created_at"`
	UpdatedAt int64       `json:"updated_at"`
	Media     []MediaView `json:"media"`
}

type Detail struct {
	Filename  string      `json:"filename"`
	Meta      Meta        `json:"meta"`
	Entries   []EntryView `json:"entries"`
	SizeBytes int64       `json:"size_bytes"`
}

const schemaSQL = `
CREATE TABLE metadata (
	key INTEGER PRIMARY KEY CHECK (key = 1),
	schema_version INTEGER NOT NULL,
	id TEXT NOT NULL,
	name TEXT NOT NULL,
	description TEXT NOT NULL,
	created_at INTEGER NOT NULL,
	updated_at INTEGER NOT NULL,
	embedding_provider TEXT NOT NULL,
	embedding_model TEXT NOT NULL,
	embedding_digest TEXT NOT NULL,
	dimensions INTEGER NOT NULL CHECK (dimensions > 0),
	input_format TEXT NOT NULL
);
CREATE TABLE entries (
	id INTEGER PRIMARY KEY,
	term TEXT NOT NULL,
	content TEXT NOT NULL,
	input_mode TEXT NOT NULL,
	created_at INTEGER NOT NULL,
	updated_at INTEGER NOT NULL,
	image_name TEXT NOT NULL,
	image_mime TEXT NOT NULL,
	image BLOB,
	audio_name TEXT NOT NULL,
	audio_mime TEXT NOT NULL,
	audio BLOB,
	embedding BLOB NOT NULL
);`

func packVector(v []float64) []byte {
	buf := make([]byte, len(v)*8)
	for i, f := range v {
		binary.LittleEndian.PutUint64(buf[i*8:], math.Float64bits(f))
	}
	return buf
}

func validVector(v []float64, dims int) bool {
	if dims <= 0 || len(v) != dims {
		return false
	}
	for _, f := range v {
		if math.IsNaN(f) || math.IsInf(f, 0) {
			return false
		}
	}
	return true
}

func checkVectorBlob(b []byte, dims int) error {
	if dims <= 0 || dims > maxDimensions {
		return fmt.Errorf("invalid dimensions %d", dims)
	}
	if len(b) != dims*8 {
		return fmt.Errorf("embedding is %d bytes, want %d", len(b), dims*8)
	}
	for i := 0; i+8 <= len(b); i += 8 {
		f := math.Float64frombits(binary.LittleEndian.Uint64(b[i:]))
		if math.IsNaN(f) || math.IsInf(f, 0) {
			return errors.New("non-finite value in embedding")
		}
	}
	return nil
}

func randHex(n int) (string, error) {
	buf := make([]byte, n)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf), nil
}

func sanitizeFileName(name string) string {
	name = strings.TrimSpace(name)
	var b strings.Builder
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '-', r == '_':
			b.WriteRune(r)
		case r >= 'A' && r <= 'Z':
			b.WriteRune(r + ('a' - 'A'))
		case r == ' ', r == '.', r == '/', r == '\\':
			b.WriteRune('-')
		}
	}
	out := strings.Trim(b.String(), "-.")
	if out == "" {
		out = "rag"
	}
	if len(out) > 60 {
		out = strings.Trim(out[:60], "-.")
		if out == "" {
			out = "rag"
		}
	}
	return out
}

func fileURI(path, mode string) string {
	u := &url.URL{Path: path, RawQuery: "mode=" + mode}
	return "file:" + u.String()
}

func openRO(path string) (*sql.DB, error) {
	return sql.Open("sqlite", fileURI(path, "ro"))
}

func readMeta(db *sql.DB) (Meta, int, error) {
	return readMetaContext(context.Background(), db)
}

func readMetaContext(ctx context.Context, db queryer) (Meta, int, error) {
	var m Meta
	var version int
	err := db.QueryRowContext(ctx, `SELECT schema_version, id, name, description, created_at,
		embedding_provider, embedding_model, embedding_digest, dimensions, input_format
		FROM metadata WHERE key = 1`).Scan(
		&version, &m.ID, &m.Name, &m.Description, &m.CreatedAt,
		&m.EmbeddingProvider, &m.EmbeddingModel, &m.EmbeddingDigest, &m.Dimensions, &m.InputFormat)
	if err != nil {
		return Meta{}, 0, err
	}
	if version == SchemaVersion {
		if err := db.QueryRowContext(ctx, `SELECT updated_at FROM metadata WHERE key = 1`).Scan(&m.UpdatedAt); err != nil {
			return Meta{}, 0, err
		}
	}
	if m.UpdatedAt == 0 {
		m.UpdatedAt = m.CreatedAt
	}
	return m, version, nil
}

func validate(db *sql.DB, m Meta, version int) error {
	return validateContext(context.Background(), db, m, version)
}

func validateContext(ctx context.Context, db queryer, m Meta, version int) error {
	switch version {
	case 1:
		if m.InputFormat != InputFormatV1 {
			return fmt.Errorf("unsupported input format %q", m.InputFormat)
		}
	case 2:
		if m.InputFormat != InputFormatV2 {
			return fmt.Errorf("unsupported input format %q", m.InputFormat)
		}
	case SchemaVersion:
		if m.InputFormat != InputFormat {
			return fmt.Errorf("unsupported input format %q", m.InputFormat)
		}
	default:
		return fmt.Errorf("unsupported schema version %d", version)
	}
	if m.EmbeddingProvider != EmbeddingProvider {
		return fmt.Errorf("unsupported embedding provider %q", m.EmbeddingProvider)
	}
	if strings.TrimSpace(m.Name) == "" || strings.TrimSpace(m.ID) == "" ||
		strings.TrimSpace(m.EmbeddingModel) == "" || strings.TrimSpace(m.EmbeddingDigest) == "" {
		return errors.New("metadata missing required fields")
	}
	if m.Dimensions <= 0 || m.Dimensions > maxDimensions {
		return fmt.Errorf("invalid dimensions %d", m.Dimensions)
	}
	rows, err := db.QueryContext(ctx, `SELECT embedding FROM entries`)
	if err != nil {
		return fmt.Errorf("entries: %w", err)
	}
	defer rows.Close()
	i := 0
	for rows.Next() {
		if err := ctx.Err(); err != nil {
			return err
		}
		var blob []byte
		if err := rows.Scan(&blob); err != nil {
			return err
		}
		if err := checkVectorBlob(blob, m.Dimensions); err != nil {
			return fmt.Errorf("entry %d: %w", i, err)
		}
		i++
	}
	if err := rows.Err(); err != nil {
		return err
	}
	return nil
}

func createTemp(ctx context.Context, dir string, meta Meta, entries []Entry) (Meta, string, error) {
	if len(entries) == 0 {
		return Meta{}, "", errors.New("a base needs at least one entry")
	}
	if meta.Dimensions <= 0 || meta.Dimensions > maxDimensions {
		return Meta{}, "", fmt.Errorf("invalid dimensions %d", meta.Dimensions)
	}
	now := time.Now().Unix()
	for i, e := range entries {
		if e.CreatedAt == 0 {
			e.CreatedAt = now
		}
		if e.UpdatedAt == 0 {
			e.UpdatedAt = e.CreatedAt
		}
		entries[i] = e
		if !validVector(e.Embedding, meta.Dimensions) {
			return Meta{}, "", fmt.Errorf("entry %d has an invalid embedding", i)
		}
		mode := e.InputMode
		if mode == "" {
			mode = "combined"
		}
		if mode != "combined" && mode != "media" {
			return Meta{}, "", fmt.Errorf("entry %d has unsupported input mode %q", i, mode)
		}
		seen := map[string]bool{}
		for _, m := range e.Media {
			if m.Type != "image" && m.Type != "audio" {
				return Meta{}, "", fmt.Errorf("entry %d has unsupported media type %q", i, m.Type)
			}
			if seen[m.Type] {
				return Meta{}, "", fmt.Errorf("entry %d has duplicate %s media", i, m.Type)
			}
			seen[m.Type] = true
			if len(m.Data) == 0 {
				return Meta{}, "", fmt.Errorf("entry %d has empty %s media", i, m.Type)
			}
		}
		if mode == "media" && len(e.Media) == 0 {
			return Meta{}, "", fmt.Errorf("entry %d uses media input without media", i)
		}
	}
	if err := ctx.Err(); err != nil {
		return Meta{}, "", err
	}
	if meta.ID == "" {
		id, err := randHex(8)
		if err != nil {
			return Meta{}, "", err
		}
		meta.ID = id
	}
	if meta.CreatedAt == 0 {
		meta.CreatedAt = now
	}
	if meta.UpdatedAt == 0 {
		meta.UpdatedAt = meta.CreatedAt
	}
	if meta.EmbeddingProvider == "" {
		meta.EmbeddingProvider = EmbeddingProvider
	}
	if meta.InputFormat == "" {
		meta.InputFormat = InputFormat
	}
	if meta.Name == "" {
		return Meta{}, "", errors.New("name is required")
	}
	if meta.EmbeddingModel == "" || meta.EmbeddingDigest == "" {
		return Meta{}, "", errors.New("embedding model and digest are required")
	}

	if err := os.MkdirAll(dir, 0o755); err != nil {
		return Meta{}, "", err
	}
	tmpFile, err := os.CreateTemp(dir, ".tmp-*.db")
	if err != nil {
		return Meta{}, "", err
	}
	tmp := tmpFile.Name()
	if err := tmpFile.Close(); err != nil {
		_ = os.Remove(tmp)
		return Meta{}, "", err
	}
	fail := func(err error) (Meta, string, error) {
		_ = os.Remove(tmp)
		return Meta{}, "", err
	}
	if err := ctx.Err(); err != nil {
		return fail(err)
	}

	db, err := sql.Open("sqlite", fileURI(tmp, "rw"))
	if err != nil {
		return fail(err)
	}
	db.SetMaxOpenConns(1)
	failDB := func(err error) (Meta, string, error) {
		_ = db.Close()
		_ = os.Remove(tmp)
		return Meta{}, "", err
	}
	if _, err := db.ExecContext(ctx, `PRAGMA journal_mode=DELETE`); err != nil {
		return failDB(err)
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return failDB(err)
	}
	if _, err := tx.ExecContext(ctx, schemaSQL); err != nil {
		_ = tx.Rollback()
		return failDB(err)
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO metadata
		(key, schema_version, id, name, description, created_at, updated_at,
		 embedding_provider, embedding_model, embedding_digest, dimensions, input_format)
		VALUES (1, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		SchemaVersion, meta.ID, meta.Name, meta.Description, meta.CreatedAt, meta.UpdatedAt,
		meta.EmbeddingProvider, meta.EmbeddingModel, meta.EmbeddingDigest,
		meta.Dimensions, meta.InputFormat); err != nil {
		_ = tx.Rollback()
		return failDB(err)
	}
	for _, e := range entries {
		mode := e.InputMode
		if mode == "" {
			mode = "combined"
		}
		var image, audio Media
		for _, m := range e.Media {
			if m.Type == "image" {
				image = m
			} else if m.Type == "audio" {
				audio = m
			}
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO entries
			(term, content, input_mode, created_at, updated_at,
			 image_name, image_mime, image, audio_name, audio_mime, audio, embedding)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			e.Term, e.Content, mode, e.CreatedAt, e.UpdatedAt,
			image.Name, image.MIME, image.Data,
			audio.Name, audio.MIME, audio.Data, packVector(e.Embedding)); err != nil {
			_ = tx.Rollback()
			return failDB(err)
		}
	}
	if err := tx.Commit(); err != nil {
		return failDB(err)
	}
	if err := db.Close(); err != nil {
		_ = os.Remove(tmp)
		return Meta{}, "", err
	}
	if err := ctx.Err(); err != nil {
		return fail(err)
	}
	return meta, tmp, nil
}

func Create(ctx context.Context, dir string, meta Meta, entries []Entry) (Meta, string, error) {
	meta, tmp, err := createTemp(ctx, dir, meta, entries)
	if err != nil {
		return Meta{}, "", err
	}
	fail := func(err error) (Meta, string, error) {
		_ = os.Remove(tmp)
		return Meta{}, "", err
	}
	base := sanitizeFileName(meta.Name)
	for attempt := 0; attempt < 8; attempt++ {
		suffix, err := randHex(6)
		if err != nil {
			return fail(err)
		}
		final := filepath.Join(dir, base+"-"+suffix+".db")
		err = os.Link(tmp, final)
		if err == nil {
			if rmErr := os.Remove(tmp); rmErr != nil {
				_ = os.Remove(final)
				return fail(rmErr)
			}
			return meta, filepath.Base(final), nil
		}
		if !errors.Is(err, os.ErrExist) {
			return fail(err)
		}
	}
	return fail(errors.New("could not allocate a unique base file name"))
}

func Replace(ctx context.Context, dir, filename string, meta Meta, entries []Entry) (Meta, error) {
	if !ValidFilename(filename) {
		return Meta{}, errors.New("invalid base name")
	}
	target := filepath.Join(dir, filename)
	st, err := os.Lstat(target)
	if err != nil || !st.Mode().IsRegular() {
		return Meta{}, os.ErrNotExist
	}
	meta.UpdatedAt = time.Now().Unix()
	meta, tmp, err := createTemp(ctx, dir, meta, entries)
	if err != nil {
		return Meta{}, err
	}
	cleanup := func(err error) (Meta, error) {
		_ = os.Remove(tmp)
		return Meta{}, err
	}
	suffix, err := randHex(6)
	if err != nil {
		return cleanup(err)
	}
	backup := target + ".bak-" + suffix
	if err := os.Rename(target, backup); err != nil {
		return cleanup(err)
	}
	if err := os.Rename(tmp, target); err != nil {
		_ = os.Rename(backup, target)
		return cleanup(err)
	}
	_ = os.Remove(backup)
	return meta, nil
}

func Path(dir, filename string) (string, error) {
	if !ValidFilename(filename) {
		return "", errors.New("invalid base name")
	}
	path := filepath.Join(dir, filename)
	st, err := os.Lstat(path)
	if err != nil || !st.Mode().IsRegular() {
		return "", os.ErrNotExist
	}
	return path, nil
}

// Import copies an uploaded .db into the managed directory only after the
// temporary copy proves it is a readable, compatible RAG database. The original
// filename is never trusted as a path; the final name is generated in dir.
func Import(dir, sourceName string, r io.Reader) (Info, error) {
	if r == nil {
		return Info{}, errors.New("empty upload")
	}
	sourceName = strings.TrimSpace(sourceName)
	if sourceName != "" && !ValidFilename(sourceName) {
		return Info{}, errors.New("invalid upload filename")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return Info{}, err
	}
	tmpFile, err := os.CreateTemp(dir, ".import-*.db")
	if err != nil {
		return Info{}, err
	}
	tmp := tmpFile.Name()
	cleanup := func(err error) (Info, error) {
		_ = tmpFile.Close()
		_ = os.Remove(tmp)
		return Info{}, err
	}
	written, err := io.Copy(tmpFile, r)
	if err != nil {
		return cleanup(err)
	}
	if err := tmpFile.Sync(); err != nil {
		return cleanup(err)
	}
	if err := tmpFile.Close(); err != nil {
		return cleanup(err)
	}
	if written == 0 {
		return cleanup(errors.New("empty upload"))
	}
	info, err := readInfo(tmp)
	if err != nil {
		return cleanup(err)
	}

	baseName := strings.TrimSuffix(sourceName, filepath.Ext(sourceName))
	if strings.TrimSpace(info.Name) != "" {
		baseName = info.Name
	}
	base := sanitizeFileName(baseName)
	for attempt := 0; attempt < 8; attempt++ {
		suffix, err := randHex(6)
		if err != nil {
			return cleanup(err)
		}
		final := filepath.Join(dir, base+"-"+suffix+".db")
		if err := os.Link(tmp, final); err == nil {
			_ = os.Remove(tmp)
			info.Filename = filepath.Base(final)
			if st, statErr := os.Stat(final); statErr == nil {
				info.SizeBytes = st.Size()
			}
			return info, nil
		} else if !errors.Is(err, os.ErrExist) {
			return cleanup(err)
		}
	}
	return cleanup(errors.New("could not allocate a unique base file name"))
}

func Delete(dir, filename string) error {
	path, err := Path(dir, filename)
	if err != nil {
		return err
	}
	return os.Remove(path)
}

func ValidFilename(name string) bool {
	if name == "" || name == "." || name == ".." {
		return false
	}
	if strings.ContainsAny(name, "/\\") || strings.ContainsRune(name, 0) {
		return false
	}
	if filepath.Base(name) != name {
		return false
	}
	if strings.HasPrefix(name, ".") || strings.HasPrefix(name, "~") {
		return false
	}
	return strings.EqualFold(filepath.Ext(name), ".db")
}

func isSidecar(name string) bool {
	l := strings.ToLower(name)
	return strings.HasSuffix(l, "-wal") || strings.HasSuffix(l, "-shm") ||
		strings.HasSuffix(l, "-journal") || strings.HasSuffix(l, ".tmp")
}

func List(dir string) ([]Info, []string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return []Info{}, nil, nil
		}
		return nil, nil, err
	}
	out := make([]Info, 0, len(entries))
	var warnings []string
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !e.Type().IsRegular() || isSidecar(name) {
			continue
		}
		if !ValidFilename(name) {
			continue
		}
		path := filepath.Join(dir, name)
		info, werr := readInfo(path)
		if werr != nil {
			warnings = append(warnings, fmt.Sprintf("%s: %s", name, werr))
			continue
		}
		if st, err := e.Info(); err == nil {
			info.SizeBytes = st.Size()
		}
		info.Filename = name
		out = append(out, info)
	}
	return out, warnings, nil
}

func readInfo(path string) (Info, error) {
	db, err := openRO(path)
	if err != nil {
		return Info{}, err
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	m, version, err := readMeta(db)
	if err != nil {
		return Info{}, fmt.Errorf("not a RAG base (%v)", err)
	}
	if err := validate(db, m, version); err != nil {
		return Info{}, err
	}
	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM entries`).Scan(&count); err != nil {
		return Info{}, err
	}
	return Info{
		Name:        m.Name,
		Description: m.Description,
		Model:       m.EmbeddingModel,
		Digest:      m.EmbeddingDigest,
		Dimensions:  m.Dimensions,
		Entries:     count,
		CreatedAt:   m.CreatedAt,
		UpdatedAt:   m.UpdatedAt,
	}, nil
}

func Get(dir, filename string) (*Detail, error) {
	return GetContext(context.Background(), dir, filename)
}

func GetContext(ctx context.Context, dir, filename string) (*Detail, error) {
	if !ValidFilename(filename) {
		return nil, errors.New("invalid base name")
	}
	path := filepath.Join(dir, filename)
	st, err := os.Lstat(path)
	if err != nil || !st.Mode().IsRegular() {
		return nil, os.ErrNotExist
	}
	db, err := openRO(path)
	if err != nil {
		return nil, err
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	m, version, err := readMetaContext(ctx, db)
	if err != nil {
		return nil, fmt.Errorf("not a RAG base (%w)", err)
	}
	if err := validateContext(ctx, db, m, version); err != nil {
		return nil, err
	}
	var query string
	switch version {
	case 1:
		query = `SELECT id, term, content, 'combined' AS input_mode, media_type, media_name, media_mime,
			COALESCE(LENGTH(media), 0), '', '', '', 0, 0, 0 FROM entries ORDER BY id`
	case 2:
		query = `SELECT id, term, content, input_mode,
			'image', image_name, image_mime, COALESCE(LENGTH(image), 0),
			'audio', audio_name, audio_mime, COALESCE(LENGTH(audio), 0), 0, 0
			FROM entries ORDER BY id`
	case SchemaVersion:
		query = `SELECT id, term, content, input_mode,
			'image', image_name, image_mime, COALESCE(LENGTH(image), 0),
			'audio', audio_name, audio_mime, COALESCE(LENGTH(audio), 0), created_at, updated_at
			FROM entries ORDER BY id`
	}
	rows, err := db.QueryContext(ctx, query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	d := &Detail{Filename: filename, Meta: m, Entries: []EntryView{}, SizeBytes: st.Size()}
	for rows.Next() {
		var ev EntryView
		var imageType, imageName, imageMIME string
		var imageSize int64
		var audioType, audioName, audioMIME string
		var audioSize int64
		if err := rows.Scan(&ev.ID, &ev.Term, &ev.Content, &ev.InputMode,
			&imageType, &imageName, &imageMIME, &imageSize,
			&audioType, &audioName, &audioMIME, &audioSize,
			&ev.CreatedAt, &ev.UpdatedAt); err != nil {
			return nil, err
		}
		if ev.CreatedAt == 0 {
			ev.CreatedAt = m.CreatedAt
		}
		if ev.UpdatedAt == 0 {
			ev.UpdatedAt = ev.CreatedAt
		}
		ev.Media = []MediaView{}
		if imageType != "" && imageType != "text" && (imageName != "" || imageMIME != "" || imageSize > 0) {
			ev.Media = append(ev.Media, MediaView{Type: imageType, Name: imageName, MIME: imageMIME, Size: imageSize})
		}
		if audioType == "audio" && (audioName != "" || audioMIME != "" || audioSize > 0) {
			ev.Media = append(ev.Media, MediaView{Type: audioType, Name: audioName, MIME: audioMIME, Size: audioSize})
		}
		d.Entries = append(d.Entries, ev)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return d, nil
}

func MediaAt(dir, filename string, entryID int64, mediaType string) (Media, error) {
	if !ValidFilename(filename) {
		return Media{}, errors.New("invalid base name")
	}
	if mediaType != "image" && mediaType != "audio" {
		return Media{}, errors.New("invalid media type")
	}
	if entryID <= 0 {
		return Media{}, errors.New("invalid entry id")
	}
	path := filepath.Join(dir, filename)
	st, err := os.Lstat(path)
	if err != nil || !st.Mode().IsRegular() {
		return Media{}, os.ErrNotExist
	}
	db, err := openRO(path)
	if err != nil {
		return Media{}, err
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	m, version, err := readMeta(db)
	if err != nil {
		return Media{}, fmt.Errorf("not a RAG base (%v)", err)
	}
	if err := validate(db, m, version); err != nil {
		return Media{}, err
	}
	var out Media
	out.Type = mediaType
	if version == 1 {
		var storedType string
		err = db.QueryRow(`SELECT media_type, media_name, media_mime, media FROM entries WHERE id = ?`, entryID).
			Scan(&storedType, &out.Name, &out.MIME, &out.Data)
		if err != nil {
			return Media{}, err
		}
		if storedType != mediaType {
			return Media{}, os.ErrNotExist
		}
	} else {
		col := mediaType
		err = db.QueryRow(`SELECT `+col+`_name, `+col+`_mime, `+col+` FROM entries WHERE id = ?`, entryID).
			Scan(&out.Name, &out.MIME, &out.Data)
		if err != nil {
			return Media{}, err
		}
	}
	if len(out.Data) == 0 {
		return Media{}, os.ErrNotExist
	}
	return out, nil
}
