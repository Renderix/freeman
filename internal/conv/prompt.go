package conv

import (
	_ "embed"
	"strings"

	"github.com/Renderix/freeman/internal/config"
)

// systemPromptTemplate is the baseline voice-assistant prompt. Embedded
// at build time so the binary carries everything it needs — no runtime
// file lookup, no knob in config.yaml. Personality is customised purely
// through config.yaml fields substituted into placeholders below.
//
//go:embed system_prompt.md
var systemPromptTemplate string

// BuildSystemPrompt renders the embedded prompt template for a given
// persona. Substitutions:
//
//   - {{name}}  → persona.Name (e.g. "Horus")
//   - {{rules}} → persona.Rules rendered as a "## Additional rules"
//     markdown section, or stripped entirely if Rules is empty so we
//     don't leave a dangling header in the prompt.
func BuildSystemPrompt(p config.PersonaConfig) string {
	out := strings.ReplaceAll(systemPromptTemplate, "{{name}}", p.Name)

	var rulesBlock string
	if len(p.Rules) > 0 {
		var b strings.Builder
		b.WriteString("\n## Additional rules\n")
		for _, r := range p.Rules {
			r = strings.TrimSpace(r)
			if r == "" {
				continue
			}
			b.WriteString("- ")
			b.WriteString(r)
			b.WriteString("\n")
		}
		rulesBlock = b.String()
	}
	out = strings.ReplaceAll(out, "{{rules}}", rulesBlock)
	return strings.TrimSpace(out)
}
