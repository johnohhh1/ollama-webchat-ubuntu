//go:build linux

package server

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/ollama/ollama/app/store"
)

var (
	pidFile       = "" // unused on Linux — systemd manages the server
	serverLogPath = filepath.Join(os.Getenv("HOME"), ".ollama", "logs", "server.log")
)

// Server on Linux is a no-op wrapper: the Ollama daemon is managed by systemd.
// Run() blocks until ctx is cancelled while health-checking localhost:11434.
type Server struct {
	store *store.Store
	dev   bool
}

type InferenceCompute struct {
	Library string
	Variant string
	Compute string
	Driver  string
	Name    string
	VRAM    string
}

type InferenceInfo struct {
	Computes             []InferenceCompute
	DefaultContextLength int
}

func New(s *store.Store, devMode bool) *Server {
	return &Server{store: s, dev: devMode}
}

// Run blocks until ctx is cancelled. It health-checks the existing systemd
// Ollama service every 30 s but never spawns a subprocess.
func (s *Server) Run(ctx context.Context) error {
	slog.Info("linux: using existing ollama systemd service at localhost:11434")

	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	// Initial health check
	if err := checkHealth(); err != nil {
		slog.Warn("ollama health check failed at startup", "error", err)
	}

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			if err := checkHealth(); err != nil {
				slog.Warn("ollama health check failed", "error", err)
			}
		}
	}
}

func checkHealth() error {
	resp, err := http.Get("http://localhost:11434/api/version")
	if err != nil {
		return fmt.Errorf("cannot reach ollama: %w", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("ollama returned status %d", resp.StatusCode)
	}
	return nil
}

// GetInferenceInfo returns inference info on Linux.
// GPU details come directly from the running systemd-managed server.
// DefaultContextLength is set to Ollama's default num_ctx so the
// context length slider in Settings is enabled.
func GetInferenceInfo(_ context.Context) (*InferenceInfo, error) {
	return &InferenceInfo{DefaultContextLength: 8192}, nil
}
