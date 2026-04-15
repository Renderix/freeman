package pm

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"

	"github.com/Renderix/freeman/internal/call"
)

// Config configures the HaikuPM client.
type Config struct {
	APIKey              string
	Model               string  // e.g. "claude-haiku-4-5-20251001"
	ConfidenceThreshold float64 // default 0.8; answer_inline below this → escalate
}

// HaikuPM implements call.PM using Claude Haiku via the Anthropic Messages API.
// Intake is multi-turn (history preserved between calls); Router is stateless.
// History is cleared by Reset().
type HaikuPM struct {
	client  anthropic.Client
	cfg     Config
	history []anthropic.MessageParam // Intake conversation history; nil after Reset
}

// New creates a HaikuPM connected to api.anthropic.com.
func New(cfg Config) *HaikuPM {
	if cfg.ConfidenceThreshold == 0 {
		cfg.ConfidenceThreshold = 0.8
	}
	return &HaikuPM{
		client: anthropic.NewClient(option.WithAPIKey(cfg.APIKey)),
		cfg:    cfg,
	}
}

// NewWithBaseURL creates a HaikuPM pointing at a custom base URL (used in tests).
func NewWithBaseURL(cfg Config, baseURL string) *HaikuPM {
	if cfg.ConfidenceThreshold == 0 {
		cfg.ConfidenceThreshold = 0.8
	}
	return &HaikuPM{
		client: anthropic.NewClient(
			option.WithAPIKey(cfg.APIKey),
			option.WithBaseURL(baseURL),
		),
		cfg: cfg,
	}
}

// Reset clears Intake conversation history. Call at the start of each new call.
func (p *HaikuPM) Reset() {
	p.history = nil
}

// Intake implements call.PM. Appends the user turn to history, calls the API,
// and returns either NeedsMore (ask_followup) or a completed Objective.
func (p *HaikuPM) Intake(ctx context.Context, in call.IntakeInput) (call.PMIntakeResult, error) {
	userText := in.Latest
	if in.InterruptedText != "" {
		userText = fmt.Sprintf("[interrupted: %q] %s", in.InterruptedText, in.Latest)
	}
	p.history = append(p.history, anthropic.NewUserMessage(anthropic.NewTextBlock(userText)))

	resp, err := p.client.Messages.New(ctx, anthropic.MessageNewParams{
		Model:     anthropic.Model(p.cfg.Model),
		MaxTokens: 1024,
		System: []anthropic.TextBlockParam{
			{Text: intakeSystemPrompt},
		},
		Messages:   p.history,
		Tools:      intakeTools(),
		ToolChoice: anthropic.ToolChoiceUnionParam{OfAny: &anthropic.ToolChoiceAnyParam{}},
	})
	if err != nil {
		return call.PMIntakeResult{}, fmt.Errorf("anthropic intake: %w", err)
	}

	// Append assistant message to history before parsing.
	p.history = append(p.history, resp.ToParam())

	for _, block := range resp.Content {
		tu, ok := block.AsAny().(anthropic.ToolUseBlock)
		if !ok {
			continue
		}
		switch tu.Name {
		case "ask_followup":
			var args struct {
				Question string `json:"question"`
			}
			if err := json.Unmarshal(tu.Input, &args); err != nil {
				return call.PMIntakeResult{NeedsMore: true, Question: "sorry, one moment."}, nil
			}
			return call.PMIntakeResult{NeedsMore: true, Question: args.Question}, nil

		case "complete_objective":
			var args struct {
				Goal               string   `json:"goal"`
				AcceptanceCriteria []string `json:"acceptance_criteria"`
				Constraints        []string `json:"constraints"`
				Notes              []string `json:"notes"`
				ModelHint          string   `json:"model_hint"`
				SpokenSummary      string   `json:"spoken_summary"`
			}
			if err := json.Unmarshal(tu.Input, &args); err != nil {
				return call.PMIntakeResult{NeedsMore: true, Question: "sorry, one moment."}, nil
			}
			return call.PMIntakeResult{
				NeedsMore: false,
				Objective: &call.Objective{
					Goal:               args.Goal,
					AcceptanceCriteria: args.AcceptanceCriteria,
					Constraints:        args.Constraints,
					Notes:              args.Notes,
					ModelHint:          args.ModelHint,
					SpokenSummary:      args.SpokenSummary,
				},
			}, nil
		}
	}

	return call.PMIntakeResult{NeedsMore: true, Question: "sorry, one moment."}, nil
}

