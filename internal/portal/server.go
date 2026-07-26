// Package portal serves a small authenticated editor for long-term memory.
package portal

import (
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"time"

	_ "embed"

	"github.com/hetsaraiya/spores/internal/memory"
)

//go:embed index.html
var page []byte

const maxBody = 64 << 10

type Server struct {
	store *memory.Store
	token string
}

// New fails when no token is configured: this endpoint edits what the agent
// believes about its owner, so it must never come up open.
func New(store *memory.Store, token string) (*Server, error) {
	if strings.TrimSpace(token) == "" {
		return nil, fmt.Errorf("portal token is required")
	}
	return &Server{store: store, token: token}, nil
}

// Handler builds the routes. /health and the page shell are unauthenticated —
// the shell carries no memory content — everything under /api is not.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte("ok"))
	})
	mux.HandleFunc("GET /{$}", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Content-Security-Policy", "default-src 'self'; style-src 'unsafe-inline'; script-src 'unsafe-inline'")
		w.Write(page)
	})
	mux.Handle("GET /api/files", s.auth(s.list))
	mux.Handle("GET /api/file", s.auth(s.read))
	mux.Handle("PUT /api/file", s.auth(s.write))
	mux.Handle("DELETE /api/file", s.auth(s.remove))
	return mux
}

// Serve runs until the process exits. A portal failure is logged, never fatal:
// it must not take the Slack bot down with it.
func (s *Server) Serve(addr string) {
	server := &http.Server{
		Addr:              addr,
		Handler:           s.Handler(),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      15 * time.Second,
		IdleTimeout:       60 * time.Second,
	}
	log.Printf("memory portal listening on %s", addr)
	if err := server.ListenAndServe(); err != nil {
		log.Printf("memory portal stopped: %v", err)
	}
}

func (s *Server) auth(next http.HandlerFunc) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Header only — a token in the query string would land in access logs.
		supplied, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
		if !ok || subtle.ConstantTimeCompare([]byte(supplied), []byte(s.token)) != 1 {
			fail(w, http.StatusUnauthorized, "unauthorized")
			return
		}
		next(w, r)
	})
}

func (s *Server) list(w http.ResponseWriter, _ *http.Request) {
	infos, err := s.store.Names()
	if err != nil {
		fail(w, http.StatusInternalServerError, err.Error())
		return
	}
	send(w, infos)
}

func (s *Server) read(w http.ResponseWriter, r *http.Request) {
	content, err := s.store.Read(r.URL.Query().Get("name"))
	if err != nil {
		fail(w, http.StatusBadRequest, err.Error())
		return
	}
	send(w, map[string]string{"content": content})
}

func (s *Server) write(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxBody))
	if err != nil {
		fail(w, http.StatusRequestEntityTooLarge, "body too large")
		return
	}
	name := r.URL.Query().Get("name")
	changed, err := s.store.Write(name, string(body))
	if err != nil {
		fail(w, http.StatusBadRequest, err.Error())
		return
	}
	log.Printf("memory: portal wrote %s (changed=%t)", name, changed)
	send(w, map[string]bool{"changed": changed})
}

func (s *Server) remove(w http.ResponseWriter, r *http.Request) {
	name := r.URL.Query().Get("name")
	changed, err := s.store.Delete(name)
	if err != nil {
		fail(w, http.StatusBadRequest, err.Error())
		return
	}
	log.Printf("memory: portal deleted %s (changed=%t)", name, changed)
	send(w, map[string]bool{"changed": changed})
}

func send(w http.ResponseWriter, payload any) {
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(payload); err != nil {
		log.Printf("memory portal: encode response: %v", err)
	}
}

func fail(w http.ResponseWriter, status int, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": message})
}
