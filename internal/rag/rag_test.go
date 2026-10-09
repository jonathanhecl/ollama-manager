package rag

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/binary"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"

	_ "modernc.org/sqlite"
)

func testMeta() Meta {
	return Meta{Name: "Base", EmbeddingModel: "nomic-embed-text:latest", EmbeddingDigest: "sha256:abc", Dimensions: 3}
}

func testEntries(n int) []Entry {
	out := make([]Entry, n)
	for i := range out {
		out[i] = Entry{Term: "term", Content: "content", InputMode: "combined",
			Embedding: []float64{0.1, 0.2, 0.3}}
	}
	return out
}

func TestCreateRoundTrip(t *testing.T) {
	dir := t.TempDir()
	meta := testMeta()
	meta.Name = "My Base"
	meta.Description = "d"
	got, filename, err := Create(context.Background(), dir, meta, testEntries(3))
	if err != nil {
		t.Fatal(err)
	}
	if got.Name != "My Base" || got.EmbeddingProvider != EmbeddingProvider ||
		got.InputFormat != InputFormat || got.ID == "" || got.CreatedAt == 0 || got.UpdatedAt != got.CreatedAt {
		t.Fatalf("unexpected meta: %+v", got)
	}
	fis, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(fis) != 1 || fis[0].Name() != filename {
		names := []string{}
		for _, f := range fis {
			names = append(names, f.Name())
		}
		t.Fatalf("dir contents = %v, want only %q", names, filename)
	}
	head, err := os.ReadFile(filepath.Join(dir, filename))
	if err != nil {
		t.Fatal(err)
	}
	if string(head[:16]) != "SQLite format 3\x00" {
		t.Fatalf("bad header %q", head[:16])
	}

	d, err := Get(dir, filename)
	if err != nil {
		t.Fatal(err)
	}
	if d.Meta.Dimensions != 3 || d.Meta.EmbeddingModel != "nomic-embed-text:latest" ||
		d.Meta.EmbeddingDigest != "sha256:abc" {
		t.Fatalf("unexpected detail meta: %+v", d.Meta)
	}
	if len(d.Entries) != 3 || d.Entries[0].Term != "term" || d.Entries[0].InputMode != "combined" ||
		d.Entries[0].CreatedAt == 0 || d.Entries[0].UpdatedAt != d.Entries[0].CreatedAt ||
		len(d.Entries[0].Media) != 0 {
		t.Fatalf("unexpected entries: %+v", d.Entries)
	}
	infos, warnings, err := List(dir)
	if err != nil || len(warnings) != 0 {
		t.Fatalf("List err=%v warnings=%v", err, warnings)
	}
	if len(infos) != 1 || infos[0].Entries != 3 || infos[0].Dimensions != 3 {
		t.Fatalf("unexpected info: %+v", infos)
	}
}

func TestVectorBytesExactRoundTrip(t *testing.T) {
	dir := t.TempDir()
	vec := []float64{0.1, -2.5, math.MaxFloat64, math.SmallestNonzeroFloat64}
	m := testMeta()
	m.Dimensions = 4
	_, filename, err := Create(context.Background(), dir, m,
		[]Entry{{Term: "t", Content: "c", Embedding: vec}})
	if err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", fileURI(filepath.Join(dir, filename), "ro"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var blob []byte
	if err := db.QueryRow(`SELECT embedding FROM entries`).Scan(&blob); err != nil {
		t.Fatal(err)
	}
	want := packVector(vec)
	if len(blob) != len(want) {
		t.Fatalf("blob len = %d", len(blob))
	}
	for i := range blob {
		if blob[i] != want[i] {
			t.Fatalf("byte %d: got %x want %x", i, blob[i], want[i])
		}
	}
	for i := range vec {
		got := math.Float64frombits(binary.LittleEndian.Uint64(blob[i*8:]))
		if got != vec[i] {
			t.Fatalf("vec[%d] = %v want %v", i, got, vec[i])
		}
	}
}

func TestCreateDuplicateNamesGetUniqueFiles(t *testing.T) {
	dir := t.TempDir()
	_, f1, err := Create(context.Background(), dir, testMeta(), testEntries(1))
	if err != nil {
		t.Fatal(err)
	}
	m2 := testMeta()
	_, f2, err := Create(context.Background(), dir, m2, testEntries(1))
	if err != nil {
		t.Fatal(err)
	}
	if f1 == f2 {
		t.Fatalf("duplicate filename %q", f1)
	}
	infos, _, err := List(dir)
	if err != nil || len(infos) != 2 {
		t.Fatalf("List = %v, %v", infos, err)
	}
}

