package rag

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"math"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

const SchemaVersion = 1
const InputFormat = "term-content-v1"
const EmbeddingProvider = "ollama"
const maxDimensions = 65536

type Meta struct {
	ID                string `json:"id"`
	Name              string `json:"name"`
	Description       string `json:"description"`
	CreatedAt         int64  `json:"created_at"`
	EmbeddingProvider string `json:"embedding_provider"`
	EmbeddingModel    string `json:"embedding_model"`
	EmbeddingDigest   string `json:"embedding_digest"`
	Dimensions        int    `json:"dimensions"`
	InputFormat       string `json:"input_format"`
}

type Entry struct {
	Term      string
	Content   string
	MediaType string
	MediaName string
	MediaMIME string
	Media     []byte
	Embedding []float64
}

type Info struct {
	Filename   string `json:"filename"`
	Name       string `json:"name"`
	Model      string `json:"embedding_model"`
	Digest     string `json:"embedding_digest"`
	Dimensions int    `json:"dimensions"`
	Entries    int    `json:"entries"`
	CreatedAt  int64  `json:"created_at"`
	SizeBytes  int64  `json:"size_bytes"`
}

type EntryView struct {
	Term      string `json:"term"`
	Content   string `json:"content"`
	MediaType string `json:"media_type"`
	MediaName string `json:"media_name"`
	MediaMIME string `json:"media_mime"`
	MediaSize int64  `json:"media_size"`
}

type Detail struct {
	Filename string      `json:"filename"`
	Meta     Meta        `json:"meta"`
	Entries  []EntryView `json:"entries"`
}

const schemaSQL = `
CREATE TABLE metadata (
	key INTEGER PRIMARY KEY CHECK (key = 1),
	schema_version INTEGER NOT NULL,
	id TEXT NOT NULL,
	name TEXT NOT NULL,
	description TEXT NOT NULL,
	created_at INTEGER NOT NULL,
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
	media_type TEXT NOT NULL,
	media_name TEXT NOT NULL,
	media_mime TEXT NOT NULL,
	media BLOB,
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
	var m Meta
	var version int
	err := db.QueryRow(`SELECT schema_version, id, name, description, created_at,
		embedding_provider, embedding_model, embedding_digest, dimensions, input_format
		FROM metadata WHERE key = 1`).Scan(
		&version, &m.ID, &m.Name, &m.Description, &m.CreatedAt,
		&m.EmbeddingProvider, &m.EmbeddingModel, &m.EmbeddingDigest, &m.Dimensions, &m.InputFormat)
	if err != nil {
		return Meta{}, 0, err
	}
	return m, version, nil
}

func validate(db *sql.DB, m Meta, version int) error {
	if version != SchemaVersion {
		return fmt.Errorf("unsupported schema version %d", version)
	}
	if m.EmbeddingProvider != EmbeddingProvider {
		return fmt.Errorf("unsupported embedding provider %q", m.EmbeddingProvider)
	}
	if m.InputFormat != InputFormat {
		return fmt.Errorf("unsupported input format %q", m.InputFormat)
	}
	if strings.TrimSpace(m.Name) == "" || strings.TrimSpace(m.ID) == "" ||
		strings.TrimSpace(m.EmbeddingModel) == "" || strings.TrimSpace(m.EmbeddingDigest) == "" {
		return errors.New("metadata missing required fields")
	}
	if m.Dimensions <= 0 || m.Dimensions > maxDimensions {
		return fmt.Errorf("invalid dimensions %d", m.Dimensions)
	}
	rows, err := db.Query(`SELECT embedding FROM entries`)
	if err != nil {
		return fmt.Errorf("entries: %w", err)
	}
	defer rows.Close()
	i := 0
	for rows.Next() {
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
	if i == 0 {
		return errors.New("base has no entries")
	}
	return nil
}

func Create(ctx context.Context, dir string, meta Meta, entries []Entry) (Meta, string, error) {
	if len(entries) == 0 {
		return Meta{}, "", errors.New("a base needs at least one entry")
	}
	if meta.Dimensions <= 0 || meta.Dimensions > maxDimensions {
		return Meta{}, "", fmt.Errorf("invalid dimensions %d", meta.Dimensions)
	}
	for i, e := range entries {
		if !validVector(e.Embedding, meta.Dimensions) {
			return Meta{}, "", fmt.Errorf("entry %d has an invalid embedding", i)
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
		meta.CreatedAt = time.Now().Unix()
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
		(key, schema_version, id, name, description, created_at,
		 embedding_provider, embedding_model, embedding_digest, dimensions, input_format)
		VALUES (1, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		SchemaVersion, meta.ID, meta.Name, meta.Description, meta.CreatedAt,
		meta.EmbeddingProvider, meta.EmbeddingModel, meta.EmbeddingDigest,
		meta.Dimensions, meta.InputFormat); err != nil {
		_ = tx.Rollback()
		return failDB(err)
	}
	for _, e := range entries {
		mediaType := e.MediaType
		if mediaType == "" {
			mediaType = "text"
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO entries
			(term, content, media_type, media_name, media_mime, media, embedding)
			VALUES (?, ?, ?, ?, ?, ?, ?)`,
			e.Term, e.Content, mediaType, e.MediaName, e.MediaMIME, e.Media,
			packVector(e.Embedding)); err != nil {
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
		Name:       m.Name,
		Model:      m.EmbeddingModel,
		Digest:     m.EmbeddingDigest,
		Dimensions: m.Dimensions,
		Entries:    count,
		CreatedAt:  m.CreatedAt,
	}, nil
}

func Get(dir, filename string) (*Detail, error) {
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
	m, version, err := readMeta(db)
	if err != nil {
		return nil, fmt.Errorf("not a RAG base (%v)", err)
	}
	if err := validate(db, m, version); err != nil {
		return nil, err
	}
	rows, err := db.Query(`SELECT term, content, media_type, media_name, media_mime,
		COALESCE(LENGTH(media), 0) FROM entries ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	d := &Detail{Filename: filename, Meta: m, Entries: []EntryView{}}
	for rows.Next() {
		var ev EntryView
		if err := rows.Scan(&ev.Term, &ev.Content, &ev.MediaType, &ev.MediaName,
			&ev.MediaMIME, &ev.MediaSize); err != nil {
			return nil, err
		}
		d.Entries = append(d.Entries, ev)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return d, nil
}
