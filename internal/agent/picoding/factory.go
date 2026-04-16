package picoding

import (
	"strings"

	"github.com/Renderix/freeman/internal/agent"
)

// TaskAgentFactory creates PiCoding task agents for a given repo root.
type TaskAgentFactory struct {
	repoRoot     string
	defaultModel string
	opusModel    string
}

func NewTaskAgentFactory(repoRoot, defaultModel, opusModel string) *TaskAgentFactory {
	return &TaskAgentFactory{
		repoRoot:     repoRoot,
		defaultModel: defaultModel,
		opusModel:    opusModel,
	}
}

func (f *TaskAgentFactory) NewTaskAgent() agent.TaskAgent {
	return NewTaskAgent(f.repoRoot)
}

// ResolveModel maps a hint like "opus" to the configured full model ID.
// Falls back to defaultModel for unrecognized hints.
func (f *TaskAgentFactory) ResolveModel(hint string) string {
	if strings.EqualFold(hint, "opus") {
		return f.opusModel
	}
	return f.defaultModel
}
