package api

import (
	"embed"
	"encoding/json"
	"fmt"
	"net/http"
	"runtime"
	"sync"
	"time"

	"github.com/Renderix/freeman/internal/config"
	"github.com/Renderix/freeman/internal/engine"
	"github.com/Renderix/freeman/internal/session"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/gorilla/websocket"
)

//go:embed static/*.html
var staticFS embed.FS

var upgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool {
		return true // Allow all origins for the TTS server
	},
}

// Server represents the Freeman TTS server.
type Server struct {
	Engine    *engine.TTSEngine
	Sessions  map[string]*session.Session
	Mu        sync.RWMutex
	Conf      config.Config
	StartTime time.Time
}

// NewServer creates a new server instance.
func NewServer(e *engine.TTSEngine, conf config.Config) *Server {
	return &Server{
		Engine:    e,
		Sessions:  make(map[string]*session.Session),
		Conf:      conf,
		StartTime: time.Now(),
	}
}

// Start launches the Gin server.
func (s *Server) Start(port int) error {
	// Suppress Gin's verbose [GIN-debug] route-registration lines and the
	// per-request access log that gin.Default() emits in development mode.
	// Freeman uses its own slog-based structured logging; Gin's built-in
	// text logger would otherwise interleave untagged lines with Go output.
	gin.SetMode(gin.ReleaseMode)
	r := gin.Default()

	r.GET("/", func(c *gin.Context) {
		content, _ := staticFS.ReadFile("static/index.html")
		c.Data(200, "text/html; charset=utf-8", content)
	})

	r.GET("/settings", func(c *gin.Context) {
		content, _ := staticFS.ReadFile("static/settings.html")
		c.Data(200, "text/html; charset=utf-8", content)
	})

	r.GET("/api/settings", func(c *gin.Context) {
		settings := config.LoadUserSettings()
		c.JSON(200, settings)
	})

	r.POST("/api/settings", func(c *gin.Context) {
		var settings config.UserSettings
		if err := c.BindJSON(&settings); err != nil {
			c.JSON(400, gin.H{"error": "Invalid settings"})
			return
		}
		if err := config.SaveUserSettings(settings); err != nil {
			c.JSON(500, gin.H{"error": "Failed to save settings"})
			return
		}
		c.JSON(200, gin.H{"status": "saved"})
	})

	r.GET("/health", func(c *gin.Context) {
		c.JSON(200, gin.H{
			"status":        "healthy",
			"engine_loaded": s.Engine != nil,
		})
	})

	r.GET("/metrics", func(c *gin.Context) {
		content, _ := staticFS.ReadFile("static/metrics.html")
		c.Data(200, "text/html; charset=utf-8", content)
	})

	r.GET("/api/metrics", func(c *gin.Context) {
		var mem runtime.MemStats
		runtime.ReadMemStats(&mem)

		s.Mu.RLock()
		activeSessions := len(s.Sessions)
		s.Mu.RUnlock()

		c.JSON(200, gin.H{
			"memory": gin.H{
				"alloc_mb":       float64(mem.Alloc) / 1024 / 1024,
				"total_alloc_mb": float64(mem.TotalAlloc) / 1024 / 1024,
				"sys_mb":         float64(mem.Sys) / 1024 / 1024,
				"heap_inuse_mb":  float64(mem.HeapInuse) / 1024 / 1024,
				"heap_objects":   mem.HeapObjects,
			},
			"runtime": gin.H{
				"goroutines":   runtime.NumGoroutine(),
				"go_version":   runtime.Version(),
				"num_cpu":      runtime.NumCPU(),
				"gomaxprocs":   runtime.GOMAXPROCS(0),
				"uptime_sec":   time.Since(s.StartTime).Seconds(),
				"uptime_human": time.Since(s.StartTime).Round(time.Second).String(),
			},
			"server": gin.H{
				"active_sessions": activeSessions,
				"engine_loaded":   s.Engine != nil,
			},
		})
	})

	r.GET("/ws/stream", func(c *gin.Context) {
		s.handleWebSocket(c.Writer, c.Request)
	})

	return r.Run(fmt.Sprintf(":%d", port))
}