func TestReplaceKeepsFilenameAndID(t *testing.T) {
	dir := t.TempDir()
	meta, filename, err := Create(context.Background(), dir, testMeta(), testEntries(1))
	if err != nil {
		t.Fatal(err)
	}
	updatedMeta := meta
	updatedMeta.Name = "Renamed Base"
	updatedMeta.Description = "updated"
	got, err := Replace(context.Background(), dir, filename, updatedMeta, []Entry{
		{Term: "new", Content: "updated", InputMode: "combined", Embedding: []float64{1, 2, 3}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != meta.ID || got.CreatedAt != meta.CreatedAt || got.UpdatedAt < got.CreatedAt {
		t.Fatalf("replace changed identity: %+v", got)
	}
	fis, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, f := range fis {
		names = append(names, f.Name())
	}
	if len(names) != 1 || names[0] != filename {
		t.Fatalf("replace files = %v, want only %q", names, filename)
	}
	d, err := Get(dir, filename)
	if err != nil {
		t.Fatal(err)
	}
	if d.Meta.Name != "Renamed Base" || len(d.Entries) != 1 || d.Entries[0].Term != "new" {
		t.Fatalf("updated detail = %+v", d)
	}
}

func TestCreateRejectsBadVectors(t *testing.T) {
	dir := t.TempDir()
	bad := []Entry{{Term: "t", Content: "c", Embedding: []float64{0.1, math.NaN(), 0.3}}}
	cases := []struct {
		name    string
		dims    int
		entries []Entry
	}{
		{"no entries", 3, nil},
		{"wrong dims", 4, testEntries(1)},
		{"nonfinite", 3, bad},
		{"inconsistent", 3, []Entry{
			{Term: "t", Content: "c", Embedding: []float64{1, 2, 3}},
			{Term: "t", Content: "c", Embedding: []float64{1, 2}},
		}},
		{"huge dims", 70000, testEntries(1)},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			m := testMeta()
			m.Dimensions = tt.dims
			_, _, err := Create(context.Background(), dir, m, tt.entries)
			if err == nil {
				t.Fatal("expected error")
			}
			fis, _ := os.ReadDir(dir)
			if len(fis) != 0 {
				names := []string{}
				for _, f := range fis {
					names = append(names, f.Name())
				}
				t.Fatalf("failure left files %v", names)
			}
		})
	}
}

func TestCreateCancelledPublishesNothing(t *testing.T) {
	dir := t.TempDir()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, _, err := Create(ctx, dir, testMeta(), testEntries(1))
	if err == nil {
		t.Fatal("expected cancellation error")
	}
	fis, _ := os.ReadDir(dir)
	if len(fis) != 0 {
		t.Fatalf("cancelled create left %d files", len(fis))
	}
	infos, warnings, err := List(dir)
	if err != nil || len(infos) != 0 || len(warnings) != 0 {
		t.Fatalf("List after cancel = %v warnings=%v err=%v", infos, warnings, err)
	}
}

func TestCreateInSpecialDir(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "weird ?#ü dir")
	m := testMeta()
	m.Name = "Unicode Ünïcode"
	_, filename, err := Create(context.Background(), dir, m, testEntries(1))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Get(dir, filename); err != nil {
		t.Fatalf("Get in special dir: %v", err)
	}
	infos, warnings, err := List(dir)
	if err != nil || len(warnings) != 0 || len(infos) != 1 {
		t.Fatalf("List = %v warnings=%v err=%v", infos, warnings, err)
	}
}

