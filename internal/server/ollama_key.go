package server

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

// handleOllamaKey returns the local Ollama ed25519 public key that must be
// registered on HuggingFace to pull gated or private GGUF repos. Ollama
// authenticates to the HF registry by signing the challenge with this key
// (see Ollama's server/auth.go); the HF_TOKEN environment variable is not
// used for pulls, so this key is the only way to unlock gated models.
//
// It only reads the key from the machine running this manager. When Ollama
// runs on another host, the key lives there instead.
func (s *Server) handleOllamaKey(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	home, err := os.UserHomeDir()
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]any{"found": false, "path": "", "public_key": ""})
		return
	}
	path := filepath.Join(home, ".ollama", "id_ed25519.pub")
	data, err := os.ReadFile(path)
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]any{"found": false, "path": path, "public_key": ""})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"found":      true,
		"path":       path,
		"public_key": strings.TrimSpace(string(data)),
	})
}
