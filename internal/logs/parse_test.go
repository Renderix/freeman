package logs

import (
	"strings"
	"testing"
)

func TestParseLogBasic(t *testing.T) {
	input := `time=2026-04-19T10:48:41.479+05:30 level=INFO msg="audio: context ready"
time=2026-04-19T10:49:06.097+05:30 level=INFO msg=heard text="What is this project about?"
time=2026-04-19T10:49:08.528+05:30 level=INFO msg=speaking text="Freeman is a TTS server."
time=2026-04-19T11:22:47.335+05:30 level=INFO msg="mdtool result" name=web_search ok=true duration_ms=983 output_preview="weather today at DuckDuckGo"
`
	events, err := ParseLog(strings.NewReader(input))
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 4 {
		t.Fatalf("want 4 events, got %d", len(events))
	}
	if events[1].Msg != "heard" || events[1].Field("text") != "What is this project about?" {
		t.Errorf("heard parse wrong: %+v", events[1])
	}
	if events[3].Msg != "mdtool result" || events[3].Field("name") != "web_search" {
		t.Errorf("mdtool result parse wrong: %+v", events[3])
	}
	if !events[3].BoolField("ok") || events[3].IntField("duration_ms") != 983 {
		t.Errorf("typed fields wrong: %+v", events[3])
	}
}

func TestParseLogSkipsMalformed(t *testing.T) {
	input := "not a log line\ntime=2026-04-19T10:48:41.479+05:30 level=INFO msg=heard text=hi\n"
	events, err := ParseLog(strings.NewReader(input))
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 {
		t.Fatalf("want 1 parsed event, got %d", len(events))
	}
}

func TestGroupIntoTurns(t *testing.T) {
	input := `time=2026-04-19T10:48:41.000+05:30 level=INFO msg="audio: context ready"
time=2026-04-19T10:49:06.000+05:30 level=INFO msg=heard text="first question"
time=2026-04-19T10:49:07.000+05:30 level=INFO msg="mdtool call" name=web_search args="{\"q\":\"x\"}"
time=2026-04-19T10:49:08.000+05:30 level=INFO msg="mdtool result" name=web_search ok=true duration_ms=1000 output_preview="hit"
time=2026-04-19T10:49:10.000+05:30 level=INFO msg=speaking text="answer one."
time=2026-04-19T10:49:20.000+05:30 level=INFO msg=heard text="second question"
time=2026-04-19T10:49:22.000+05:30 level=INFO msg=speaking text="answer two."
`
	events, err := ParseLog(strings.NewReader(input))
	if err != nil {
		t.Fatal(err)
	}
	s := &Session{}
	groupEvents(s, events)
	if len(s.Turns) != 2 {
		t.Fatalf("want 2 turns, got %d", len(s.Turns))
	}
	t1 := s.Turns[0]
	if t1.HeardText != "first question" {
		t.Errorf("turn 1 heard: %q", t1.HeardText)
	}
	if len(t1.Tools) != 1 || t1.Tools[0].Name != "web_search" {
		t.Errorf("turn 1 tools: %+v", t1.Tools)
	}
	if !t1.Tools[0].Ok || t1.Tools[0].DurationMS != 1000 {
		t.Errorf("tool result not matched: %+v", t1.Tools[0])
	}
	if len(t1.AssistantLines) != 1 {
		t.Errorf("turn 1 assistant lines: %+v", t1.AssistantLines)
	}
	// Perceived dead air: from end-of-speech (heard at 10:49:06) to first
	// audible output (assistant line at 10:49:10) = 4000ms. The tool call
	// in between is silent to the user.
	if t1.DeadAirMS != 4000 {
		t.Errorf("dead air wrong: got %d, want 4000", t1.DeadAirMS)
	}
	if len(s.Startup) == 0 {
		t.Error("startup should contain the audio: context ready event")
	}
}
