package server

import (
	"context"
	"log"
	"net"
	"net/http"
	"time"
)

// ListenAndServeGateway serves the OpenAI/Ollama-compatible gateway mux on
// its own managed listener until ctx is cancelled. It is a no-op when the
// gateway is disabled. Unlike the old one-shot version, the listener state
// is tracked (see GatewayStatus) and bind failures are returned as errors
// without killing the caller — main.go logs them and keeps the main server
// up.
func (s *Server) ListenAndServeGateway(ctx context.Context) error {
	if err := s.StartGateway(); err != nil {
		return err
	}
	<-ctx.Done()
	s.StopGateway()
	return nil
}

// StartGateway reconciles the managed gateway listener with the current
// config: starts it when enabled but not listening, restarts it when the
// bind address changed, stops it when disabled. Safe for concurrent use and
// idempotent. It binds with net.Listen first so "address already in use"
// and similar errors surface immediately instead of hiding inside Serve.
func (s *Server) StartGateway() error {
	return s.SyncGateway()
}

// SyncGateway applies the current gateway config to the managed listener.
// See StartGateway.
func (s *Server) SyncGateway() error {
	s.cfgMu.RLock()
	enabled := s.cfg.Gateway.Enabled
	addr := s.cfg.Gateway.GatewayBindAddress()
	s.cfgMu.RUnlock()

	s.gwMu.Lock()
	defer s.gwMu.Unlock()

	if !enabled {
		s.stopGatewayLocked()
		return nil
	}
	if s.gwListening && s.gwAddr == addr && s.gwSrv != nil {
		return nil // already serving the desired address
	}
	s.stopGatewayLocked()

	ln, err := net.Listen("tcp", addr)
	if err != nil {
		log.Printf("gateway: cannot listen on http://%s: %v", addr, err)
		return err
	}
	srv := &http.Server{
		Handler: logging(s.gatewayRoutes()),
	}
	s.gwSrv = srv
	s.gwAddr = addr
	s.gwListening = true

	go func(srv *http.Server, ln net.Listener, addr string) {
		log.Printf("gateway: listening on http://%s", addr)
		if err := srv.Serve(ln); err != nil && err != http.ErrServerClosed {
			log.Printf("gateway: serve error on %s: %v", addr, err)
		}
		s.gwMu.Lock()
		if s.gwSrv == srv {
			s.gwSrv = nil
			s.gwListening = false
		}
		s.gwMu.Unlock()
	}(srv, ln, addr)
	return nil
}

// StopGateway shuts the managed gateway listener down, if running.
func (s *Server) StopGateway() {
	s.gwMu.Lock()
	defer s.gwMu.Unlock()
	s.stopGatewayLocked()
}

func (s *Server) stopGatewayLocked() {
	if s.gwSrv != nil {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		_ = s.gwSrv.Shutdown(shutdownCtx)
		cancel()
		s.gwSrv = nil
	}
	s.gwListening = false
}

// GatewayStatus reports the desired config versus the actual listener state.
// needsRestart is true when the gateway is enabled but not listening (e.g.
// the bind failed) — the UI uses it to show "restart required / not
// listening" instead of a misleading green badge.
func (s *Server) GatewayStatus() (enabled bool, addr string, listening bool) {
	s.cfgMu.RLock()
	enabled = s.cfg.Gateway.Enabled
	addr = s.cfg.Gateway.GatewayBindAddress()
	s.cfgMu.RUnlock()
	s.gwMu.Lock()
	defer s.gwMu.Unlock()
	if s.gwListening && s.gwAddr == addr && s.gwSrv != nil {
		listening = true
	}
	return enabled, addr, listening
}

func (s *Server) handleGatewayStatus(w http.ResponseWriter, r *http.Request) {
	enabled, addr, listening := s.GatewayStatus()
	writeJSON(w, http.StatusOK, map[string]any{
		"enabled":       enabled,
		"addr":          addr,
		"listening":     listening,
		"needs_restart": enabled && !listening,
	})
}