func (s *Server) handleWebSocket(w http.ResponseWriter, r *http.Request) {
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		fmt.Printf("failed to upgrade: %v\n", err)
		return
	}
	defer conn.Close()

	sessionID := uuid.New().String()
	var currentSession *session.Session

	// Helper to send JSON messages
	sendJSON := func(v interface{}) error {
		return conn.WriteJSON(v)
	}

	// Helper to send Binary messages
	sendBinary := func(b []byte) error {
		return conn.WriteMessage(websocket.BinaryMessage, b)
	}

	// Session cleanup
	defer func() {
		if currentSession != nil {
			currentSession.IsActive = false
			s.Mu.Lock()
			delete(s.Sessions, sessionID)
			s.Mu.Unlock()
		}
	}()

	// Timeout checker (goroutine)
	stopTimeout := make(chan struct{})
	defer close(stopTimeout)
	go func() {
		ticker := time.NewTicker(500 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				if currentSession != nil && currentSession.IsActive {
					currentSession.CheckTimeout()
				}
			case <-stopTimeout:
				return
			}
		}
	}()

	for {
		_, message, err := conn.ReadMessage()
		if err != nil {
			fmt.Printf("session %s disconnected: %v\n", sessionID, err)
			if currentSession != nil {
				currentSession.SendStreamEnd("disconnect")
			}
			break
		}

		var data map[string]interface{}
		if err := json.Unmarshal(message, &data); err != nil {
			sendJSON(map[string]interface{}{
				"type":    "error",
				"message": "Invalid JSON",
				"code":    session.ErrorCodeFatal,
			})
			continue
		}

		msgType, _ := data["type"].(string)

		switch msgType {
		case "init":
			voice, ok := data["voice"].(string)
			if !ok {
				voice = s.Conf.TTS.DefaultVoice
			}
			speed, ok := data["speed"].(float64)
			if !ok {
				speed = s.Conf.TTS.DefaultSpeed
			}

			// Validate
			if _, exists := engine.Voices[voice]; !exists {
				sendJSON(map[string]interface{}{
					"type":    "error",
					"message": fmt.Sprintf("Invalid voice: %s", voice),
					"code":    session.ErrorCodeInvalidVoice,
				})
				continue
			}

			currentSession = session.NewSession(sessionID, voice, speed, s.Engine, sendBinary, sendJSON)
			s.Mu.Lock()
			s.Sessions[sessionID] = currentSession
			s.Mu.Unlock()

			sendJSON(map[string]interface{}{
				"type":       "init_ack",
				"session_id": sessionID,
				"voice":      voice,
				"speed":      speed,
				"status":     "ready",
			})

		case "text":
			if currentSession == nil {
				// Auto-init
				currentSession = session.NewSession(sessionID, s.Conf.TTS.DefaultVoice, s.Conf.TTS.DefaultSpeed, s.Engine, sendBinary, sendJSON)
				s.Mu.Lock()
				s.Sessions[sessionID] = currentSession
				s.Mu.Unlock()
			}

			chunk, _ := data["chunk"].(string)
			isFinal, _ := data["is_final"].(bool)

			if currentSession.Buffer.IsOverflow(chunk) {
				sendJSON(map[string]interface{}{
					"type":    "error",
					"message": "Buffer overflow",
					"code":    session.ErrorCodeBufferOverflow,
				})
				continue
			}

			pending, _ := currentSession.HandleText(chunk, isFinal)

			sendJSON(map[string]interface{}{
				"type":              "text_ack",
				"buffered_chars":    len(chunk), // simplified for ack
				"pending_sentences": pending,
			})

			if isFinal {
				currentSession.SendStreamEnd("client_final")
			}

		case "flush":
			if currentSession != nil {
				currentSession.Flush()
			}

		case "end":
			if currentSession != nil {
				currentSession.Flush()
				currentSession.SendStreamEnd("client_end")
			}
			return
		}
	}
}
