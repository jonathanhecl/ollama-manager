package server

import (
	"encoding/json"
	"os"
	"path/filepath"
	"time"
)

// writeJSONFileAtomic marshals v as indented JSON and persists it atomically:
// write tmp + fsync(file) + rename + fsync(parent dir, best effort).
// Callers must hold whatever lock serializes concurrent saves themselves —
// two concurrent calls with the same path share the same ".tmp" file, so the
// whole snapshot+write must happen under the store's write lock (last writer
// wins, but the file on disk is always complete and valid).
func writeJSONFileAtomic(path string, v any, perm os.FileMode) error {
	if path == "" {
		return nil
	}
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	tmp := path + ".tmp"
	f, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, perm)
	if err != nil {
		return err
	}
	if _, err := f.Write(data); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		return err
	}
	// Best effort: fsync the parent dir so the rename itself survives a crash.
	// os.Open on a directory fails on Windows — ignored on purpose.
	if dir, err := os.Open(filepath.Dir(path)); err == nil {
		_ = dir.Sync()
		_ = dir.Close()
	}
	return nil
}

// quarantineCorrupt renames a corrupt JSON file aside so a failed Load never
// leads to silent data loss when the store later saves fresh state over it.
// It returns the quarantine path, or "" if the rename failed.
func quarantineCorrupt(path string) string {
	if path == "" {
		return ""
	}
	q := path + ".corrupt-" + time.Now().Format("20060102-150405")
	if err := os.Rename(path, q); err != nil {
		return ""
	}
	return q
}
