package conv

import (
	"bufio"
	"os"
	"path/filepath"
	"strings"
)

// Read returns a short static blurb describing the project at cwd,
// suitable for injection into the chat LLM's system prompt. It reads
// the first ~50 lines of README, the first ~30 lines of common
// manifest files (go.mod, package.json, pyproject.toml, Cargo.toml),
// and a one-level top directory listing. Total output is capped at
// roughly 4 KB so we don't blow the conv-sidecar prompt budget.
func Read(cwd string) string {
	var sb strings.Builder

	if readme := findFirst(cwd, []string{"README.md", "README.MD", "README.txt", "README"}); readme != "" {
		sb.WriteString("README (")
		sb.WriteString(filepath.Base(readme))
		sb.WriteString("):\n")
		appendFirstLines(&sb, readme, 50)
		sb.WriteString("\n")
	}

	for _, manifest := range []string{"go.mod", "package.json", "pyproject.toml", "Cargo.toml"} {
		p := filepath.Join(cwd, manifest)
		if _, err := os.Stat(p); err == nil {
			sb.WriteString(manifest)
			sb.WriteString(":\n")
			appendFirstLines(&sb, p, 30)
			sb.WriteString("\n")
		}
	}

	if entries, err := os.ReadDir(cwd); err == nil {
		sb.WriteString("Top-level entries:\n")
		count := 0
		for _, e := range entries {
			name := e.Name()
			if strings.HasPrefix(name, ".") {
				continue
			}
			if e.IsDir() {
				sb.WriteString("- ")
				sb.WriteString(name)
				sb.WriteString("/\n")
			} else {
				sb.WriteString("- ")
				sb.WriteString(name)
				sb.WriteString("\n")
			}
			count++
			if count >= 40 {
				break
			}
		}
	}

	out := sb.String()
	const cap = 4096
	if len(out) > cap {
		out = out[:cap] + "\n…(truncated)\n"
	}
	return out
}

func findFirst(dir string, names []string) string {
	for _, n := range names {
		p := filepath.Join(dir, n)
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	return ""
}

func appendFirstLines(sb *strings.Builder, path string, n int) {
	f, err := os.Open(path)
	if err != nil {
		return
	}
	defer f.Close()
	s := bufio.NewScanner(f)
	s.Buffer(make([]byte, 64*1024), 1024*1024)
	for i := 0; i < n && s.Scan(); i++ {
		sb.WriteString(s.Text())
		sb.WriteString("\n")
	}
}
