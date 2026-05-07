package server

import (
	"encoding/json"
	"errors"
	"os"
	"sync"
	"time"
)

var allowedUninstallReasons = map[string]bool{
	"load_failed":            true,
	"missing_capabilities":   true,
	"too_slow":               true,
	"obsolete_or_outdated":   true,
}

type uninstallRecord struct {
	Name            string    `json:"name"`
	LastReason      string    `json:"last_reason,omitempty"`
	LastUninstallAt time.Time `json:"last_uninstall_at,omitempty"`
}

type uninstallHistoryFile struct {
	Models []uninstallRecord `json:"models"`
}

type uninstallHistoryStore struct {
	path   string
	mu     sync.Mutex
	byName map[string]uninstallRecord
}

func newUninstallHistoryStore(path string) *uninstallHistoryStore {
	return &uninstallHistoryStore{
		path:   path,
		byName: make(map[string]uninstallRecord),
	}
}

func (s *uninstallHistoryStore) Load() error {
	if s.path == "" {
		return nil
	}
	data, err := os.ReadFile(s.path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	var file uninstallHistoryFile
	if err := json.Unmarshal(data, &file); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.byName = make(map[string]uninstallRecord)
	for _, row := range file.Models {
		if row.Name == "" {
			continue
		}
		s.byName[row.Name] = row
	}
	return nil
}

func (s *uninstallHistoryStore) Get(name string) (uninstallRecord, bool) {
	if name == "" {
		return uninstallRecord{}, false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	row, ok := s.byName[name]
	return row, ok
}

func (s *uninstallHistoryStore) Record(name, reason string, when time.Time) error {
	if name == "" {
		return nil
	}
	if when.IsZero() {
		when = time.Now().UTC()
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.byName[name] = uninstallRecord{
		Name:            name,
		LastReason:      reason,
		LastUninstallAt: when,
	}
	return s.saveLocked()
}

func (s *uninstallHistoryStore) saveLocked() error {
	if s.path == "" {
		return nil
	}
	file := uninstallHistoryFile{
		Models: make([]uninstallRecord, 0, len(s.byName)),
	}
	for _, row := range s.byName {
		if row.Name == "" {
			continue
		}
		file.Models = append(file.Models, row)
	}
	data, err := json.MarshalIndent(file, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, s.path)
}
