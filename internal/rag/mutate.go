package rag

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strings"
	"time"
)

type queryer interface {
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
}

var ErrConflict = errors.New("rag base changed since it was read")

type EntrySnapshot struct {
	ID       int64
	Entry    Entry
	Revision string
}

func MetaRevision(m Meta) string {
	h := sha256.New()
	_ = json.NewEncoder(h).Encode(m)
	return hex.EncodeToString(h.Sum(nil))
}

func entryRevision(m Meta, id int64, e Entry) string {
	h := sha256.New()
	_ = json.NewEncoder(h).Encode(struct {
		MetaID string `json:"meta_id"`
		Model  string `json:"model"`
		Digest string `json:"digest"`
		Dims   int    `json:"dimensions"`
		ID     int64  `json:"id"`
		Entry  Entry  `json:"entry"`
	}{m.ID, m.EmbeddingModel, m.EmbeddingDigest, m.Dimensions, id, e})
	return hex.EncodeToString(h.Sum(nil))
}

func readEntry(ctx context.Context, q queryer, m Meta, version int, id int64) (EntrySnapshot, error) {
	var rowID int64
	var e Entry
	e.InputMode = "combined"
	var blob []byte
	switch version {
	case 1:
		var mediaType, mediaName, mediaMIME string
		var media []byte
		err := q.QueryRowContext(ctx, `SELECT id, term, content, media_type, media_name, media_mime, media, embedding FROM entries WHERE id = ?`, id).
			Scan(&rowID, &e.Term, &e.Content, &mediaType, &mediaName, &mediaMIME, &media, &blob)
		if err != nil {
			return EntrySnapshot{}, err
		}
		if mediaType != "" && mediaType != "text" && len(media) > 0 {
			e.Media = []Media{{Type: mediaType, Name: mediaName, MIME: mediaMIME, Data: media}}
		}
		e.CreatedAt = m.CreatedAt
		e.UpdatedAt = m.CreatedAt
	case 2:
		var imageName, imageMIME, audioName, audioMIME string
		var image, audio []byte
		err := q.QueryRowContext(ctx, `SELECT id, term, content, input_mode, image_name, image_mime, image, audio_name, audio_mime, audio, embedding FROM entries WHERE id = ?`, id).
			Scan(&rowID, &e.Term, &e.Content, &e.InputMode,
				&imageName, &imageMIME, &image, &audioName, &audioMIME, &audio, &blob)
		if err != nil {
			return EntrySnapshot{}, err
		}
		e.CreatedAt = m.CreatedAt
		e.UpdatedAt = m.CreatedAt
		if len(image) > 0 {
			e.Media = append(e.Media, Media{Type: "image", Name: imageName, MIME: imageMIME, Data: image})
		}
		if len(audio) > 0 {
			e.Media = append(e.Media, Media{Type: "audio", Name: audioName, MIME: audioMIME, Data: audio})
		}
	case 3, SchemaVersion:
		var imageName, imageMIME, audioName, audioMIME, aliasRaw string
		var image, audio []byte
		if version == 3 {
			aliasRaw = "[]"
			err := q.QueryRowContext(ctx, `SELECT id, term, content, input_mode, created_at, updated_at, image_name, image_mime, image, audio_name, audio_mime, audio, embedding FROM entries WHERE id = ?`, id).
				Scan(&rowID, &e.Term, &e.Content, &e.InputMode, &e.CreatedAt, &e.UpdatedAt,
					&imageName, &imageMIME, &image, &audioName, &audioMIME, &audio, &blob)
			if err != nil {
				return EntrySnapshot{}, err
			}
		} else {
			err := q.QueryRowContext(ctx, `SELECT id, term, content, input_mode, created_at, updated_at, image_name, image_mime, image, audio_name, audio_mime, audio, aliases, embedding FROM entries WHERE id = ?`, id).
				Scan(&rowID, &e.Term, &e.Content, &e.InputMode, &e.CreatedAt, &e.UpdatedAt,
					&imageName, &imageMIME, &image, &audioName, &audioMIME, &audio, &aliasRaw, &blob)
			if err != nil {
				return EntrySnapshot{}, err
			}
		}
		var aerr error
		e.Aliases, aerr = decodeAliases(aliasRaw)
		if aerr != nil {
			return EntrySnapshot{}, fmt.Errorf("entry %d: %w", id, aerr)
		}
		if len(image) > 0 {
			e.Media = append(e.Media, Media{Type: "image", Name: imageName, MIME: imageMIME, Data: image})
		}
		if len(audio) > 0 {
			e.Media = append(e.Media, Media{Type: "audio", Name: audioName, MIME: audioMIME, Data: audio})
		}
	default:
		return EntrySnapshot{}, fmt.Errorf("unsupported schema version %d", version)
	}
	if e.CreatedAt == 0 {
		e.CreatedAt = m.CreatedAt
	}
	if e.UpdatedAt == 0 {
		e.UpdatedAt = e.CreatedAt
	}
	if e.Aliases == nil {
		e.Aliases = []string{}
	}
	if err := checkVectorBlob(blob, m.Dimensions); err != nil {
		return EntrySnapshot{}, fmt.Errorf("entry %d: %w", rowID, err)
	}
	vec, err := decodeVector(blob, m.Dimensions)
	if err != nil {
		return EntrySnapshot{}, fmt.Errorf("entry %d: %w", rowID, err)
	}
	e.Embedding = vec
	return EntrySnapshot{ID: rowID, Entry: e, Revision: entryRevision(m, rowID, e)}, nil
}