func TestImportValidatesAndCopiesDB(t *testing.T) {
	src := t.TempDir()
	m := testMeta()
	m.Name = "Imported Base"
	_, filename, err := Create(context.Background(), src, m, testEntries(2))
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(src, filename))
	if err != nil {
		t.Fatal(err)
	}

	dst := t.TempDir()
	info, err := Import(dst, "source.db", bytes.NewReader(raw))
	if err != nil {
		t.Fatalf("Import: %v", err)
	}
	if info.Filename == "" || info.Filename == filename || !ValidFilename(info.Filename) {
		t.Fatalf("unexpected imported filename %q", info.Filename)
	}
	if info.Name != "Imported Base" || info.Entries != 2 || info.Dimensions != 3 {
		t.Fatalf("unexpected imported info: %+v", info)
	}
	if _, err := Get(dst, info.Filename); err != nil {
		t.Fatalf("imported base is not readable: %v", err)
	}

	before := countFiles(t, dst)
	if _, err := Import(dst, "../unsafe/source.db", bytes.NewReader(raw)); err == nil {
		t.Fatal("unsafe upload filename succeeded")
	}
	if _, err := Import(dst, "bad.db", strings.NewReader("not sqlite")); err == nil {
		t.Fatal("invalid upload succeeded")
	}
	if got := countFiles(t, dst); got != before {
		t.Fatalf("invalid import left %d files, want %d", got, before)
	}
}

func countFiles(t *testing.T, dir string) int {
	t.Helper()
	fis, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	return len(fis)
}

func TestListSeesCopiedDB(t *testing.T) {
	src := t.TempDir()
	m := testMeta()
	m.Name = "Copied"
	_, filename, err := Create(context.Background(), src, m, testEntries(2))
	if err != nil {
		t.Fatal(err)
	}
	dst := t.TempDir()
	data, err := os.ReadFile(filepath.Join(src, filename))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dst, "hand-copied.db"), data, 0o644); err != nil {
		t.Fatal(err)
	}
	infos, warnings, err := List(dst)
	if err != nil {
		t.Fatal(err)
	}
	if len(warnings) != 0 || len(infos) != 1 || infos[0].Filename != "hand-copied.db" || infos[0].Name != "Copied" {
		t.Fatalf("List = %+v warnings=%v", infos, warnings)
	}
}

