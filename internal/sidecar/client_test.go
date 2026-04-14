package sidecar

import (
	"bufio"
	"context"
	"io"
	"testing"
	"time"
)

func TestClient_RoundTrip(t *testing.T) {
	// Freeman writes to clientOut (the sidecar's stdin) and reads from clientIn
	// (the sidecar's stdout). We simulate a sidecar by driving the other ends.
	sidecarStdinR, sidecarStdinW := io.Pipe()
	sidecarStdoutR, sidecarStdoutW := io.Pipe()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	client := NewClientFromPipes(sidecarStdinW, sidecarStdoutR)
	defer client.Close()

	// Fake sidecar goroutine: reads start, emits assistant_text, ask_user.
	go func() {
		defer sidecarStdoutW.Close()
		// Read one line (the start msg).
		sc := bufio.NewScanner(sidecarStdinR)
		if !sc.Scan() {
			return
		}
		_, err := Decode(sc.Bytes())
		if err != nil {
			return
		}
		_ = Encode(sidecarStdoutW, AssistantTextMsg{Type: MsgTypeAssistantText, Text: "hi"})
		_ = Encode(sidecarStdoutW, AskUserMsg{Type: MsgTypeAskUser, ID: "q1", Question: "ok?"})
	}()

	// Send start.
	err := client.Send(StartMsg{
		Type: MsgTypeStart,
		Objective: ObjectivePayload{Goal: "g", Model: "sonnet"},
	})
	if err != nil {
		t.Fatal(err)
	}

	// Read two events.
	events := client.Events()
	var got1, got2 Message
	select {
	case got1 = <-events:
	case <-ctx.Done():
		t.Fatal("timed out waiting for first event")
	}
	select {
	case got2 = <-events:
	case <-ctx.Done():
		t.Fatal("timed out waiting for second event")
	}

	if _, ok := got1.(AssistantTextMsg); !ok {
		t.Errorf("got1 = %T, want AssistantTextMsg", got1)
	}
	au, ok := got2.(AskUserMsg)
	if !ok {
		t.Fatalf("got2 = %T, want AskUserMsg", got2)
	}
	if au.ID != "q1" || au.Question != "ok?" {
		t.Errorf("au = %+v", au)
	}
}

func TestClient_SendAskUserReply(t *testing.T) {
	sidecarStdinR, sidecarStdinW := io.Pipe()
	sidecarStdoutR, sidecarStdoutW := io.Pipe()
	defer sidecarStdoutW.Close()

	client := NewClientFromPipes(sidecarStdinW, sidecarStdoutR)
	defer client.Close()

	done := make(chan Message, 1)
	go func() {
		sc := bufio.NewScanner(sidecarStdinR)
		if !sc.Scan() {
			done <- nil
			return
		}
		m, err := Decode(sc.Bytes())
		if err != nil {
			done <- nil
			return
		}
		done <- m
	}()

	if err := client.Send(AskUserReplyMsg{
		Type: MsgTypeAskUserReply, ID: "q1", Answer: "yes",
	}); err != nil {
		t.Fatal(err)
	}

	select {
	case m := <-done:
		r, ok := m.(AskUserReplyMsg)
		if !ok || r.ID != "q1" || r.Answer != "yes" {
			t.Errorf("got %+v", m)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out")
	}
}

