package sidecar

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

func TestEncodeDecodeStart(t *testing.T) {
	msg := StartMsg{
		Type: MsgTypeStart,
		Objective: ObjectivePayload{
			Goal:               "build flag",
			AcceptanceCriteria: []string{"tests pass"},
			Constraints:        []string{"no db changes"},
			Model:              "sonnet",
		},
	}
	var buf bytes.Buffer
	if err := Encode(&buf, msg); err != nil {
		t.Fatal(err)
	}
	line := strings.TrimSpace(buf.String())
	if !strings.Contains(line, `"type":"start"`) {
		t.Errorf("missing type: %s", line)
	}
	// Round-trip.
	var raw map[string]any
	if err := json.Unmarshal([]byte(line), &raw); err != nil {
		t.Fatal(err)
	}
	if raw["type"] != "start" {
		t.Errorf("type = %v", raw["type"])
	}
}

func TestDecodeAskUser(t *testing.T) {
	line := `{"type":"ask_user","id":"q1","question":"use existing client?"}`
	m, err := Decode([]byte(line))
	if err != nil {
		t.Fatal(err)
	}
	au, ok := m.(AskUserMsg)
	if !ok {
		t.Fatalf("got %T, want AskUserMsg", m)
	}
	if au.ID != "q1" || au.Question != "use existing client?" {
		t.Errorf("got %+v", au)
	}
}

func TestDecodeAssistantText(t *testing.T) {
	line := `{"type":"assistant_text","text":"editing file"}`
	m, err := Decode([]byte(line))
	if err != nil {
		t.Fatal(err)
	}
	at, ok := m.(AssistantTextMsg)
	if !ok || at.Text != "editing file" {
		t.Errorf("got %T %+v", m, m)
	}
}

func TestDecodeDone(t *testing.T) {
	line := `{"type":"done","summary":"edited 3 files"}`
	m, err := Decode([]byte(line))
	if err != nil {
		t.Fatal(err)
	}
	d, ok := m.(DoneMsg)
	if !ok || d.Summary != "edited 3 files" {
		t.Errorf("got %T %+v", m, m)
	}
}

func TestDecodeError(t *testing.T) {
	line := `{"type":"error","message":"boom"}`
	m, err := Decode([]byte(line))
	if err != nil {
		t.Fatal(err)
	}
	e, ok := m.(ErrorMsg)
	if !ok || e.Message != "boom" {
		t.Errorf("got %T %+v", m, m)
	}
}

func TestDecodeUnknownType(t *testing.T) {
	line := `{"type":"huh","foo":"bar"}`
	_, err := Decode([]byte(line))
	if err == nil {
		t.Error("expected error for unknown type")
	}
}

func TestEncodeAskUserReply(t *testing.T) {
	msg := AskUserReplyMsg{
		Type:   MsgTypeAskUserReply,
		ID:     "q1",
		Answer: "yes",
	}
	var buf bytes.Buffer
	if err := Encode(&buf, msg); err != nil {
		t.Fatal(err)
	}
	line := strings.TrimSpace(buf.String())
	if !strings.Contains(line, `"type":"ask_user_reply"`) {
		t.Errorf("missing type: %s", line)
	}
	if !strings.Contains(line, `"id":"q1"`) {
		t.Errorf("missing id: %s", line)
	}
}
