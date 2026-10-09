package rag

import (
	"context"
	"database/sql"
	"errors"
	"math"
	"os"
	"path/filepath"
	"testing"

	_ "modernc.org/sqlite"
)

func searchMeta(dims int) Meta {
	m := testMeta()
	m.Dimensions = dims
	return m
}

func createSearchBase(t *testing.T, dir string, m Meta, entries []Entry) (Meta, string) {
	t.Helper()
	meta, filename, err := Create(context.Background(), dir, m, entries)
	if err != nil {
		t.Fatal(err)
	}
	return meta, filename
}

func TestSearchCosineScoresAndThreshold(t *testing.T) {
	dir := t.TempDir()
	meta, filename := createSearchBase(t, dir, searchMeta(2), []Entry{
		{Term: "apple", Content: "a fruit", Embedding: []float64{1, 0}},
		{Term: "pear", Content: "orthogonal", Embedding: []float64{0, 1}},
		{Term: "neg", Content: "opposite", Embedding: []float64{-1, 0}},
	})
	got, err := Search(context.Background(), dir, filename, meta, []float64{2, 0}, 10, 0.35)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("matches = %+v, want only the aligned entry", got)
	}
	if got[0].Term != "apple" || math.Abs(got[0].Score-1) > 1e-9 {
		t.Fatalf("match = %+v, want apple with score ~1", got[0])
	}
	got, err = Search(context.Background(), dir, filename, meta, []float64{1, 0}, 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[1].Term != "pear" || got[1].Score != 0 {
		t.Fatalf("matches at threshold 0 = %+v", got)
	}
}

func TestSearchNormalizesNonUnitVectors(t *testing.T) {
	dir := t.TempDir()
	meta, filename := createSearchBase(t, dir, searchMeta(2), []Entry{
		{Term: "big", Content: "c", Embedding: []float64{10, 0}},
	})
	got, err := Search(context.Background(), dir, filename, meta, []float64{3, 0}, 10, 0.35)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || math.Abs(got[0].Score-1) > 1e-9 {
		t.Fatalf("match = %+v, want score ~1 regardless of magnitudes", got)
	}
}

func TestSearchSkipsEmptyAndZeroNormEntries(t *testing.T) {
	dir := t.TempDir()
	meta, filename := createSearchBase(t, dir, searchMeta(2), []Entry{
		{Term: "zero", Content: "c", Embedding: []float64{0, 0}},
		{Term: " ", Content: "", Embedding: []float64{1, 0}},
		{Term: "real", Content: "c", Embedding: []float64{1, 0}},
	})
	got, err := Search(context.Background(), dir, filename, meta, []float64{1, 0}, 10, -1)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Term != "real" {
		t.Fatalf("matches = %+v, want only the real entry", got)
	}
}

func TestSearchRejectsBadQueryAndParams(t *testing.T) {
	dir := t.TempDir()
	meta, filename := createSearchBase(t, dir, searchMeta(2), []Entry{
		{Term: "t", Content: "c", Embedding: []float64{1, 0}},
	})
	cases := []struct {
		name     string
		query    []float64
		limit    int
		minScore float64
	}{
		{"zero query", []float64{0, 0}, 10, 0},
		{"nil query", nil, 10, 0},
		{"dim mismatch", []float64{1, 0, 0}, 10, 0},
		{"nan query", []float64{math.NaN(), 0}, 10, 0},
		{"inf query", []float64{math.Inf(1), 0}, 10, 0},
		{"bad limit", []float64{1, 0}, 0, 0},
		{"nan threshold", []float64{1, 0}, 10, math.NaN()},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := Search(context.Background(), dir, filename, meta, tt.query, tt.limit, tt.minScore); err == nil {
				t.Fatal("expected error")
			}
		})
	}
}

func TestSearchLimitAndTieOrdering(t *testing.T) {
	dir := t.TempDir()
	meta, filename := createSearchBase(t, dir, searchMeta(2), []Entry{
		{Term: "first", Content: "c", Embedding: []float64{1, 0}},
		{Term: "second", Content: "c", Embedding: []float64{1, 0}},
		{Term: "third", Content: "c", Embedding: []float64{1, 0}},
	})
	got, err := Search(context.Background(), dir, filename, meta, []float64{1, 0}, 2, 0.35)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].Term != "first" || got[1].Term != "second" {
		t.Fatalf("ties must keep id order and honour the limit: %+v", got)
	}
}

func TestSearchGuardsFilename(t *testing.T) {
	dir := t.TempDir()
	meta, filename := createSearchBase(t, dir, searchMeta(2), []Entry{
		{Term: "t", Content: "c", Embedding: []float64{1, 0}},
	})
	for _, bad := range []string{"", "..", "../x.db", "a/b.db", "x.txt", ".hidden.db"} {
		if _, err := Search(context.Background(), dir, bad, meta, []float64{1, 0}, 10, 0); err == nil {
			t.Fatalf("Search(%q) should fail", bad)
		}
	}
	link := filepath.Join(dir, "link.db")
	if err := os.Symlink(filepath.Join(dir, filename), link); err == nil {
		if _, err := Search(context.Background(), dir, "link.db", meta, []float64{1, 0}, 10, 0); err == nil {
			t.Fatal("symlinked base should be rejected")
		}
	}
}

