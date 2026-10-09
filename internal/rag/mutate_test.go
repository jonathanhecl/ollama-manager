package rag

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

func TestMutateCreateUpdateDeletePreservesUntouched(t *testing.T) {
	dir := t.TempDir()
	meta, filename := createSearchBase(t, dir, searchMeta(2), []Entry{
		{Term: "keep", Content: "untouched", Embedding: []float64{1, 0}},
		{Term: "media", Content: "with image", InputMode: "combined",
			Media:     []Media{{Type: "image", Name: "a.png", MIME: "image/png", Data: []byte{1, 2, 3}}},
			Embedding: []float64{0, 1}},
	})
	detail, err := Get(dir, filename)
	if err != nil {
		t.Fatal(err)
	}
	keep := detail.Entries[0]
	mediaEntry := detail.Entries[1]

	snap, err := CreateEntry(context.Background(), dir, filename, meta, Entry{
		Term: "new", Content: "added", Embedding: []float64{1, 1},
	})
	if err != nil {
		t.Fatal(err)
	}
	if snap.ID <= mediaEntry.ID || snap.Revision == "" {
		t.Fatalf("snapshot = %+v", snap)
	}

	_, rsnap, err := ReadEntry(context.Background(), dir, filename, snap.ID)
	if err != nil {
		t.Fatal(err)
	}
	if rsnap.Revision != snap.Revision {
		t.Fatal("revision must be stable across reads")
	}
	stale := snap.Revision
	up, err := UpdateEntry(context.Background(), dir, filename, meta, snap.ID, stale, Entry{
		Term: "renamed", Content: "edited", InputMode: "combined", Embedding: []float64{0, 1},
	})
	if err != nil {
		t.Fatal(err)
	}
	if up.Revision == stale || up.Entry.Term != "renamed" {
		t.Fatalf("updated snapshot = %+v", up)
	}
	if _, err := UpdateEntry(context.Background(), dir, filename, meta, snap.ID, stale, Entry{
		Term: "x", Content: "y", Embedding: []float64{0, 1},
	}); !errors.Is(err, ErrConflict) {
		t.Fatalf("stale revision: %v", err)
	}

	if err := DeleteEntry(context.Background(), dir, filename, meta, snap.ID, stale); !errors.Is(err, ErrConflict) {
		t.Fatalf("stale delete: %v", err)
	}
	if err := DeleteEntry(context.Background(), dir, filename, meta, snap.ID, up.Revision); err != nil {
		t.Fatal(err)
	}

	after, err := Get(dir, filename)
	if err != nil {
		t.Fatal(err)
	}
	if len(after.Entries) != 2 {
		t.Fatalf("entries = %+v", after.Entries)
	}
	if after.Entries[0].ID != keep.ID || after.Entries[1].ID != mediaEntry.ID {
		t.Fatalf("untouched IDs changed: %+v", after.Entries)
	}
	if after.Entries[1].InputMode != "combined" || len(after.Entries[1].Media) != 1 ||
		after.Entries[1].Media[0].Size != 3 || after.Entries[1].CreatedAt != mediaEntry.CreatedAt {
		t.Fatalf("media entry changed: %+v", after.Entries[1])
	}
	got, err := Search(context.Background(), dir, filename, meta, []float64{1, 0}, 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range got {
		if m.ID == snap.ID {
			t.Fatalf("deleted entry still matches: %+v", got)
		}
	}
}

func TestMutateRejectsBadEmbeddingsAndKeepsDB(t *testing.T) {
	dir := t.TempDir()
	meta, filename := createSearchBase(t, dir, searchMeta(2), []Entry{
		{Term: "a", Content: "c", Embedding: []float64{1, 0}},
	})
	before, err := Get(dir, filename)
	if err != nil {
		t.Fatal(err)
	}
	for _, vec := range [][]float64{{1, 0, 0}, {math.NaN(), 0}, {0, 0}, {math.MaxFloat64, math.MaxFloat64}} {
		if _, err := CreateEntry(context.Background(), dir, filename, meta, Entry{
			Term: "bad", Content: "bad", Embedding: vec,
		}); err == nil {
			t.Fatalf("CreateEntry(%v) should fail", vec)
		}
	}
	_, snap, err := ReadEntry(context.Background(), dir, filename, before.Entries[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, vec := range [][]float64{{1, 0, 0}, {math.Inf(1), 0}, {0, 0}, {math.MaxFloat64, math.MaxFloat64}} {
		if _, err := UpdateEntry(context.Background(), dir, filename, meta, snap.ID, snap.Revision, Entry{
			Term: "bad", Content: "bad", Embedding: vec,
		}); err == nil {
			t.Fatalf("UpdateEntry(%v) should fail", vec)
		}
	}
	after, err := Get(dir, filename)
	if err != nil {
		t.Fatal(err)
	}
	if len(after.Entries) != 1 || after.Entries[0].Term != "a" {
		t.Fatalf("db changed on failed writes: %+v", after.Entries)
	}
}

func TestMutateCancelledAndBadFilenames(t *testing.T) {
	dir := t.TempDir()
	meta, filename := createSearchBase(t, dir, searchMeta(2), []Entry{
		{Term: "a", Content: "c", Embedding: []float64{1, 0}},
	})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := CreateEntry(ctx, dir, filename, meta, Entry{
		Term: "x", Content: "y", Embedding: []float64{1, 0},
	}); !errors.Is(err, context.Canceled) {
		t.Fatalf("create cancelled: %v", err)
	}
	if err := DeleteEntry(ctx, dir, filename, meta, 1, "x"); !errors.Is(err, context.Canceled) {
		t.Fatalf("delete cancelled: %v", err)
	}
	for _, bad := range []string{"", "..", "../x.db", "a/b.db", "x.txt"} {
		if _, _, err := ReadEntry(context.Background(), dir, bad, 1); err == nil {
			t.Fatalf("ReadEntry(%q) should fail", bad)
		}
	}
	link := filepath.Join(dir, "link.db")
	if err := os.Symlink(filepath.Join(dir, filename), link); err == nil {
		if _, err := CreateEntry(context.Background(), dir, "link.db", meta, Entry{
			Term: "x", Content: "y", Embedding: []float64{1, 0},
		}); err == nil {
			t.Fatal("symlinked base should be rejected")
		}
	}
	if _, err := CreateEntry(context.Background(), dir, "missing.db", meta, Entry{
		Term: "x", Content: "y", Embedding: []float64{1, 0},
	}); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("missing base: %v", err)
	}
	other := searchMeta(2)
	other.EmbeddingModel = "other:latest"
	if _, err := CreateEntry(context.Background(), dir, filename, other, Entry{
		Term: "x", Content: "y", Embedding: []float64{1, 0},
	}); !errors.Is(err, ErrConflict) {
		t.Fatalf("meta mismatch: %v", err)
	}
}

func TestMutateMetadataAndLastEntryDelete(t *testing.T) {
	dir := t.TempDir()
	meta, filename := createSearchBase(t, dir, searchMeta(2), []Entry{
		{Term: "only", Content: "c", Embedding: []float64{1, 0}},
	})
	rev := MetaRevision(meta)
	updated, err := UpdateMetadata(context.Background(), dir, filename, rev, strptr("Renamed"), strptr("new desc"))
	if err != nil {
		t.Fatal(err)
	}
	if updated.Name != "Renamed" || updated.Description != "new desc" ||
		updated.EmbeddingModel != meta.EmbeddingModel || updated.EmbeddingDigest != meta.EmbeddingDigest ||
		updated.Dimensions != meta.Dimensions || updated.ID != meta.ID {
		t.Fatalf("meta = %+v", updated)
	}
	if _, err := UpdateMetadata(context.Background(), dir, filename, rev, strptr("x"), nil); !errors.Is(err, ErrConflict) {
		t.Fatalf("stale meta revision: %v", err)
	}

	detail, err := Get(dir, filename)
	if err != nil {
		t.Fatal(err)
	}
	_, snap, err := ReadEntry(context.Background(), dir, filename, detail.Entries[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := DeleteEntry(context.Background(), dir, filename, updated, snap.ID, snap.Revision); err != nil {
		t.Fatal(err)
	}
	empty, err := Get(dir, filename)
	if err != nil {
		t.Fatalf("empty base must still open: %v", err)
	}
	if len(empty.Entries) != 0 {
		t.Fatalf("entries = %+v", empty.Entries)
	}
	infos, _, err := List(dir)
	if err != nil || len(infos) != 1 || infos[0].Entries != 0 {
		t.Fatalf("List = %+v, %v", infos, err)
	}
	got, err := Search(context.Background(), dir, filename, updated, []float64{1, 0}, 10, 0)
	if err != nil || len(got) != 0 {
		t.Fatalf("search on empty base = %+v, %v", got, err)
	}
	snap2, err := CreateEntry(context.Background(), dir, filename, updated, Entry{
		Term: "again", Content: "c", Embedding: []float64{0, 1},
	})
	if err != nil {
		t.Fatalf("create after emptying: %v", err)
	}
	if snap2.ID <= 0 {
		t.Fatalf("new id = %d", snap2.ID)
	}
	if _, rsnap, err := ReadEntry(context.Background(), dir, filename, snap2.ID); err != nil || rsnap.Entry.Term != "again" {
		t.Fatalf("recreated entry: %+v %v", rsnap, err)
	}
}

func strptr(s string) *string { return &s }

func mkLegacyBase(t *testing.T, dir, name string, version int) string {
	t.Helper()
	emb := packVector([]float64{1, 0})
	path := filepath.Join(dir, name)
	db, err := sql.Open("sqlite", fileURI(path, "rwc"))
	if err != nil {
		t.Fatal(err)
	}
	var sqlText string
	switch version {
	case 1:
		sqlText = `CREATE TABLE metadata (key INTEGER PRIMARY KEY CHECK (key = 1),
			schema_version INTEGER NOT NULL, id TEXT NOT NULL, name TEXT NOT NULL, description TEXT NOT NULL,
			created_at INTEGER NOT NULL, embedding_provider TEXT NOT NULL, embedding_model TEXT NOT NULL,
			embedding_digest TEXT NOT NULL, dimensions INTEGER NOT NULL, input_format TEXT NOT NULL);
			CREATE TABLE entries (id INTEGER PRIMARY KEY, term TEXT NOT NULL, content TEXT NOT NULL,
			media_type TEXT NOT NULL, media_name TEXT NOT NULL, media_mime TEXT NOT NULL, media BLOB,
			embedding BLOB NOT NULL);
			INSERT INTO metadata VALUES (1, 1, 'idv1', 'V1', '', 1, 'ollama', 'm', 'd', 2, 'term-content-v1');
			INSERT INTO entries (term, content, media_type, media_name, media_mime, embedding)
			VALUES ('hit', 'v1 entry', 'text', '', '', ?)`
	case 2:
		sqlText = `CREATE TABLE metadata (key INTEGER PRIMARY KEY CHECK (key = 1),
			schema_version INTEGER NOT NULL, id TEXT NOT NULL, name TEXT NOT NULL, description TEXT NOT NULL,
			created_at INTEGER NOT NULL, embedding_provider TEXT NOT NULL, embedding_model TEXT NOT NULL,
			embedding_digest TEXT NOT NULL, dimensions INTEGER NOT NULL, input_format TEXT NOT NULL);
			CREATE TABLE entries (id INTEGER PRIMARY KEY, term TEXT NOT NULL, content TEXT NOT NULL,
			input_mode TEXT NOT NULL, image_name TEXT NOT NULL, image_mime TEXT NOT NULL, image BLOB,
			audio_name TEXT NOT NULL, audio_mime TEXT NOT NULL, audio BLOB, embedding BLOB NOT NULL);
			INSERT INTO metadata VALUES (1, 2, 'idv2', 'V2', '', 10, 'ollama', 'm', 'd', 2, 'entry-inputs-v2');
			INSERT INTO entries (term, content, input_mode, image_name, image_mime, image,
				audio_name, audio_mime, audio, embedding)
			VALUES ('hit', 'v2 entry', 'combined', '', '', NULL, '', '', NULL, ?)`
	default:
		t.Fatalf("bad version %d", version)
	}
	if _, err := db.Exec(sqlText, emb); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	return name
}

func TestMutateLegacyVersions(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"v1.db", "v2.db"} {
		version := 1
		if name == "v2.db" {
			version = 2
		}
		mkLegacyBase(t, dir, name, version)
		d, err := Get(dir, name)
		if err != nil {
			t.Fatalf("Get(%s): %v", name, err)
		}
		meta, snap, err := ReadEntry(context.Background(), dir, name, d.Entries[0].ID)
		if err != nil {
			t.Fatalf("ReadEntry(%s): %v", name, err)
		}
		if snap.Entry.Term != "hit" {
			t.Fatalf("entry = %+v", snap.Entry)
		}
		created, err := CreateEntry(context.Background(), dir, name, meta, Entry{
			Term: "added", Content: "legacy add", Embedding: []float64{0, 1},
		})
		if err != nil {
			t.Fatalf("CreateEntry(%s): %v", name, err)
		}
		up, err := UpdateEntry(context.Background(), dir, name, meta, created.ID, created.Revision, Entry{
			Term: "added2", Content: "legacy edit", Embedding: []float64{1, 1},
		})
		if err != nil {
			t.Fatalf("UpdateEntry(%s): %v", name, err)
		}
		if up.Entry.Term != "added2" {
			t.Fatalf("updated = %+v", up.Entry)
		}
		if err := DeleteEntry(context.Background(), dir, name, meta, created.ID, up.Revision); err != nil {
			t.Fatalf("DeleteEntry(%s): %v", name, err)
		}
		nm, err := UpdateMetadata(context.Background(), dir, name, MetaRevision(meta), strptr("NewName"), nil)
		if err != nil {
			t.Fatalf("UpdateMetadata(%s): %v", name, err)
		}
		if nm.Name != "NewName" {
			t.Fatalf("meta = %+v", nm)
		}
	}
	meta1, snap1, err := ReadEntry(context.Background(), dir, "v1.db", 1)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := UpdateEntry(context.Background(), dir, "v1.db", meta1, snap1.ID, snap1.Revision, Entry{
		Term: "hit", Content: "v1 entry", InputMode: "media", Embedding: []float64{1, 0},
	}); err == nil {
		t.Fatal("v1 media input mode should be rejected")
	}
}

func TestMutateRejectsCorruptMetadataAndMediaRules(t *testing.T) {
	dir := t.TempDir()
	meta, filename := createSearchBase(t, dir, searchMeta(2), []Entry{
		{Term: "a", Content: "c", Embedding: []float64{1, 0}},
	})
	path := filepath.Join(dir, filename)
	db, err := sql.Open("sqlite", fileURI(path, "rw"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE metadata SET embedding_provider = 'bogus' WHERE key = 1`); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := CreateEntry(context.Background(), dir, filename, meta, Entry{
		Term: "x", Content: "y", Embedding: []float64{1, 0},
	}); err == nil {
		t.Fatal("corrupt provider must block create")
	}
	if _, err := UpdateMetadata(context.Background(), dir, filename, MetaRevision(meta), strptr("x"), nil); err == nil {
		t.Fatal("corrupt provider must block metadata update")
	}
	if err := DeleteEntry(context.Background(), dir, filename, meta, 1, "x"); err == nil {
		t.Fatal("corrupt provider must block delete")
	}
}

func TestMutateCreateRejectsMediaOnlyWithoutAttachment(t *testing.T) {
	dir := t.TempDir()
	meta, filename := createSearchBase(t, dir, searchMeta(2), []Entry{
		{Term: "a", Content: "c", Embedding: []float64{1, 0}},
	})
	if _, err := CreateEntry(context.Background(), dir, filename, meta, Entry{
		Term: "x", InputMode: "media", Embedding: []float64{1, 0},
	}); err == nil {
		t.Fatal("media-only create without attachment must fail")
	}
	name := mkLegacyBase(t, dir, "v1media.db", 1)
	d1, err := Get(dir, name)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := CreateEntry(context.Background(), dir, name, d1.Meta, Entry{
		Term: "x", Content: "y", Embedding: []float64{1, 0},
		Media: []Media{{Type: "image", Name: "a.png", MIME: "image/png", Data: []byte{1}}},
	}); err == nil {
		t.Fatal("v1 create with media bytes must fail")
	}
	if _, err := UpdateMetadata(context.Background(), dir, filename, MetaRevision(meta), strptr("  "), nil); err == nil {
		t.Fatal("empty name must fail")
	}
	after, err := Get(dir, filename)
	if err != nil || after.Meta.Name != meta.Name {
		t.Fatalf("rejected rename changed meta: %+v", after.Meta)
	}
}

func TestMutateDoesNotWriteOnEmbedlessPath(t *testing.T) {
	dir := t.TempDir()
	meta, filename := createSearchBase(t, dir, searchMeta(2), []Entry{
		{Term: "a", Content: "c", Embedding: []float64{1, 0}},
	})
	deadline, cancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer cancel()
	if _, err := CreateEntry(deadline, dir, filename, meta, Entry{
		Term: "x", Content: "y", Embedding: []float64{1, 0},
	}); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expired ctx: %v", err)
	}
	d, err := Get(dir, filename)
	if err != nil || len(d.Entries) != 1 {
		t.Fatalf("entries = %+v, %v", d.Entries, err)
	}
}

func mkAliasLegacyBase(t *testing.T, dir, name string, version int) string {
	t.Helper()
	emb := packVector([]float64{1, 0})
	png := []byte{9, 9, 9}
	path := filepath.Join(dir, name)
	db, err := sql.Open("sqlite", fileURI(path, "rwc"))
	if err != nil {
		t.Fatal(err)
	}
	var sqlText string
	rows := [][]any{}
	switch version {
	case 1:
		sqlText = `CREATE TABLE metadata (key INTEGER PRIMARY KEY CHECK (key = 1),
			schema_version INTEGER NOT NULL, id TEXT NOT NULL, name TEXT NOT NULL, description TEXT NOT NULL,
			created_at INTEGER NOT NULL, embedding_provider TEXT NOT NULL, embedding_model TEXT NOT NULL,
			embedding_digest TEXT NOT NULL, dimensions INTEGER NOT NULL, input_format TEXT NOT NULL);
			CREATE TABLE entries (id INTEGER PRIMARY KEY, term TEXT NOT NULL, content TEXT NOT NULL,
			media_type TEXT NOT NULL, media_name TEXT NOT NULL, media_mime TEXT NOT NULL, media BLOB,
			embedding BLOB NOT NULL);
			INSERT INTO metadata VALUES (1, 1, 'idv1', 'V1', '', 500, 'ollama', 'm', 'd', 2, 'term-content-v1')`
		rows = [][]any{
			{`INSERT INTO entries (term, content, media_type, media_name, media_mime, embedding)
				VALUES ('hit', 'v1 entry', 'text', '', '', ?)`, []any{emb}},
			{`INSERT INTO entries (term, content, media_type, media_name, media_mime, media, embedding)
				VALUES ('pic', 'v1 image', 'image', 'i.png', 'image/png', ?, ?)`, []any{png, emb}},
		}
	case 2:
		sqlText = `CREATE TABLE metadata (key INTEGER PRIMARY KEY CHECK (key = 1),
			schema_version INTEGER NOT NULL, id TEXT NOT NULL, name TEXT NOT NULL, description TEXT NOT NULL,
			created_at INTEGER NOT NULL, embedding_provider TEXT NOT NULL, embedding_model TEXT NOT NULL,
			embedding_digest TEXT NOT NULL, dimensions INTEGER NOT NULL, input_format TEXT NOT NULL);
			CREATE TABLE entries (id INTEGER PRIMARY KEY, term TEXT NOT NULL, content TEXT NOT NULL,
			input_mode TEXT NOT NULL, image_name TEXT NOT NULL, image_mime TEXT NOT NULL, image BLOB,
			audio_name TEXT NOT NULL, audio_mime TEXT NOT NULL, audio BLOB, embedding BLOB NOT NULL);
			INSERT INTO metadata VALUES (1, 2, 'idv2', 'V2', '', 600, 'ollama', 'm', 'd', 2, 'entry-inputs-v2')`
		rows = [][]any{
			{`INSERT INTO entries (term, content, input_mode, image_name, image_mime, image,
				audio_name, audio_mime, audio, embedding)
				VALUES ('hit', 'v2 entry', 'combined', '', '', NULL, '', '', NULL, ?)`, []any{emb}},
			{`INSERT INTO entries (term, content, input_mode, image_name, image_mime, image,
				audio_name, audio_mime, audio, embedding)
				VALUES ('pic', 'v2 media', 'media', 'i.png', 'image/png', ?, 'a.mp3', 'audio/mp3', ?, ?)`, []any{png, png, emb}},
		}
	case 3:
		sqlText = `CREATE TABLE metadata (key INTEGER PRIMARY KEY CHECK (key = 1),
			schema_version INTEGER NOT NULL, id TEXT NOT NULL, name TEXT NOT NULL, description TEXT NOT NULL,
			created_at INTEGER NOT NULL, updated_at INTEGER NOT NULL, embedding_provider TEXT NOT NULL,
			embedding_model TEXT NOT NULL, embedding_digest TEXT NOT NULL, dimensions INTEGER NOT NULL,
			input_format TEXT NOT NULL);
			CREATE TABLE entries (id INTEGER PRIMARY KEY, term TEXT NOT NULL, content TEXT NOT NULL,
			input_mode TEXT NOT NULL, created_at INTEGER NOT NULL, updated_at INTEGER NOT NULL,
			image_name TEXT NOT NULL, image_mime TEXT NOT NULL, image BLOB,
			audio_name TEXT NOT NULL, audio_mime TEXT NOT NULL, audio BLOB, embedding BLOB NOT NULL);
			INSERT INTO metadata VALUES (1, 3, 'idv3', 'V3', '', 700, 800, 'ollama', 'm', 'd', 2, 'entry-inputs-v3')`
		rows = [][]any{
			{`INSERT INTO entries (term, content, input_mode, created_at, updated_at, image_name, image_mime,
				image, audio_name, audio_mime, audio, embedding)
				VALUES ('hit', 'v3 entry', 'combined', 1000, 1100, '', '', NULL, '', '', NULL, ?)`, []any{emb}},
			{`INSERT INTO entries (term, content, input_mode, created_at, updated_at, image_name, image_mime,
				image, audio_name, audio_mime, audio, embedding)
				VALUES ('pic', 'v3 media', 'combined', 2000, 2100, 'i.png', 'image/png', ?, '', '', NULL, ?)`, []any{png, emb}},
		}
	default:
		t.Fatalf("bad version %d", version)
	}
	if _, err := db.Exec(sqlText); err != nil {
		t.Fatal(err)
	}
	for _, row := range rows {
		if _, err := db.Exec(row[0].(string), row[1].([]any)...); err != nil {
			t.Fatal(err)
		}
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	return name
}

func schemaVersionOf(t *testing.T, path string) int {
	t.Helper()
	db, err := sql.Open("sqlite", fileURI(path, "ro"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var v int
	if err := db.QueryRow(`SELECT schema_version FROM metadata WHERE key = 1`).Scan(&v); err != nil {
		t.Fatal(err)
	}
	return v
}

func TestLegacyReadsAliasesEmpty(t *testing.T) {
	dir := t.TempDir()
	for _, version := range []int{1, 2, 3} {
		name := mkAliasLegacyBase(t, dir, "legacy"+string(rune('0'+version))+".db", version)
		d, err := Get(dir, name)
		if err != nil {
			t.Fatalf("Get v%d: %v", version, err)
		}
		for _, e := range d.Entries {
			if e.Aliases == nil || len(e.Aliases) != 0 {
				t.Fatalf("v%d read aliases = %#v", version, e.Aliases)
			}
		}
		_, snap, err := ReadEntry(context.Background(), dir, name, d.Entries[0].ID)
		if err != nil || snap.Entry.Aliases == nil || len(snap.Entry.Aliases) != 0 {
			t.Fatalf("v%d snapshot aliases = %#v %v", version, snap.Entry.Aliases, err)
		}
		if got := schemaVersionOf(t, filepath.Join(dir, name)); got != version {
			t.Fatalf("read migrated schema to %d", got)
		}
	}
}

func TestLegacyAliasWriteMigrates(t *testing.T) {
	for _, version := range []int{1, 2, 3} {
		t.Run(string(rune('a'+version-1)), func(t *testing.T) {
			dir := t.TempDir()
			name := mkAliasLegacyBase(t, dir, "legacy.db", version)
			meta, err := func() (Meta, error) {
				d, err := Get(dir, name)
				if err != nil {
					return Meta{}, err
				}
				return d.Meta, nil
			}()
			if err != nil {
				t.Fatal(err)
			}
			d, _ := Get(dir, name)
			firstID := d.Entries[0].ID
			secondID := d.Entries[1].ID
			_, snap1, err := ReadEntry(context.Background(), dir, name, firstID)
			if err != nil {
				t.Fatal(err)
			}
			_, snap2, err := ReadEntry(context.Background(), dir, name, secondID)
			if err != nil {
				t.Fatal(err)
			}
			fresh, err := UpdateEntry(context.Background(), dir, name, meta, firstID, snap1.Revision, Entry{
				Term: "hit", Content: snap1.Entry.Content, Aliases: []string{" one ", "TWO", "One"},
				Embedding: []float64{0, 1},
			})
			if err != nil {
				t.Fatalf("alias write on v%d: %v", version, err)
			}
			if got := schemaVersionOf(t, filepath.Join(dir, name)); got != SchemaVersion {
				t.Fatalf("schema after alias write = %d", got)
			}
			m2, snapA, err := ReadEntry(context.Background(), dir, name, firstID)
			if err != nil {
				t.Fatal(err)
			}
			if m2.InputFormat != InputFormat || m2.ID != meta.ID || m2.EmbeddingDigest != meta.EmbeddingDigest {
				t.Fatalf("meta after migration = %+v", m2)
			}
			if want := []string{"one", "TWO"}; !reflect.DeepEqual(snapA.Entry.Aliases, want) {
				t.Fatalf("aliases = %v", snapA.Entry.Aliases)
			}
			if snapA.Revision == snap1.Revision {
				t.Fatal("alias edit did not change revision")
			}
			if fresh.Revision != snapA.Revision {
				t.Fatal("returned revision mismatch")
			}
			_, snapB, err := ReadEntry(context.Background(), dir, name, secondID)
			if err != nil || snapB.Revision != snap2.Revision {
				t.Fatalf("untouched entry changed: %v %v", snapB.Entry, err)
			}
			if len(snapB.Entry.Media) == 0 {
				t.Fatal("legacy media lost in migration")
			}
			media, err := MediaAt(dir, name, secondID, "image")
			if err != nil || len(media.Data) == 0 {
				t.Fatalf("MediaAt after migration = %v %v", media, err)
			}
			switch version {
			case 3:
				if snapB.Entry.CreatedAt != 2000 || snapB.Entry.UpdatedAt != 2100 {
					t.Fatalf("v3 timestamps mutated: %+v", snapB.Entry)
				}
			default:
				if snapB.Entry.CreatedAt != meta.CreatedAt {
					t.Fatalf("legacy timestamp fill = %v, want %v", snapB.Entry.CreatedAt, meta.CreatedAt)
				}
			}
			created, err := CreateEntry(context.Background(), dir, name, m2, Entry{
				Term: "new", Content: "post-migration", Aliases: []string{"n1"}, Embedding: []float64{1, 0},
			})
			if err != nil {
				t.Fatalf("create after migration: %v", err)
			}
			if !reflect.DeepEqual(created.Entry.Aliases, []string{"n1"}) {
				t.Fatalf("created aliases = %v", created.Entry.Aliases)
			}
		})
	}
}

func TestLegacyAliasWriteFailuresLeaveBase(t *testing.T) {
	dir := t.TempDir()
	name := mkAliasLegacyBase(t, dir, "legacy.db", 3)
	d, err := Get(dir, name)
	if err != nil {
		t.Fatal(err)
	}
	meta := d.Meta
	id := d.Entries[0].ID
	_, snap, err := ReadEntry(context.Background(), dir, name, id)
	if err != nil {
		t.Fatal(err)
	}
	rawBefore, err := os.ReadFile(filepath.Join(dir, name))
	if err != nil {
		t.Fatal(err)
	}
	tooMany := make([]string, MaxAliases+1)
	for i := range tooMany {
		tooMany[i] = fmt.Sprintf("a%d", i)
	}
	if _, err := UpdateEntry(context.Background(), dir, name, meta, id, snap.Revision, Entry{
		Term: "hit", Content: "c", Aliases: tooMany, Embedding: []float64{0, 1},
	}); err == nil {
		t.Fatal("oversized aliases accepted")
	}
	if _, err := UpdateEntry(context.Background(), dir, name, meta, id, "stale", Entry{
		Term: "hit", Content: "c", Aliases: []string{"x"}, Embedding: []float64{0, 1},
	}); !errors.Is(err, ErrConflict) {
		t.Fatalf("stale revision = %v", err)
	}
	if _, err := UpdateEntry(context.Background(), dir, name, meta, id, snap.Revision, Entry{
		Term: "hit", Content: "c", Aliases: []string{"x"}, Embedding: []float64{0, 0},
	}); err == nil {
		t.Fatal("invalid vector accepted")
	}
	rawAfter, err := os.ReadFile(filepath.Join(dir, name))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(rawBefore, rawAfter) {
		t.Fatal("failed alias writes modified the base")
	}
	if got := schemaVersionOf(t, filepath.Join(dir, name)); got != 3 {
		t.Fatalf("failed writes migrated schema to %d", got)
	}
	if _, err := CreateEntry(context.Background(), dir, name, meta, Entry{
		Term: "added", Content: "no aliases", Embedding: []float64{1, 0},
	}); err != nil {
		t.Fatal(err)
	}
	if got := schemaVersionOf(t, filepath.Join(dir, name)); got != 3 {
		t.Fatalf("no-alias write migrated schema to %d", got)
	}
}

func TestLegacyAliasCreateMigrates(t *testing.T) {
	for _, version := range []int{1, 2, 3} {
		t.Run(string(rune('a'+version-1)), func(t *testing.T) {
			dir := t.TempDir()
			name := mkAliasLegacyBase(t, dir, "legacy.db", version)
			d, err := Get(dir, name)
			if err != nil {
				t.Fatal(err)
			}
			secondID := d.Entries[1].ID
			_, snap2, err := ReadEntry(context.Background(), dir, name, secondID)
			if err != nil {
				t.Fatal(err)
			}
			created, err := CreateEntry(context.Background(), dir, name, d.Meta, Entry{
				Term: "new", Content: "via create", Aliases: []string{" n1 ", "N2"}, Embedding: []float64{1, 0},
			})
			if err != nil {
				t.Fatalf("alias create on v%d: %v", version, err)
			}
			if got := schemaVersionOf(t, filepath.Join(dir, name)); got != SchemaVersion {
				t.Fatalf("schema after alias create = %d", got)
			}
			if want := []string{"n1", "N2"}; !reflect.DeepEqual(created.Entry.Aliases, want) {
				t.Fatalf("created aliases = %v", created.Entry.Aliases)
			}
			m2, snapB, err := ReadEntry(context.Background(), dir, name, secondID)
			if err != nil || m2.InputFormat != InputFormat {
				t.Fatalf("after create-migration: %v %v", m2.InputFormat, err)
			}
			if snapB.Revision != snap2.Revision || len(snapB.Entry.Media) == 0 {
				t.Fatalf("untouched entry changed: %+v", snapB.Entry)
			}
		})
	}
}

func TestLegacyAliasWritePostMigrationRollback(t *testing.T) {
	dir := t.TempDir()
	name := mkAliasLegacyBase(t, dir, "legacy.db", 3)
	path := filepath.Join(dir, name)
	db, err := sql.Open("sqlite", fileURI(path, "rw"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`CREATE TRIGGER fail_term BEFORE UPDATE OF term ON entries
		BEGIN SELECT RAISE(ABORT, 'forced write failure'); END;`); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	rawBefore, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	d, err := Get(dir, name)
	if err != nil {
		t.Fatal(err)
	}
	id := d.Entries[0].ID
	_, snap, err := ReadEntry(context.Background(), dir, name, id)
	if err != nil {
		t.Fatal(err)
	}
	_, err = UpdateEntry(context.Background(), dir, name, d.Meta, id, snap.Revision, Entry{
		Term: "changed", Content: snap.Entry.Content, Aliases: []string{"x"}, Embedding: []float64{0, 1},
	})
	if err == nil || !strings.Contains(err.Error(), "forced write failure") {
		t.Fatalf("update = %v, want trigger abort", err)
	}
	if got := schemaVersionOf(t, path); got != 3 {
		t.Fatalf("failed write left schema %d, migration must roll back", got)
	}
	rawAfter, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(rawBefore, rawAfter) {
		t.Fatal("failed write left data modifications")
	}
	d2, err := Get(dir, name)
	if err != nil || len(d2.Entries) != 2 || d2.Entries[0].Term != "hit" {
		t.Fatalf("base unreadable after rollback: %v", err)
	}
}
