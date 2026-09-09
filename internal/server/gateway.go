package server

import (
	"context"
	"log"
	"net/http"
	"time"
)

// ListenAndServeGateway serves the OpenAI/Ollama-compatible gateway mux on
// its own listener until ctx is cancelled. It is a no-op when the gateway
// is disabled and reports bind failures as errors.
func (s *Server) ListenAndServeGateway(ctx context.Context) error {
	s.cfgMu.RLock()
	enabled := s.cfg.Gateway.Enabled
	addr := s.cfg.Gateway.GatewayBindAddress()
	s.cfgMu.RUnlock()
	if !enabled {
		return nil
	}
	srv := &http.Server{
		Addr:    addr,
		Handler: logging(s.gatewayRoutes()),
	}

	// Graceful shutdown.
	go func() {
		<-ctx.Done()
		log.Println("gateway: shutting down…")
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdownCtx)
	}()

	log.Printf("gateway: listening on http://%s", addr)
	if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		return err
	}
	return nil
}