func ReadEntry(ctx context.Context, dir, filename string, id int64) (Meta, EntrySnapshot, error) {
	if id <= 0 {
		return Meta{}, EntrySnapshot{}, errors.New("invalid entry id")
	}
	path, err := Path(dir, filename)
	if err != nil {
		return Meta{}, EntrySnapshot{}, err
	}
	db, err := openRO(path)
	if err != nil {
		return Meta{}, EntrySnapshot{}, err
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	m, version, err := readMetaContext(ctx, db)
	if err != nil {
		return Meta{}, EntrySnapshot{}, fmt.Errorf("not a RAG base (%w)", err)
	}
	if err := validateContext(ctx, db, m, version); err != nil {
		return Meta{}, EntrySnapshot{}, err
	}
	snap, err := readEntry(ctx, db, m, version, id)
	if err != nil {
		return Meta{}, EntrySnapshot{}, err
	}
	return m, snap, nil
}

func beginMutation(ctx context.Context, dir, filename string, expected Meta) (*sql.DB, *sql.Tx, Meta, int, error) {
	path, err := Path(dir, filename)
	if err != nil {
		return nil, nil, Meta{}, 0, err
	}
	db, err := sql.Open("sqlite", fileURI(path, "rw"))
	if err != nil {
		return nil, nil, Meta{}, 0, err
	}
	db.SetMaxOpenConns(1)
	fail := func(err error) (*sql.DB, *sql.Tx, Meta, int, error) {
		_ = db.Close()
		return nil, nil, Meta{}, 0, err
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fail(err)
	}
	m, version, err := readMetaContext(ctx, tx)
	if err != nil {
		_ = tx.Rollback()
		return fail(fmt.Errorf("not a RAG base (%w)", err))
	}
	if err := validateContext(ctx, tx, m, version); err != nil {
		_ = tx.Rollback()
		return fail(err)
	}
	if err := metaMismatch(expected, m); err != nil {
		_ = tx.Rollback()
		return fail(fmt.Errorf("%w: %v", ErrConflict, err))
	}
	if err := ctx.Err(); err != nil {
		_ = tx.Rollback()
		return fail(err)
	}
	return db, tx, m, version, nil
}

func validWriteEntry(m Meta, version int, e *Entry) error {
	mode := e.InputMode
	if mode == "" {
		mode = "combined"
	}
	if mode != "combined" && mode != "media" {
		return fmt.Errorf("unsupported input mode %q", mode)
	}
	if version == 1 && mode != "combined" {
		return errors.New("schema v1 entries do not support the media input mode")
	}
	if mode == "media" && len(e.Media) == 0 {
		return errors.New("media input mode requires an attachment")
	}
	if !validVector(e.Embedding, m.Dimensions) {
		return fmt.Errorf("embedding must have %d finite dimensions", m.Dimensions)
	}
	norm := vectorNorm(e.Embedding)
	if norm == 0 || math.IsNaN(norm) || math.IsInf(norm, 0) {
		return errors.New("embedding has no usable norm")
	}
	seen := map[string]bool{}
	for _, mm := range e.Media {
		if mm.Type != "image" && mm.Type != "audio" {
			return fmt.Errorf("unsupported media type %q", mm.Type)
		}
		if seen[mm.Type] {
			return fmt.Errorf("duplicate %s media", mm.Type)
		}
		seen[mm.Type] = true
		if len(mm.Data) == 0 {
			return fmt.Errorf("empty %s media", mm.Type)
		}
	}
	normAliases, err := NormalizeAliases(e.Aliases)
	if err != nil {
		return err
	}
	e.Aliases = normAliases
	return nil
}

func migrateToV4(ctx context.Context, tx *sql.Tx, m *Meta, version int) (int, error) {
	if version >= SchemaVersion {
		return version, nil
	}
	if version < 1 || version > 3 {
		return version, fmt.Errorf("unsupported schema version %d", version)
	}
	run := func(q string, args ...any) error {
		_, err := tx.ExecContext(ctx, q, args...)
		return err
	}
	if version == 1 {
		for _, q := range []string{
			`ALTER TABLE entries ADD COLUMN input_mode TEXT NOT NULL DEFAULT 'combined'`,
			`ALTER TABLE entries ADD COLUMN image_name TEXT NOT NULL DEFAULT ''`,
			`ALTER TABLE entries ADD COLUMN image_mime TEXT NOT NULL DEFAULT ''`,
			`ALTER TABLE entries ADD COLUMN image BLOB`,
			`ALTER TABLE entries ADD COLUMN audio_name TEXT NOT NULL DEFAULT ''`,
			`ALTER TABLE entries ADD COLUMN audio_mime TEXT NOT NULL DEFAULT ''`,
			`ALTER TABLE entries ADD COLUMN audio BLOB`,
			`UPDATE entries SET image_name = media_name, image_mime = media_mime, image = media WHERE media_type = 'image'`,
			`UPDATE entries SET audio_name = media_name, audio_mime = media_mime, audio = media WHERE media_type = 'audio'`,
		} {
			if err := run(q); err != nil {
				return version, err
			}
		}
	}
	if version < 3 {
		for _, q := range []string{
			`ALTER TABLE entries ADD COLUMN created_at INTEGER NOT NULL DEFAULT 0`,
			`ALTER TABLE entries ADD COLUMN updated_at INTEGER NOT NULL DEFAULT 0`,
		} {
			if err := run(q); err != nil {
				return version, err
			}
		}
		if err := run(`UPDATE entries SET created_at = ?, updated_at = ?`, m.CreatedAt, m.CreatedAt); err != nil {
			return version, err
		}
		if err := run(`ALTER TABLE metadata ADD COLUMN updated_at INTEGER NOT NULL DEFAULT 0`); err != nil {
			return version, err
		}
		if err := run(`UPDATE metadata SET updated_at = created_at WHERE key = 1`); err != nil {
			return version, err
		}
	}
	if err := run(`ALTER TABLE entries ADD COLUMN aliases TEXT NOT NULL DEFAULT '[]'`); err != nil {
		return version, err
	}
	if err := run(`UPDATE metadata SET schema_version = ?, input_format = ? WHERE key = 1`, SchemaVersion, InputFormat); err != nil {
		return version, err
	}
	m.InputFormat = InputFormat
	m.UpdatedAt = time.Now().Unix()
	return SchemaVersion, nil
}

func entryMedia(e Entry) (image, audio Media) {
	for _, mm := range e.Media {
		if mm.Type == "image" {
			image = mm
		} else if mm.Type == "audio" {
			audio = mm
		}
	}
	return image, audio
}

func touchMeta(ctx context.Context, tx *sql.Tx, version int, now int64) error {
	if version < 3 {
		return nil
	}
	_, err := tx.ExecContext(ctx, `UPDATE metadata SET updated_at = ? WHERE key = 1`, now)
	return err
}

func CreateEntry(ctx context.Context, dir, filename string, expected Meta, e Entry) (EntrySnapshot, error) {
	db, tx, m, version, err := beginMutation(ctx, dir, filename, expected)
	if err != nil {
		return EntrySnapshot{}, err
	}
	defer db.Close()
	fail := func(err error) (EntrySnapshot, error) {
		_ = tx.Rollback()
		return EntrySnapshot{}, err
	}
	if version == 1 && len(e.Media) > 0 {
		return fail(errors.New("schema v1 entries cannot carry media through this API"))
	}
	if err := validWriteEntry(m, version, &e); err != nil {
		return fail(err)
	}
	if version < SchemaVersion && len(e.Aliases) > 0 {
		version, err = migrateToV4(ctx, tx, &m, version)
		if err != nil {
			return fail(fmt.Errorf("could not migrate base for aliases: %w", err))
		}
	}
	mode := e.InputMode
	if mode == "" {
		mode = "combined"
	}
	now := time.Now().Unix()
	if e.CreatedAt == 0 {
		e.CreatedAt = now
	}
	if e.UpdatedAt == 0 {
		e.UpdatedAt = e.CreatedAt
	}
	image, audio := entryMedia(e)
	var res sql.Result
	switch version {
	case 1:
		res, err = tx.ExecContext(ctx, `INSERT INTO entries (term, content, media_type, media_name, media_mime, media, embedding) VALUES (?, ?, 'text', '', '', NULL, ?)`,
			e.Term, e.Content, packVector(e.Embedding))
	case 2:
		res, err = tx.ExecContext(ctx, `INSERT INTO entries (term, content, input_mode, image_name, image_mime, image, audio_name, audio_mime, audio, embedding) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			e.Term, e.Content, mode,
			image.Name, image.MIME, image.Data, audio.Name, audio.MIME, audio.Data, packVector(e.Embedding))
	case 3:
		res, err = tx.ExecContext(ctx, `INSERT INTO entries (term, content, input_mode, created_at, updated_at, image_name, image_mime, image, audio_name, audio_mime, audio, embedding) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			e.Term, e.Content, mode, e.CreatedAt, e.UpdatedAt,
			image.Name, image.MIME, image.Data, audio.Name, audio.MIME, audio.Data, packVector(e.Embedding))
	case SchemaVersion:
		legacyV1 := false
		var probe string
		if perr := tx.QueryRowContext(ctx, `SELECT media_type FROM entries LIMIT 1`).Scan(&probe); perr == nil ||
			errors.Is(perr, sql.ErrNoRows) {
			legacyV1 = true
		}
		if legacyV1 {
			res, err = tx.ExecContext(ctx, `INSERT INTO entries (term, content, input_mode, created_at, updated_at, image_name, image_mime, image, audio_name, audio_mime, audio, aliases, embedding, media_type, media_name, media_mime, media) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 'text', '', '', NULL)`,
				e.Term, e.Content, mode, e.CreatedAt, e.UpdatedAt,
				image.Name, image.MIME, image.Data, audio.Name, audio.MIME, audio.Data, encodeAliases(e.Aliases), packVector(e.Embedding))
		} else {
			res, err = tx.ExecContext(ctx, `INSERT INTO entries (term, content, input_mode, created_at, updated_at, image_name, image_mime, image, audio_name, audio_mime, audio, aliases, embedding) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
				e.Term, e.Content, mode, e.CreatedAt, e.UpdatedAt,
				image.Name, image.MIME, image.Data, audio.Name, audio.MIME, audio.Data, encodeAliases(e.Aliases), packVector(e.Embedding))
		}
	default:
		return fail(fmt.Errorf("unsupported schema version %d", version))
	}
	if err != nil {
		return fail(err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return fail(err)
	}
	if err := touchMeta(ctx, tx, version, now); err != nil {
		return fail(err)
	}
	snap, err := readEntry(ctx, tx, m, version, id)
	if err != nil {
		return fail(err)
	}
	if err := tx.Commit(); err != nil {
		return EntrySnapshot{}, err
	}
	return snap, nil
}

func UpdateEntry(ctx context.Context, dir, filename string, expected Meta, id int64, revision string, e Entry) (EntrySnapshot, error) {
	db, tx, m, version, err := beginMutation(ctx, dir, filename, expected)
	if err != nil {
		return EntrySnapshot{}, err
	}
	defer db.Close()
	fail := func(err error) (EntrySnapshot, error) {
		_ = tx.Rollback()
		return EntrySnapshot{}, err
	}
	snap, err := readEntry(ctx, tx, m, version, id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return fail(fmt.Errorf("%w: entry %d is gone", ErrConflict, id))
		}
		return fail(err)
	}
	if snap.Revision != revision {
		return fail(fmt.Errorf("%w: entry %d changed since it was read", ErrConflict, id))
	}
	mode := e.InputMode
	if mode == "" {
		mode = snap.Entry.InputMode
	}
	if mode == "" {
		mode = "combined"
	}
	e.InputMode = mode
	e.Media = snap.Entry.Media
	if err := validWriteEntry(m, version, &e); err != nil {
		return fail(err)
	}
	if mode == "media" && len(snap.Entry.Media) == 0 {
		return fail(errors.New("media input mode requires an existing attachment"))
	}
	if version < SchemaVersion && len(e.Aliases) > 0 {
		version, err = migrateToV4(ctx, tx, &m, version)
		if err != nil {
			return fail(fmt.Errorf("could not migrate base for aliases: %w", err))
		}
	}
	now := time.Now().Unix()
	switch version {
	case 1:
		_, err = tx.ExecContext(ctx, `UPDATE entries SET term = ?, content = ?, embedding = ? WHERE id = ?`,
			e.Term, e.Content, packVector(e.Embedding), id)
	case 2:
		_, err = tx.ExecContext(ctx, `UPDATE entries SET term = ?, content = ?, input_mode = ?, embedding = ? WHERE id = ?`,
			e.Term, e.Content, mode, packVector(e.Embedding), id)
	case 3:
		_, err = tx.ExecContext(ctx, `UPDATE entries SET term = ?, content = ?, input_mode = ?, updated_at = ?, embedding = ? WHERE id = ?`,
			e.Term, e.Content, mode, now, packVector(e.Embedding), id)
	case SchemaVersion:
		_, err = tx.ExecContext(ctx, `UPDATE entries SET term = ?, content = ?, input_mode = ?, aliases = ?, updated_at = ?, embedding = ? WHERE id = ?`,
			e.Term, e.Content, mode, encodeAliases(e.Aliases), now, packVector(e.Embedding), id)
	default:
		return fail(fmt.Errorf("unsupported schema version %d", version))
	}
	if err != nil {
		return fail(err)
	}
	if err := touchMeta(ctx, tx, version, now); err != nil {
		return fail(err)
	}
	fresh, err := readEntry(ctx, tx, m, version, id)
	if err != nil {
		return fail(err)
	}
	if err := tx.Commit(); err != nil {
		return EntrySnapshot{}, err
	}
	return fresh, nil
}

func DeleteEntry(ctx context.Context, dir, filename string, expected Meta, id int64, revision string) error {
	db, tx, m, version, err := beginMutation(ctx, dir, filename, expected)
	if err != nil {
		return err
	}
	defer db.Close()
	fail := func(err error) error {
		_ = tx.Rollback()
		return err
	}
	snap, err := readEntry(ctx, tx, m, version, id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return fail(fmt.Errorf("%w: entry %d is gone", ErrConflict, id))
		}
		return fail(err)
	}
	if snap.Revision != revision {
		return fail(fmt.Errorf("%w: entry %d changed since it was read", ErrConflict, id))
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM entries WHERE id = ?`, id); err != nil {
		return fail(err)
	}
	if err := touchMeta(ctx, tx, version, time.Now().Unix()); err != nil {
		return fail(err)
	}
	return tx.Commit()
}

func UpdateMetadata(ctx context.Context, dir, filename string, expectedRevision string, name, description *string) (Meta, error) {
	path, err := Path(dir, filename)
	if err != nil {
		return Meta{}, err
	}
	db, err := sql.Open("sqlite", fileURI(path, "rw"))
	if err != nil {
		return Meta{}, err
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return Meta{}, err
	}
	fail := func(err error) (Meta, error) {
		_ = tx.Rollback()
		return Meta{}, err
	}
	m, version, err := readMetaContext(ctx, tx)
	if err != nil {
		return fail(fmt.Errorf("not a RAG base (%w)", err))
	}
	if err := validateContext(ctx, tx, m, version); err != nil {
		return fail(err)
	}
	if MetaRevision(m) != expectedRevision {
		return fail(fmt.Errorf("%w: base metadata changed since it was read", ErrConflict))
	}
	newName := m.Name
	if name != nil {
		if strings.TrimSpace(*name) == "" {
			return fail(errors.New("name must not be empty"))
		}
		newName = *name
	}
	newDesc := m.Description
	if description != nil {
		newDesc = *description
	}
	now := time.Now().Unix()
	if version >= 3 {
		_, err = tx.ExecContext(ctx, `UPDATE metadata SET name = ?, description = ?, updated_at = ? WHERE key = 1`, newName, newDesc, now)
	} else {
		_, err = tx.ExecContext(ctx, `UPDATE metadata SET name = ?, description = ? WHERE key = 1`, newName, newDesc)
	}
	if err != nil {
		return fail(err)
	}
	fresh, _, err := readMetaContext(ctx, tx)
	if err != nil {
		return fail(err)
	}
	if err := tx.Commit(); err != nil {
		return Meta{}, err
	}
	return fresh, nil
}
