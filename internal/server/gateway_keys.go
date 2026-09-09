package server

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"sort"
	"strings"
	"sync"
	"time"
)

// gatewayKeyPrefix marks keys issued for the OpenAI/Ollama-compatible gateway.
const gatewayKeyPrefix = "omgr-"

// GatewayKeyRecord is one Bearer API key for the gateway. Only the sha256
// hash is persisted; the plaintext is shown once at creation time.
type GatewayKeyRecord struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	KeyHash   string    `json:"key_hash"`
	Prefix    string    `json:"prefix"`
	CreatedAt time.Time `json:"created_at"`
}

// gatewayKeyPublic is the masked view returned by the list endpoint.
type gatewayKeyPublic struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	Prefix    string    `json:"prefix"`
	CreatedAt time.Time `json:"created_at"`
}

type gatewayKeysFile struct {
	Keys map[string]GatewayKeyRecord `json:"keys"`
}

type gatewayKeysStore struct {
	path string
	mu   sync.RWMutex
	keys map[string]GatewayKeyRecord
}

func newGatewayKeysStore(path string) *gatewayKeysStore {
	return &gatewayKeysStore{
		path: path,
		keys: make(map[string]GatewayKeyRecord),
	}
}

func (s *gatewayKeysStore) Load() error {
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
	var file gatewayKeysFile
	if err := json.Unmarshal(data, &file); err != nil {
		if q := quarantineCorrupt(s.path); q != "" {
			return fmt.Errorf("gateway-keys: corrupt %s quarantined to %s: %w", s.path, q, err)
		}
		return fmt.Errorf("gateway-keys: corrupt %s (quarantine failed): %w", s.path, err)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if file.Keys != nil {
		s.keys = file.Keys
	} else {
		s.keys = make(map[string]GatewayKeyRecord)
	}
	return nil
}

func (s *gatewayKeysStore) save() error {
	if s.path == "" {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	file := gatewayKeysFile{Keys: make(map[string]GatewayKeyRecord, len(s.keys))}
	for k, v := range s.keys {
		file.Keys[k] = v
	}
	return writeJSONFileAtomic(s.path, file, 0o600)
}

// hashGatewayKey returns the hex sha256 of a plaintext key.
func hashGatewayKey(key string) string {
	sum := sha256.Sum256([]byte(key))
	return hex.EncodeToString(sum[:])
}

func newGatewayKeyID() (string, error) {
	buf := make([]byte, 8)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return "gk_" + hex.EncodeToString(buf), nil
}

func newGatewayKeySecret() (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return gatewayKeyPrefix + hex.EncodeToString(buf), nil
}

// Create issues a new key. It returns the record plus the plaintext secret,
// which is never stored and must be shown to the user immediately.
func (s *gatewayKeysStore) Create(name string) (GatewayKeyRecord, string, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return GatewayKeyRecord{}, "", errors.New("key name is required")
	}
	id, err := newGatewayKeyID()
	if err != nil {
		return GatewayKeyRecord{}, "", err
	}
	secret, err := newGatewayKeySecret()
	if err != nil {
		return GatewayKeyRecord{}, "", err
	}
	rec := GatewayKeyRecord{
		ID:        id,
		Name:      name,
		KeyHash:   hashGatewayKey(secret),
		Prefix:    secret[:12] + "…",
		CreatedAt: time.Now().UTC(),
	}
	s.mu.Lock()
	s.keys[id] = rec
	s.mu.Unlock()
	if err := s.save(); err != nil {
		s.mu.Lock()
		delete(s.keys, id)
		s.mu.Unlock()
		return GatewayKeyRecord{}, "", err
	}
	return rec, secret, nil
}

// Verify reports whether plaintext matches any stored key (constant time).
func (s *gatewayKeysStore) Verify(plaintext string) bool {
	plaintext = strings.TrimSpace(plaintext)
	if plaintext == "" {
		return false
	}
	sum := sha256.Sum256([]byte(plaintext))
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, rec := range s.keys {
		raw, err := hex.DecodeString(rec.KeyHash)
		if err != nil || len(raw) != sha256.Size {
			continue
		}
		if subtle.ConstantTimeCompare(raw, sum[:]) == 1 {
			return true
		}
	}
	return false
}

// List returns the masked key records, newest first.
func (s *gatewayKeysStore) List() []gatewayKeyPublic {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]gatewayKeyPublic, 0, len(s.keys))
	for _, rec := range s.keys {
		out = append(out, gatewayKeyPublic{
			ID:        rec.ID,
			Name:      rec.Name,
			Prefix:    rec.Prefix,
			CreatedAt: rec.CreatedAt,
		})
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].CreatedAt.After(out[j].CreatedAt)
	})
	return out
}

// Delete revokes a key by id.
func (s *gatewayKeysStore) Delete(id string) error {
	s.mu.Lock()
	if _, ok := s.keys[id]; !ok {
		s.mu.Unlock()
		return errors.New("key not found")
	}
	delete(s.keys, id)
	s.mu.Unlock()
	return s.save()
}

// ---------- HTTP handlers (management via the main UI, requireAuth) ----------

func (s *Server) handleListGatewayKeys(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"keys": s.gatewayKeys.List()})
}

func (s *Server) handleCreateGatewayKey(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Name string `json:"name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, errors.New("invalid body"))
		return
	}
	rec, secret, err := s.gatewayKeys.Create(body.Name)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	// The plaintext secret is returned exactly once; it is never stored.
	writeJSON(w, http.StatusOK, map[string]any{
		"id":         rec.ID,
		"name":       rec.Name,
		"prefix":     rec.Prefix,
		"created_at": rec.CreatedAt,
		"key":        secret,
	})
}

func (s *Server) handleDeleteGatewayKey(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		writeError(w, http.StatusBadRequest, errors.New("missing key id"))
		return
	}
	if err := s.gatewayKeys.Delete(id); err != nil {
		writeError(w, http.StatusNotFound, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}