func TestSearchReadsV1AndV2Bases(t *testing.T) {
	dir := t.TempDir()
	emb := packVector([]float64{1, 0})
	mkV1 := filepath.Join(dir, "v1.db")
	db, err := sql.Open("sqlite", fileURI(mkV1, "rwc"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`CREATE TABLE metadata (key INTEGER PRIMARY KEY CHECK (key = 1),
		schema_version INTEGER NOT NULL, id TEXT NOT NULL, name TEXT NOT NULL, description TEXT NOT NULL,
		created_at INTEGER NOT NULL, embedding_provider TEXT NOT NULL, embedding_model TEXT NOT NULL,
		embedding_digest TEXT NOT NULL, dimensions INTEGER NOT NULL, input_format TEXT NOT NULL);
		CREATE TABLE entries (id INTEGER PRIMARY KEY, term TEXT NOT NULL, content TEXT NOT NULL,
		media_type TEXT NOT NULL, media_name TEXT NOT NULL, media_mime TEXT NOT NULL, media BLOB,
		embedding BLOB NOT NULL);
		INSERT INTO metadata VALUES (1, 1, 'idv1', 'V1', '', 1, 'ollama', 'm', 'd', 2, 'term-content-v1');
		INSERT INTO entries (term, content, media_type, media_name, media_mime, embedding)
		VALUES ('hit', 'v1 entry', 'text', '', '', ?)`, emb); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	mkV2 := filepath.Join(dir, "v2.db")
	db, err = sql.Open("sqlite", fileURI(mkV2, "rwc"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`CREATE TABLE metadata (key INTEGER PRIMARY KEY CHECK (key = 1),
		schema_version INTEGER NOT NULL, id TEXT NOT NULL, name TEXT NOT NULL, description TEXT NOT NULL,
		created_at INTEGER NOT NULL, embedding_provider TEXT NOT NULL, embedding_model TEXT NOT NULL,
		embedding_digest TEXT NOT NULL, dimensions INTEGER NOT NULL, input_format TEXT NOT NULL);
		CREATE TABLE entries (id INTEGER PRIMARY KEY, term TEXT NOT NULL, content TEXT NOT NULL,
		input_mode TEXT NOT NULL, image_name TEXT NOT NULL, image_mime TEXT NOT NULL, image BLOB,
		audio_name TEXT NOT NULL, audio_mime TEXT NOT NULL, audio BLOB, embedding BLOB NOT NULL);
		INSERT INTO metadata VALUES (1, 2, 'idv2', 'V2', '', 10, 'ollama', 'm', 'd', 2, 'entry-inputs-v2');
		INSERT INTO entries (term, content, input_mode, image_name, image_mime, image,
			audio_name, audio_mime, audio, embedding)
		VALUES ('hit', 'v2 entry', 'combined', '', '', NULL, '', '', NULL, ?)`, emb); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"v1.db", "v2.db"} {
		d, err := Get(dir, name)
		if err != nil {
			t.Fatal(err)
		}
		got, err := Search(context.Background(), dir, name, d.Meta, []float64{1, 0}, 10, 0.35)
		if err != nil {
			t.Fatalf("Search(%s): %v", name, err)
		}
		if len(got) != 1 || got[0].Term != "hit" {
			t.Fatalf("Search(%s) = %+v", name, got)
		}
	}
}

func TestSearchRejectsSwappedBase(t *testing.T) {
	dir := t.TempDir()
	meta, filename := createSearchBase(t, dir, searchMeta(2), []Entry{
		{Term: "t", Content: "c", Embedding: []float64{1, 0}},
	})
	otherDir := t.TempDir()
	otherMeta := searchMeta(2)
	otherMeta.EmbeddingModel = "other-model:latest"
	otherMeta.EmbeddingDigest = "sha256:other"
	_, otherFile, err := Create(context.Background(), otherDir, otherMeta, []Entry{
		{Term: "t", Content: "c", Embedding: []float64{1, 0}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(filepath.Join(otherDir, otherFile), filepath.Join(dir, filename)); err != nil {
		t.Fatal(err)
	}
	if _, err := Search(context.Background(), dir, filename, meta, []float64{1, 0}, 10, 0); err == nil {
		t.Fatal("expected a meta mismatch error after the file was swapped")
	}
}

func TestSearchCancelledContext(t *testing.T) {
	dir := t.TempDir()
	meta, filename := createSearchBase(t, dir, searchMeta(2), []Entry{
		{Term: "t", Content: "c", Embedding: []float64{1, 0}},
	})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := Search(ctx, dir, filename, meta, []float64{1, 0}, 10, 0); !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context.Canceled, got %v", err)
	}
	if _, err := GetContext(ctx, dir, filename); !errors.Is(err, context.Canceled) {
		t.Fatalf("GetContext: expected context.Canceled, got %v", err)
	}
}
