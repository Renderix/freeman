//go:build smoke

package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"testing"
)

// Run with: go test -tags=smoke -v ./internal/tools/... -run TestSmokeAllTools
func TestSmokeAllTools(t *testing.T) {
	repoRoot := "/Users/ayusman/hale/freeman"
	specs, err := LoadDirs([]string{filepath.Join(repoRoot, "tools")})
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("loaded %d tools", len(specs))
	reg := NewRegistry(specs)

	cases := []struct{ name, args string }{
		{"system_stats", `{}`},
		{"active_window", `{}`},
		{"clipboard_read", `{}`},
		{"file_search", `{"query":"README","limit":3}`},
		{"screenshot", `{"display":1}`},
		{"web_search", `{"query":"weather bangalore today"}`},
		{"read_file", `{"path":"/Users/ayusman/hale/freeman/README.md","max_bytes":300}`},
		{"web_fetch", `{"url":"https://example.com","max_bytes":500}`},
	}
	for _, c := range cases {
		res := reg.Run(context.Background(), c.name, json.RawMessage(c.args))
		b, _ := json.MarshalIndent(res, "", "  ")
		fmt.Printf("\n--- %s ---\n%s\n", c.name, string(b))
	}
}
