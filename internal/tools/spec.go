// Package tools loads provider-agnostic tool definitions from Markdown
// files and exposes a registry used by the conversation layer.
//
// A tool is declared in an MD file: YAML frontmatter describes the name,
// description, JSON Schema parameters, and runtime; the body is the
// implementation (a shell script for runtime=shell). The JSON Schema is
// passed through to the LLM provider as-is, so tools work identically
// across Anthropic, OpenAI, local OpenAI-compatible endpoints, etc.
package tools

import "encoding/json"

type Runtime string

const (
	RuntimeShell Runtime = "shell"
)

// Spec is the provider-agnostic description of a tool. The Parameters
// field is JSON Schema (object type) and is sent verbatim to the LLM
// provider.
type Spec struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Parameters  json.RawMessage `json:"parameters"`

	Runtime   Runtime `json:"-"`
	Body      string  `json:"-"`
	TimeoutMS int     `json:"-"`
	SourceDir string  `json:"-"`
}

// Result is what a tool returns to the model. Serialised as JSON and
// handed back via the existing tool_result channel.
type Result struct {
	Ok     bool   `json:"ok"`
	Output string `json:"output,omitempty"`
	Error  string `json:"error,omitempty"`
}
