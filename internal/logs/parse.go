// Package logs parses Freeman session log files (slog text format) into
// a structured timeline — per-turn transcripts, tool calls, and timing
// annotations — that powers the `freeman logs` HTML viewer.
package logs

import (
	"bufio"
	"io"
	"strconv"
	"strings"
	"time"
)

// Event is one line from a session log after key=value parsing.
type Event struct {
	Time   time.Time         `json:"time"`
	Level  string            `json:"level"`
	Msg    string            `json:"msg"`
	Fields map[string]string `json:"fields,omitempty"`
	Raw    string            `json:"-"` // original line, retained for unknown types
}

// Field returns a named field or empty string.
func (e Event) Field(k string) string {
	if e.Fields == nil {
		return ""
	}
	return e.Fields[k]
}

// ParseLog reads slog text lines from r and returns the parsed events.
// Malformed lines are skipped silently so one bad line doesn't poison a
// whole session.
func ParseLog(r io.Reader) ([]Event, error) {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 64*1024), 4*1024*1024)
	var out []Event
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		ev, ok := parseLine(line)
		if !ok {
			continue
		}
		out = append(out, ev)
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

// parseLine tokenises a slog text line into an Event. The format is a
// sequence of key=value pairs; values can be quoted strings (with Go-style
// escapes) or bare tokens up to the next whitespace. Standard slog keys
// "time", "level", "msg" are pulled into named fields.
func parseLine(line string) (Event, bool) {
	tokens := tokenizeKV(line)
	if len(tokens) == 0 {
		return Event{}, false
	}
	ev := Event{Raw: line, Fields: make(map[string]string)}
	for _, kv := range tokens {
		switch kv.k {
		case "time":
			if t, err := time.Parse(time.RFC3339Nano, kv.v); err == nil {
				ev.Time = t
			}
		case "level":
			ev.Level = kv.v
		case "msg":
			ev.Msg = kv.v
		default:
			ev.Fields[kv.k] = kv.v
		}
	}
	if ev.Time.IsZero() && ev.Msg == "" {
		return Event{}, false
	}
	return ev, true
}

type kvPair struct{ k, v string }

// tokenizeKV walks the input left-to-right, emitting k=v pairs. Quoted
// values handle Go's standard escape sequences via strconv.Unquote.
func tokenizeKV(s string) []kvPair {
	var out []kvPair
	i := 0
	n := len(s)
	for i < n {
		// Skip whitespace.
		for i < n && (s[i] == ' ' || s[i] == '\t') {
			i++
		}
		if i >= n {
			break
		}
		// Read key up to '='.
		keyStart := i
		for i < n && s[i] != '=' && s[i] != ' ' {
			i++
		}
		if i >= n || s[i] != '=' {
			break
		}
		key := s[keyStart:i]
		i++ // skip '='
		// Read value.
		if i < n && s[i] == '"' {
			// Quoted: find the matching unescaped '"'.
			end := i + 1
			for end < n {
				if s[end] == '\\' && end+1 < n {
					end += 2
					continue
				}
				if s[end] == '"' {
					end++
					break
				}
				end++
			}
			raw := s[i:end]
			if unq, err := strconv.Unquote(raw); err == nil {
				out = append(out, kvPair{key, unq})
			} else {
				// Fall back: strip outer quotes.
				if len(raw) >= 2 && raw[0] == '"' && raw[len(raw)-1] == '"' {
					out = append(out, kvPair{key, raw[1 : len(raw)-1]})
				} else {
					out = append(out, kvPair{key, raw})
				}
			}
			i = end
		} else {
			// Bare token up to next whitespace.
			end := i
			for end < n && s[end] != ' ' && s[end] != '\t' {
				end++
			}
			out = append(out, kvPair{key, s[i:end]})
			i = end
		}
	}
	return out
}

// IntField returns a field as int or 0.
func (e Event) IntField(k string) int {
	v, _ := strconv.Atoi(e.Field(k))
	return v
}

// BoolField returns a field as bool ("true" → true, else false).
func (e Event) BoolField(k string) bool {
	return e.Field(k) == "true"
}