// Route implements call.PM. Stateless one-shot call. Returns inline answer or
// escalates to spoken question. Low-confidence answers are upgraded to escalate.
func (p *HaikuPM) Route(ctx context.Context, in call.RouteInput) (call.PMRouteResult, error) {
	userText := fmt.Sprintf("Objective: %s\n\nQuestion: %s", in.Objective.Goal, in.Question)
	if in.InterruptedText != "" {
		userText += fmt.Sprintf("\n\nNote: Freeman was interrupted saying %q when the agent asked this question.", in.InterruptedText)
	}

	resp, err := p.client.Messages.New(ctx, anthropic.MessageNewParams{
		Model:     anthropic.Model(p.cfg.Model),
		MaxTokens: 256,
		System: []anthropic.TextBlockParam{
			{Text: routerSystemPrompt},
		},
		Messages:   []anthropic.MessageParam{anthropic.NewUserMessage(anthropic.NewTextBlock(userText))},
		Tools:      routerTools(),
		ToolChoice: anthropic.ToolChoiceUnionParam{OfAny: &anthropic.ToolChoiceAnyParam{}},
	})
	if err != nil {
		// On error, escalate with raw question.
		return call.PMRouteResult{SpokenQuestion: in.Question}, nil
	}

	for _, block := range resp.Content {
		tu, ok := block.AsAny().(anthropic.ToolUseBlock)
		if !ok {
			continue
		}
		switch tu.Name {
		case "answer_inline":
			var args struct {
				Answer     string  `json:"answer"`
				Confidence float64 `json:"confidence"`
			}
			if err := json.Unmarshal(tu.Input, &args); err != nil {
				return call.PMRouteResult{SpokenQuestion: in.Question}, nil
			}
			if args.Confidence < p.cfg.ConfidenceThreshold {
				return call.PMRouteResult{SpokenQuestion: in.Question}, nil
			}
			return call.PMRouteResult{AnswerInline: args.Answer}, nil

		case "escalate":
			var args struct {
				SpokenQuestion string `json:"spoken_question"`
			}
			if err := json.Unmarshal(tu.Input, &args); err != nil {
				return call.PMRouteResult{SpokenQuestion: in.Question}, nil
			}
			return call.PMRouteResult{SpokenQuestion: args.SpokenQuestion}, nil
		}
	}

	return call.PMRouteResult{SpokenQuestion: in.Question}, nil
}

// intakeTools returns the two tools Haiku can call during intake.
func intakeTools() []anthropic.ToolUnionParam {
	return []anthropic.ToolUnionParam{
		{OfTool: &anthropic.ToolParam{
			Name:        "ask_followup",
			Description: anthropic.String("Ask the user one follow-up question to clarify their request."),
			InputSchema: anthropic.ToolInputSchemaParam{
				Properties: map[string]any{
					"question": map[string]any{
						"type":        "string",
						"description": "The follow-up question to ask the user.",
					},
				},
				Required: []string{"question"},
			},
		}},
		{OfTool: &anthropic.ToolParam{
			Name:        "complete_objective",
			Description: anthropic.String("Signal that you have enough information and provide the completed engineering objective."),
			InputSchema: anthropic.ToolInputSchemaParam{
				Properties: map[string]any{
					"goal": map[string]any{
						"type":        "string",
						"description": "A concise description of what to build.",
					},
					"acceptance_criteria": map[string]any{
						"type":        "array",
						"items":       map[string]any{"type": "string"},
						"description": "List of conditions that must be true for the task to be complete.",
					},
					"constraints": map[string]any{
						"type":        "array",
						"items":       map[string]any{"type": "string"},
						"description": "List of constraints or limitations to respect.",
					},
					"notes": map[string]any{
						"type":        "array",
						"items":       map[string]any{"type": "string"},
						"description": "Optional notes or context for the implementer.",
					},
					"model_hint": map[string]any{
						"type":        "string",
						"enum":        []string{"sonnet", "opus"},
						"description": "Use opus for cross-cutting refactors and architectural changes; sonnet for everything else.",
					},
					"spoken_summary": map[string]any{
						"type":        "string",
						"description": "A one-sentence summary suitable for text-to-speech. No markdown.",
					},
				},
				Required: []string{"goal", "acceptance_criteria", "constraints", "notes", "model_hint", "spoken_summary"},
			},
		}},
	}
}

// routerTools returns the two tools Haiku can call during routing.
func routerTools() []anthropic.ToolUnionParam {
	return []anthropic.ToolUnionParam{
		{OfTool: &anthropic.ToolParam{
			Name:        "answer_inline",
			Description: anthropic.String("Answer the agent's question directly without asking the user."),
			InputSchema: anthropic.ToolInputSchemaParam{
				Properties: map[string]any{
					"answer": map[string]any{
						"type":        "string",
						"description": "The direct answer to give the coding agent.",
					},
					"confidence": map[string]any{
						"type":        "number",
						"description": "Confidence in the answer (0.0–1.0). Use escalate if below 0.8.",
					},
				},
				Required: []string{"answer", "confidence"},
			},
		}},
		{OfTool: &anthropic.ToolParam{
			Name:        "escalate",
			Description: anthropic.String("Ask the user this question out loud because it requires their judgment."),
			InputSchema: anthropic.ToolInputSchemaParam{
				Properties: map[string]any{
					"spoken_question": map[string]any{
						"type":        "string",
						"description": "The question to speak aloud, phrased naturally for TTS. One sentence, no markdown.",
					},
				},
				Required: []string{"spoken_question"},
			},
		}},
	}
}
