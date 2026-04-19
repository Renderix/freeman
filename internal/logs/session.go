package logs

import (
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Session is a parsed log file grouped into turns.
type Session struct {
	Path      string    `json:"path"`
	Filename  string    `json:"filename"`
	Date      string    `json:"date"`       // yyyy-mm-dd folder name
	StartTime time.Time `json:"start_time"`
	EndTime   time.Time `json:"end_time"`
	DurationMS int64    `json:"duration_ms"`
	Turns     []Turn    `json:"turns"`
	// Unbound events that don't belong to any turn — startup init logs,
	// keyword events outside turns, shutdown.
	Startup  []Event `json:"startup,omitempty"`
	Shutdown []Event `json:"shutdown,omitempty"`
	// Aggregates for the session summary bar.
	TurnCount    int `json:"turn_count"`
	ToolCallCount int `json:"tool_call_count"`
	// WakeEvents records wake/mute/stop detection events for the Gantt.
	WakeEvents []WakeEvent `json:"wake_events,omitempty"`
}

// Turn is one user utterance and everything the assistant did in response
// until the next utterance (or session end).
type Turn struct {
	Index       int        `json:"index"`
	StartTime   time.Time  `json:"start_time"`
	EndTime     time.Time  `json:"end_time"`
	HeardText   string     `json:"heard_text"`
	AssistantLines []SpokenLine `json:"assistant_lines,omitempty"`
	Tools       []ToolCall `json:"tools,omitempty"`
	TaskEvents  []Event    `json:"task_events,omitempty"`
	// DeadAirMS is the gap from HeardTime to the first spoken output in
	// this turn. High values are what we hunt when debugging latency.
	DeadAirMS   int64      `json:"dead_air_ms"`
	TotalMS     int64      `json:"total_ms"`
}

// SpokenLine is a single "speaking" event the TTS played.
type SpokenLine struct {
	Time    time.Time `json:"time"`
	Text    string    `json:"text"`
	IsFiller bool     `json:"is_filler"`
}

// ToolCall pairs an mdtool call with its result (if it arrived).
type ToolCall struct {
	Name       string    `json:"name"`
	Args       string    `json:"args"`
	CalledAt   time.Time `json:"called_at"`
	ResultAt   time.Time `json:"result_at"`
	DurationMS int       `json:"duration_ms"`
	Ok         bool      `json:"ok"`
	Output     string    `json:"output,omitempty"`
	Error      string    `json:"error,omitempty"`
}

// WakeEvent is a Porcupine keyword detection. Not the near-misses.
type WakeEvent struct {
	Time    time.Time `json:"time"`
	Keyword string    `json:"keyword"`
	Score   string    `json:"score"`
}

// LoadSession parses the log file at path into a structured Session.
func LoadSession(path string) (*Session, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	events, err := ParseLog(f)
	if err != nil {
		return nil, err
	}
	s := &Session{
		Path:     path,
		Filename: filepath.Base(path),
		Date:     filepath.Base(filepath.Dir(path)),
	}
	if len(events) > 0 {
		s.StartTime = events[0].Time
		s.EndTime = events[len(events)-1].Time
		s.DurationMS = s.EndTime.Sub(s.StartTime).Milliseconds()
	}
	groupEvents(s, events)
	return s, nil
}

// groupEvents walks events and splits them into turns. A turn begins when
// we see `msg=heard` and continues until the next `heard` (or end of log).
// Everything before the first `heard` goes into Startup.
func groupEvents(s *Session, events []Event) {
	var cur *Turn
	var curIdx int
	// Pending tool calls keyed by name — mdtool result matches by name and
	// comes shortly after the call. Multiple same-name calls within a turn
	// queue up.
	type pending struct {
		tc   *ToolCall
	}
	var pendingCalls []pending

	finish := func() {
		if cur == nil {
			return
		}
		if !cur.EndTime.IsZero() && !cur.StartTime.IsZero() {
			cur.TotalMS = cur.EndTime.Sub(cur.StartTime).Milliseconds()
		}
		if len(cur.AssistantLines) > 0 && !cur.StartTime.IsZero() {
			cur.DeadAirMS = cur.AssistantLines[0].Time.Sub(cur.StartTime).Milliseconds()
		} else if len(cur.Tools) > 0 && !cur.StartTime.IsZero() {
			cur.DeadAirMS = cur.Tools[0].CalledAt.Sub(cur.StartTime).Milliseconds()
		}
		s.Turns = append(s.Turns, *cur)
		cur = nil
		pendingCalls = nil
	}

	for _, ev := range events {
		switch ev.Msg {
		case "heard":
			finish()
			curIdx++
			cur = &Turn{
				Index:     curIdx,
				StartTime: ev.Time,
				EndTime:   ev.Time,
				HeardText: ev.Field("text"),
			}
		case "speaking":
			// Skip greetings and farewells that fire outside a heard-bound
			// turn — route them to startup/shutdown for context.
			text := ev.Field("text")
			if cur == nil {
				s.Startup = append(s.Startup, ev)
				continue
			}
			cur.AssistantLines = append(cur.AssistantLines, SpokenLine{
				Time: ev.Time,
				Text: text,
				IsFiller: isFillerPhrase(text),
			})
			cur.EndTime = ev.Time
		case "mdtool call":
			if cur == nil {
				s.Startup = append(s.Startup, ev)
				continue
			}
			tc := ToolCall{
				Name:     ev.Field("name"),
				Args:     ev.Field("args"),
				CalledAt: ev.Time,
			}
			cur.Tools = append(cur.Tools, tc)
			pendingCalls = append(pendingCalls, pending{tc: &cur.Tools[len(cur.Tools)-1]})
			cur.EndTime = ev.Time
			s.ToolCallCount++
		case "mdtool result":
			name := ev.Field("name")
			// Match to the oldest pending call with this name.
			for i, p := range pendingCalls {
				if p.tc.Name == name {
					p.tc.ResultAt = ev.Time
					p.tc.DurationMS = ev.IntField("duration_ms")
					p.tc.Ok = ev.BoolField("ok")
					p.tc.Output = ev.Field("output_preview")
					p.tc.Error = ev.Field("error")
					pendingCalls = append(pendingCalls[:i], pendingCalls[i+1:]...)
					break
				}
			}
			if cur != nil {
				cur.EndTime = ev.Time
			}
		case "tool activity", "task state", "task transition":
			if cur != nil {
				cur.TaskEvents = append(cur.TaskEvents, ev)
				cur.EndTime = ev.Time
			}
		case "keyword detected":
			s.WakeEvents = append(s.WakeEvents, WakeEvent{
				Time: ev.Time, Keyword: ev.Field("keyword"), Score: ev.Field("score"),
			})
		case "wake word detected", "mute word detected", "stop word detected — shutting down":
			// Already counted via keyword detected above. Keep unbound
			// events in shutdown/startup for context if relevant.
			if strings.HasPrefix(ev.Msg, "stop") {
				s.Shutdown = append(s.Shutdown, ev)
			}
		case "speaking farewell":
			s.Shutdown = append(s.Shutdown, ev)
		case "keyword near-miss", "tools loaded", "audio: context ready":
			if cur == nil {
				s.Startup = append(s.Startup, ev)
			}
		default:
			// Other events attach to current turn for fidelity.
			if cur != nil {
				cur.TaskEvents = append(cur.TaskEvents, ev)
				cur.EndTime = ev.Time
			}
		}
	}
	finish()
	s.TurnCount = len(s.Turns)
}

// isFillerPhrase heuristically flags system-spoken fillers so the UI can
// mark them differently from actual assistant responses. Kept in sync
// with toolFillerVariants in internal/conv/session.go.
func isFillerPhrase(text string) bool {
	switch strings.TrimSpace(text) {
	case "One sec, let me check.", "Hold on, looking.", "Hmm, lemme see.", "Give me a second.",
		"Hang on.", "One moment.", "Reading that.",
		"Hold on.", "One sec.", "Let me look at that.",
		"Lemme find it.", "Hold on, searching.",
		"Let me look.", "Checking your screen.",
		"Let me check.":
		return true
	}
	return false
}

// ListSessions walks the logs root (~/.freeman/logs/) and returns a
// date-grouped list of available sessions, newest first.
type SessionSummary struct {
	Path      string    `json:"path"`
	Filename  string    `json:"filename"`
	Date      string    `json:"date"`
	StartTime time.Time `json:"start_time"`
}

func ListSessions(root string) ([]SessionSummary, error) {
	var out []SessionSummary
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil // skip unreadable entries, don't abort whole walk
		}
		if d.IsDir() || !strings.HasSuffix(d.Name(), ".log") {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return nil
		}
		out = append(out, SessionSummary{
			Path:      p,
			Filename:  d.Name(),
			Date:      filepath.Base(filepath.Dir(p)),
			StartTime: info.ModTime(),
		})
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].StartTime.After(out[j].StartTime)
	})
	return out, nil
}
