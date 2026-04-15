package pm_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Renderix/freeman/internal/call"
	"github.com/Renderix/freeman/internal/pm"
)

// anthropicResp builds a minimal Anthropic Messages API response with a tool_use block.
func anthropicResp(toolName string, input any) map[string]any {
	inputJSON, _ := json.Marshal(input)
	return map[string]any{
		"id":   "msg_test",
		"type": "message",
		"role": "assistant",
		"content": []map[string]any{
			{
				"type":  "tool_use",
				"id":    "tu_test",
				"name":  toolName,
				"input": json.RawMessage(inputJSON),
			},
		},
		"model":       "claude-haiku-4-5-20251001",
		"stop_reason": "tool_use",
		"usage":       map[string]any{"input_tokens": 50, "output_tokens": 20},
	}
}

func newTestServer(t *testing.T, handler http.HandlerFunc) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	return srv
}

func TestHaikuPM_IntakeNeedsMore(t *testing.T) {
	srv := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(anthropicResp("ask_followup", map[string]any{
			"question": "what are the constraints?",
		}))
	})

	p := pm.NewWithBaseURL(pm.Config{
		APIKey:              "test",
		Model:               "claude-haiku-4-5-20251001",
		ConfidenceThreshold: 0.8,
	}, srv.URL)

	res, err := p.Intake(context.Background(), call.IntakeInput{Latest: "build a feature flag"})
	if err != nil {
		t.Fatalf("Intake error: %v", err)
	}
	if !res.NeedsMore {
		t.Error("NeedsMore = false, want true")
	}
	if res.Question != "what are the constraints?" {
		t.Errorf("Question = %q, want %q", res.Question, "what are the constraints?")
	}
}

func TestHaikuPM_IntakeObjective(t *testing.T) {
	srv := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(anthropicResp("complete_objective", map[string]any{
			"goal":                "add feature flag system",
			"acceptance_criteria": []string{"flag defaults off", "tests pass"},
			"constraints":         []string{"no breaking changes"},
			"notes":               []string{},
			"model_hint":          "sonnet",
			"spoken_summary":      "build a feature flag system that defaults off",
		}))
	})

	p := pm.NewWithBaseURL(pm.Config{
		APIKey:              "test",
		Model:               "claude-haiku-4-5-20251001",
		ConfidenceThreshold: 0.8,
	}, srv.URL)

	res, err := p.Intake(context.Background(), call.IntakeInput{Latest: "build a feature flag"})
	if err != nil {
		t.Fatalf("Intake error: %v", err)
	}
	if res.NeedsMore {
		t.Error("NeedsMore = true, want false")
	}
	if res.Objective == nil {
		t.Fatal("Objective is nil")
	}
	if res.Objective.Goal != "add feature flag system" {
		t.Errorf("Goal = %q", res.Objective.Goal)
	}
	if res.Objective.ModelHint != "sonnet" {
		t.Errorf("ModelHint = %q, want sonnet", res.Objective.ModelHint)
	}
}

func TestHaikuPM_IntakeJustGo(t *testing.T) {
	srv := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(anthropicResp("complete_objective", map[string]any{
			"goal":                "build something",
			"acceptance_criteria": []string{},
			"constraints":         []string{},
			"notes":               []string{},
			"model_hint":          "sonnet",
			"spoken_summary":      "ok, starting now",
		}))
	})

	p := pm.NewWithBaseURL(pm.Config{
		APIKey:              "test",
		Model:               "claude-haiku-4-5-20251001",
		ConfidenceThreshold: 0.8,
	}, srv.URL)

	res, err := p.Intake(context.Background(), call.IntakeInput{Latest: "just go"})
	if err != nil {
		t.Fatalf("Intake error: %v", err)
	}
	if res.NeedsMore {
		t.Error("NeedsMore = true for 'just go', want false")
	}
}

func TestHaikuPM_RouterAnswerInlineAboveThreshold(t *testing.T) {
	srv := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(anthropicResp("answer_inline", map[string]any{
			"answer":     "yes",
			"confidence": 0.95,
		}))
	})

	p := pm.NewWithBaseURL(pm.Config{
		APIKey:              "test",
		Model:               "claude-haiku-4-5-20251001",
		ConfidenceThreshold: 0.8,
	}, srv.URL)

	res, err := p.Route(context.Background(), call.RouteInput{
		Question: "use existing client?",
	})
	if err != nil {
		t.Fatalf("Route error: %v", err)
	}
	if res.AnswerInline != "yes" {
		t.Errorf("AnswerInline = %q, want yes", res.AnswerInline)
	}
	if res.SpokenQuestion != "" {
		t.Errorf("SpokenQuestion = %q, want empty", res.SpokenQuestion)
	}
}

func TestHaikuPM_RouterAnswerInlineBelowThreshold(t *testing.T) {
	srv := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(anthropicResp("answer_inline", map[string]any{
			"answer":     "maybe",
			"confidence": 0.5,
		}))
	})

	p := pm.NewWithBaseURL(pm.Config{
		APIKey:              "test",
		Model:               "claude-haiku-4-5-20251001",
		ConfidenceThreshold: 0.8,
	}, srv.URL)

	res, err := p.Route(context.Background(), call.RouteInput{
		Question: "use existing client?",
	})
	if err != nil {
		t.Fatalf("Route error: %v", err)
	}
	if res.AnswerInline != "" {
		t.Errorf("AnswerInline = %q, want empty (low confidence should escalate)", res.AnswerInline)
	}
	if res.SpokenQuestion != "use existing client?" {
		t.Errorf("SpokenQuestion = %q, want %q", res.SpokenQuestion, "use existing client?")
	}
}

func TestHaikuPM_RouterEscalate(t *testing.T) {
	srv := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(anthropicResp("escalate", map[string]any{
			"spoken_question": "should i use the existing auth client?",
		}))
	})

	p := pm.NewWithBaseURL(pm.Config{
		APIKey:              "test",
		Model:               "claude-haiku-4-5-20251001",
		ConfidenceThreshold: 0.8,
	}, srv.URL)

	res, err := p.Route(context.Background(), call.RouteInput{
		Question: "use existing client?",
	})
	if err != nil {
		t.Fatalf("Route error: %v", err)
	}
	if res.SpokenQuestion != "should i use the existing auth client?" {
		t.Errorf("SpokenQuestion = %q", res.SpokenQuestion)
	}
}

func TestHaikuPM_ResetClearsHistory(t *testing.T) {
	var reqBody map[string]any
	srv := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&reqBody)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(anthropicResp("ask_followup", map[string]any{
			"question": "tell me more",
		}))
	})

	p := pm.NewWithBaseURL(pm.Config{
		APIKey:              "test",
		Model:               "claude-haiku-4-5-20251001",
		ConfidenceThreshold: 0.8,
	}, srv.URL)

	ctx := context.Background()
	_, _ = p.Intake(ctx, call.IntakeInput{Latest: "first"})
	_, _ = p.Intake(ctx, call.IntakeInput{Latest: "second"})

	p.Reset()
	reqBody = nil
	_, _ = p.Intake(ctx, call.IntakeInput{Latest: "after reset"})

	if reqBody == nil {
		t.Fatal("server not called after Reset")
	}
	msgs, _ := reqBody["messages"].([]interface{})
	// After Reset, history is empty. Only the new user message is sent.
	if len(msgs) != 1 {
		t.Errorf("messages after Reset = %d, want 1 (only the new user turn)", len(msgs))
	}
}