func TestListCorruptAndUnsupported(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "junk.db"), []byte("not sqlite"), 0o644); err != nil {
		t.Fatal(err)
	}
	wrongVer := filepath.Join(dir, "v2.db")
	db, err := sql.Open("sqlite", fileURI(wrongVer, "rwc"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`CREATE TABLE metadata (key INTEGER PRIMARY KEY, schema_version INTEGER,
		id TEXT, name TEXT, description TEXT, created_at INTEGER, embedding_provider TEXT,
		embedding_model TEXT, embedding_digest TEXT, dimensions INTEGER, input_format TEXT);
		INSERT INTO metadata VALUES (1, 99, 'x', 'n', '', 0, 'ollama', 'm', 'd', 3, 'fmt');
		CREATE TABLE entries (id INTEGER PRIMARY KEY, term TEXT, content TEXT, media_type TEXT,
		media_name TEXT, media_mime TEXT, media BLOB, embedding BLOB);`); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	realDir := t.TempDir()
	rm := testMeta()
	rm.Name = "Real"
	_, realFile, err := Create(context.Background(), realDir, rm, testEntries(1))
	if err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "linked.db")
	if err := os.Symlink(filepath.Join(realDir, realFile), link); err != nil {
		t.Skipf("symlink unsupported: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "notes.txt"), []byte("hi"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".tmp-leftover.db"), []byte("not sqlite"), 0o644); err != nil {
		t.Fatal(err)
	}

	infos, warnings, err := List(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(infos) != 0 {
		t.Fatalf("expected no valid bases, got %+v", infos)
	}
	if len(warnings) != 2 {
		t.Fatalf("warnings = %v, want 2 (corrupt + version)", warnings)
	}
	for _, w := range warnings {
		if strings.Contains(w, "linked.db") || strings.Contains(w, ".tmp-") || strings.Contains(w, "notes.txt") {
			t.Fatalf("unexpected warning for skipped file: %s", w)
		}
	}
}

func TestListBadMetadata(t *testing.T) {
	dir := t.TempDir()
	mk := func(name, provider, model, digest, inFmt string, dims int) string {
		p := filepath.Join(dir, name)
		db, err := sql.Open("sqlite", fileURI(p, "rwc"))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := db.Exec(`CREATE TABLE metadata (key INTEGER PRIMARY KEY, schema_version INTEGER,
			id TEXT, name TEXT, description TEXT, created_at INTEGER, embedding_provider TEXT,
			embedding_model TEXT, embedding_digest TEXT, dimensions INTEGER, input_format TEXT);
			CREATE TABLE entries (id INTEGER PRIMARY KEY, term TEXT, content TEXT, media_type TEXT,
			media_name TEXT, media_mime TEXT, media BLOB, embedding BLOB);`); err != nil {
			t.Fatal(err)
		}
		emb := packVector([]float64{1, 2, 3})
		if _, err := db.Exec(`INSERT INTO metadata VALUES (1, 1, 'id1', 'n', '', 0, ?, ?, ?, ?, ?);
			INSERT INTO entries (term, content, media_type, media_name, media_mime, embedding)
			VALUES ('t','c','text','','',?)`, provider, model, digest, dims, inFmt, emb); err != nil {
			t.Fatal(err)
		}
		if err := db.Close(); err != nil {
			t.Fatal(err)
		}
		return name
	}
	mk("bad-provider.db", "openai", "m", "d", InputFormat, 3)
	mk("bad-format.db", "ollama", "m", "d", "other", 3)
	mk("no-model.db", "ollama", "", "d", InputFormat, 3)
	mk("no-digest.db", "ollama", "m", "", InputFormat, 3)
	mk("no-entries.db", "ollama", "m", "d", InputFormat, 3)
	if db, err := sql.Open("sqlite", fileURI(filepath.Join(dir, "no-entries.db"), "rw")); err == nil {
		_, _ = db.Exec(`DELETE FROM entries`)
		_ = db.Close()
	}
	infos, warnings, err := List(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(infos) != 0 || len(warnings) != 5 {
		t.Fatalf("infos=%v warnings=%v", infos, warnings)
	}
}

func TestGetReadsV1Base(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "legacy.db")
	db, err := sql.Open("sqlite", fileURI(path, "rwc"))
	if err != nil {
		t.Fatal(err)
	}
	media := []byte{0x89, 0x50, 0x4E, 0x47}
	emb := packVector([]float64{1, 2, 3})
	if _, err := db.Exec(`CREATE TABLE metadata (key INTEGER PRIMARY KEY CHECK (key = 1),
		schema_version INTEGER NOT NULL, id TEXT NOT NULL, name TEXT NOT NULL, description TEXT NOT NULL,
		created_at INTEGER NOT NULL, embedding_provider TEXT NOT NULL, embedding_model TEXT NOT NULL,
		embedding_digest TEXT NOT NULL, dimensions INTEGER NOT NULL, input_format TEXT NOT NULL);
		CREATE TABLE entries (id INTEGER PRIMARY KEY, term TEXT NOT NULL, content TEXT NOT NULL,
		media_type TEXT NOT NULL, media_name TEXT NOT NULL, media_mime TEXT NOT NULL, media BLOB,
		embedding BLOB NOT NULL)`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO metadata VALUES
		(1, 1, 'legacy', 'Legacy', '', 1, 'ollama', 'm', 'd', 3, ?)`, InputFormatV1); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO entries
		(term, content, media_type, media_name, media_mime, media, embedding)
		VALUES ('pic', '', 'image', 'p.png', 'image/png', ?, ?)`, media, emb); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	infos, warnings, err := List(dir)
	if err != nil || len(warnings) != 0 || len(infos) != 1 {
		t.Fatalf("List = %+v warnings=%v err=%v", infos, warnings, err)
	}
	d, err := Get(dir, "legacy.db")
	if err != nil {
		t.Fatal(err)
	}
	if d.Meta.InputFormat != InputFormatV1 || len(d.Entries) != 1 ||
		d.Entries[0].InputMode != "combined" || len(d.Entries[0].Media) != 1 ||
		d.Entries[0].Media[0].Type != "image" || d.Entries[0].Media[0].Size != int64(len(media)) {
		t.Fatalf("legacy detail = %+v", d)
	}
	m, err := MediaAt(dir, "legacy.db", d.Entries[0].ID, "image")
	if err != nil || string(m.Data) != string(media) || m.MIME != "image/png" {
		t.Fatalf("legacy media = %+v err=%v", m, err)
	}
}

func TestGetReadsV2BaseWithFallbackTimestamps(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "v2.db")
	db, err := sql.Open("sqlite", fileURI(path, "rwc"))
	if err != nil {
		t.Fatal(err)
	}
	emb := packVector([]float64{1, 2, 3})
	if _, err := db.Exec(`CREATE TABLE metadata (key INTEGER PRIMARY KEY CHECK (key = 1),
		schema_version INTEGER NOT NULL, id TEXT NOT NULL, name TEXT NOT NULL, description TEXT NOT NULL,
		created_at INTEGER NOT NULL, embedding_provider TEXT NOT NULL, embedding_model TEXT NOT NULL,
		embedding_digest TEXT NOT NULL, dimensions INTEGER NOT NULL, input_format TEXT NOT NULL);
		CREATE TABLE entries (id INTEGER PRIMARY KEY, term TEXT NOT NULL, content TEXT NOT NULL,
		input_mode TEXT NOT NULL, image_name TEXT NOT NULL, image_mime TEXT NOT NULL, image BLOB,
		audio_name TEXT NOT NULL, audio_mime TEXT NOT NULL, audio BLOB, embedding BLOB NOT NULL)`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO metadata VALUES
		(1, 2, 'legacy-v2', 'V2', '', 10, 'ollama', 'm', 'd', 3, ?)`, InputFormatV2); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO entries
		(term, content, input_mode, image_name, image_mime, image,
		 audio_name, audio_mime, audio, embedding)
		VALUES ('pic', '', 'media', 'p.png', 'image/png', NULL, '', '', NULL, ?)`, emb); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	d, err := Get(dir, "v2.db")
	if err != nil {
		t.Fatal(err)
	}
	if d.Meta.UpdatedAt != 10 || d.Entries[0].CreatedAt != 10 || d.Entries[0].UpdatedAt != 10 {
		t.Fatalf("v2 timestamps = %+v", d)
	}
}

