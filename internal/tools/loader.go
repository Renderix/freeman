package tools

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// frontmatter is the YAML block at the top of a tool MD file. Parameters
// is parsed as a generic map so it can be re-encoded as JSON Schema.
type frontmatter struct {
	Name        string      `yaml:"name"`
	Description string      `yaml:"description"`
	Runtime     string      `yaml:"runtime"`
	Parameters  interface{} `yaml:"parameters"`
	TimeoutMS   int         `yaml:"timeout_ms"`
}

// LoadDirs scans the given directories in order; later directories
// override earlier ones by tool name. Missing directories are silently
// skipped.
func LoadDirs(dirs []string) ([]Spec, error) {
	byName := make(map[string]Spec)
	for _, dir := range dirs {
		entries, err := os.ReadDir(dir)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return nil, fmt.Errorf("tools: read %s: %w", dir, err)
		}
		for _, e := range entries {
			if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
				continue
			}
			path := filepath.Join(dir, e.Name())
			spec, err := loadFile(path)
			if err != nil {
				return nil, fmt.Errorf("tools: %s: %w", path, err)
			}
			spec.SourceDir = dir
			byName[spec.Name] = spec
		}
	}
	out := make([]Spec, 0, len(byName))
	for _, s := range byName {
		out = append(out, s)
	}
	return out, nil
}

func loadFile(path string) (Spec, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return Spec{}, err
	}
	fmRaw, body, err := splitFrontmatter(raw)
	if err != nil {
		return Spec{}, err
	}
	var fm frontmatter
	if err := yaml.Unmarshal(fmRaw, &fm); err != nil {
		return Spec{}, fmt.Errorf("frontmatter yaml: %w", err)
	}
	if fm.Name == "" {
		return Spec{}, fmt.Errorf("missing name")
	}
	if fm.Description == "" {
		return Spec{}, fmt.Errorf("missing description")
	}
	if fm.Runtime == "" {
		fm.Runtime = "shell"
	}
	if fm.Runtime != string(RuntimeShell) {
		return Spec{}, fmt.Errorf("unsupported runtime: %s", fm.Runtime)
	}
	params, err := paramsToJSONSchema(fm.Parameters)
	if err != nil {
		return Spec{}, fmt.Errorf("parameters: %w", err)
	}
	timeout := fm.TimeoutMS
	if timeout <= 0 {
		timeout = 10000
	}
	if timeout > 60000 {
		timeout = 60000
	}
	return Spec{
		Name:        fm.Name,
		Description: fm.Description,
		Parameters:  params,
		Runtime:     Runtime(fm.Runtime),
		Body:        strings.TrimSpace(body),
		TimeoutMS:   timeout,
	}, nil
}

// splitFrontmatter expects the file to begin with a line "---", a YAML
// block, a closing "---" line, and then the body.
func splitFrontmatter(raw []byte) ([]byte, string, error) {
	text := string(raw)
	// Strip BOM if present.
	text = strings.TrimPrefix(text, "\uFEFF")
	if !strings.HasPrefix(text, "---\n") && !strings.HasPrefix(text, "---\r\n") {
		return nil, "", fmt.Errorf("missing opening --- frontmatter marker")
	}
	// Find closing marker.
	lines := strings.Split(text, "\n")
	end := -1
	for i := 1; i < len(lines); i++ {
		if strings.TrimRight(lines[i], "\r") == "---" {
			end = i
			break
		}
	}
	if end < 0 {
		return nil, "", fmt.Errorf("missing closing --- frontmatter marker")
	}
	fm := strings.Join(lines[1:end], "\n")
	body := strings.Join(lines[end+1:], "\n")
	return []byte(fm), body, nil
}

// paramsToJSONSchema re-encodes the YAML-parsed parameters block as JSON.
// If no parameters are declared, falls back to the empty object schema.
func paramsToJSONSchema(v interface{}) (json.RawMessage, error) {
	if v == nil {
		return json.RawMessage(`{"type":"object","properties":{}}`), nil
	}
	// YAML unmarshals into map[interface{}]interface{}; convert recursively
	// so encoding/json can handle it.
	norm := normalizeYAML(v)
	m, ok := norm.(map[string]interface{})
	if !ok {
		return nil, fmt.Errorf("parameters must be an object")
	}
	if _, hasType := m["type"]; !hasType {
		m["type"] = "object"
	}
	b, err := json.Marshal(m)
	if err != nil {
		return nil, err
	}
	return b, nil
}

func normalizeYAML(v interface{}) interface{} {
	switch t := v.(type) {
	case map[interface{}]interface{}:
		out := make(map[string]interface{}, len(t))
		for k, val := range t {
			out[fmt.Sprint(k)] = normalizeYAML(val)
		}
		return out
	case map[string]interface{}:
		out := make(map[string]interface{}, len(t))
		for k, val := range t {
			out[k] = normalizeYAML(val)
		}
		return out
	case []interface{}:
		out := make([]interface{}, len(t))
		for i, e := range t {
			out[i] = normalizeYAML(e)
		}
		return out
	default:
		return v
	}
}
