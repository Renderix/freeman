package logs

import (
	"embed"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

//go:embed web/index.html
var webFS embed.FS

// MicController is the subset of the capture device the logs server
// needs to expose a soft mic-mute toggle to the menu bar. Kept as an
// interface so the logs package doesn't import internal/audio/capture.
type MicController interface {
	UserMuted() bool
	SetUserMuted(bool)
}

// Server exposes a local HTML viewer for Freeman session logs, plus a
// tiny control API (currently just mic mute) that the menu bar calls.
type Server struct {
	root string // e.g. ~/.freeman/logs
	mux  *http.ServeMux
	mic  MicController
}

// NewServer returns a configured log viewer. root is scanned for session
// files under <date>/<name>.log.
func NewServer(root string) *Server {
	s := &Server{root: root, mux: http.NewServeMux()}
	s.mux.HandleFunc("/", s.handleIndex)
	s.mux.HandleFunc("/api/sessions", s.handleList)
	s.mux.HandleFunc("/api/session", s.handleSession)
	s.mux.HandleFunc("/mic/status", s.handleMicStatus)
	s.mux.HandleFunc("/mic/mute", s.handleMicMute)
	s.mux.HandleFunc("/mic/unmute", s.handleMicUnmute)
	return s
}

// SetMicController wires in the capture device so /mic/* endpoints
// start working. Safe to call before Serve; must be called before the
// menu bar issues its first request or endpoints return 503.
func (s *Server) SetMicController(m MicController) { s.mic = m }

// Listen binds to 127.0.0.1:port. port=0 picks a free port. Returns the
// listener and the resolved address so the caller can open a browser.
func (s *Server) Listen(port int) (net.Listener, string, error) {
	ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
	if err != nil {
		return nil, "", err
	}
	return ln, "http://" + ln.Addr().String(), nil
}

// Serve runs the HTTP server on ln until ln closes or an error occurs.
func (s *Server) Serve(ln net.Listener) error {
	return http.Serve(ln, s.mux)
}

func (s *Server) handleIndex(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	data, err := webFS.ReadFile("web/index.html")
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write(data)
}

func (s *Server) handleList(w http.ResponseWriter, r *http.Request) {
	sessions, err := ListSessions(s.root)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	writeJSON(w, sessions)
}

func (s *Server) handleSession(w http.ResponseWriter, r *http.Request) {
	p := r.URL.Query().Get("path")
	if p == "" {
		http.Error(w, "missing path", 400)
		return
	}
	// Sandbox: only allow files under root.
	abs, err := filepath.Abs(p)
	if err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	rootAbs, _ := filepath.Abs(s.root)
	if !strings.HasPrefix(abs, rootAbs) {
		http.Error(w, "outside logs root", 403)
		return
	}
	if _, err := os.Stat(abs); err != nil {
		http.Error(w, err.Error(), 404)
		return
	}
	sess, err := LoadSession(abs)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	writeJSON(w, sess)
}

func (s *Server) handleMicStatus(w http.ResponseWriter, r *http.Request) {
	if s.mic == nil {
		http.Error(w, "mic controller not wired", 503)
		return
	}
	writeJSON(w, map[string]bool{"muted": s.mic.UserMuted()})
}

func (s *Server) handleMicMute(w http.ResponseWriter, r *http.Request) {
	s.setMuted(w, r, true)
}

func (s *Server) handleMicUnmute(w http.ResponseWriter, r *http.Request) {
	s.setMuted(w, r, false)
}

func (s *Server) setMuted(w http.ResponseWriter, r *http.Request, muted bool) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST required", http.StatusMethodNotAllowed)
		return
	}
	if s.mic == nil {
		http.Error(w, "mic controller not wired", 503)
		return
	}
	s.mic.SetUserMuted(muted)
	writeJSON(w, map[string]bool{"muted": muted})
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	_ = enc.Encode(v)
}