func TestGetGuardsFilename(t *testing.T) {
	dir := t.TempDir()
	_, filename, err := Create(context.Background(), dir, testMeta(), testEntries(1))
	if err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{"", ".", "..", "../x.db", "a/b.db", `a\b.db`, "x.txt", ".hidden.db", filename + ".x"} {
		if _, err := Get(dir, bad); err == nil {
			t.Fatalf("Get(%q) should fail", bad)
		}
	}
	if _, err := Get(dir, "missing.db"); err == nil {
		t.Fatal("missing db should fail")
	}
	target := filepath.Join(dir, filename)
	link := filepath.Join(dir, "link.db")
	if err := os.Symlink(target, link); err == nil {
		if _, err := Get(dir, "link.db"); err == nil {
			t.Fatal("symlinked db should be rejected")
		}
	}
	if _, err := Get(dir, filename); err != nil {
		t.Fatalf("Get(%q): %v", filename, err)
	}
}

func TestMediaStoredAndListed(t *testing.T) {
	dir := t.TempDir()
	imagePayload := []byte{0x89, 0x50, 0x4E, 0x47, 0xAA, 0xBB}
	audioPayload := []byte{'R', 'I', 'F', 'F', 1, 2, 3}
	entries := []Entry{{
		Term: "pic", Content: "", InputMode: "media",
		Media: []Media{
			{Type: "image", Name: "p.png", MIME: "image/png", Data: imagePayload},
			{Type: "audio", Name: "a.wav", MIME: "audio/wav", Data: audioPayload},
		},
		Embedding: []float64{1, 2, 3},
	}}
	_, filename, err := Create(context.Background(), dir, testMeta(), entries)
	if err != nil {
		t.Fatal(err)
	}
	d, err := Get(dir, filename)
	if err != nil {
		t.Fatal(err)
	}
	if d.Entries[0].InputMode != "media" || len(d.Entries[0].Media) != 2 ||
		d.Entries[0].Media[0].Type != "image" || d.Entries[0].Media[0].MIME != "image/png" ||
		d.Entries[0].Media[0].Size != int64(len(imagePayload)) || d.Entries[0].Media[0].Name != "p.png" ||
		d.Entries[0].Media[1].Type != "audio" || d.Entries[0].Media[1].MIME != "audio/wav" ||
		d.Entries[0].Media[1].Size != int64(len(audioPayload)) || d.Entries[0].Media[1].Name != "a.wav" {
		t.Fatalf("unexpected entry view: %+v", d.Entries[0])
	}
	db, err := sql.Open("sqlite", fileURI(filepath.Join(dir, filename), "ro"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var image, audio []byte
	if err := db.QueryRow(`SELECT image, audio FROM entries`).Scan(&image, &audio); err != nil {
		t.Fatal(err)
	}
	if string(image) != string(imagePayload) || string(audio) != string(audioPayload) {
		t.Fatalf("media payloads changed: image=%x audio=%x", image, audio)
	}
}
