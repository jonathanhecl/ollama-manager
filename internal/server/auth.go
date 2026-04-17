package server

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"net/http"
	"strings"
	"time"
)

const (
	cookieName    = "ollama_manager_session"
	sessionMaxAge = 7 * 24 * time.Hour
)

// signSession produces a token "<expiryUnix>.<base64-hmac>".
func signSession(secret string, expiresAt time.Time) string {
	exp := expiresAt.Unix()
	expBytes := make([]byte, 8)
	binary.BigEndian.PutUint64(expBytes, uint64(exp))
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(expBytes)
	sig := base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
	return base64.RawURLEncoding.EncodeToString(expBytes) + "." + sig
}

// verifySession returns nil if the token is valid and not expired.
func verifySession(secret, token string) error {
	parts := strings.SplitN(token, ".", 2)
	if len(parts) != 2 {
		return errors.New("malformed session token")
	}
	expBytes, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil || len(expBytes) != 8 {
		return errors.New("malformed session expiry")
	}
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(expBytes)
	want := base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
	if !hmac.Equal([]byte(want), []byte(parts[1])) {
		return errors.New("invalid session signature")
	}
	exp := int64(binary.BigEndian.Uint64(expBytes))
	if time.Now().Unix() > exp {
		return errors.New("session expired")
	}
	return nil
}

// setSessionCookie issues a fresh session cookie. Caller is responsible
// for holding any required cfg lock.
func (s *Server) setSessionCookie(w http.ResponseWriter) {
	expires := time.Now().Add(sessionMaxAge)
	http.SetCookie(w, &http.Cookie{
		Name:     cookieName,
		Value:    signSession(s.cfg.SessionSecret, expires),
		Path:     "/",
		Expires:  expires,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
	})
}

func (s *Server) clearSessionCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name:     cookieName,
		Value:    "",
		Path:     "/",
		Expires:  time.Unix(0, 0),
		MaxAge:   -1,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
	})
}

// isAuthenticated returns true when:
//   - no password is configured, OR
//   - the request carries a valid session cookie.
func (s *Server) isAuthenticated(r *http.Request) bool {
	s.cfgMu.RLock()
	hasPwd := s.cfg.HasPassword()
	secret := s.cfg.SessionSecret
	s.cfgMu.RUnlock()

	if !hasPwd {
		return true
	}
	c, err := r.Cookie(cookieName)
	if err != nil || c.Value == "" {
		return false
	}
	return verifySession(secret, c.Value) == nil
}
