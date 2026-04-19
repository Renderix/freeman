package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadAndRunShellTool(t *testing.T) {
	dir := t.TempDir()
	md := `---
name: echo_num
description: Echo the given number plus one.
runtime: shell
timeout_ms: 2000
parameters:
  type: object
  properties:
    n:
      type: integer
      description: number
  required: [n]
---
set -euo pipefail
echo $((ARG_n + 1))
`
	if err := os.WriteFile(filepath.Join(dir, "echo_num.md"), []byte(md), 0644); err != nil {
		t.Fatal(err)
	}
	specs, err := LoadDirs([]string{dir})
	if err != nil {
		t.Fatalf("LoadDirs: %v", err)
	}
	if len(specs) != 1 || specs[0].Name != "echo_num" {
		t.Fatalf("unexpected specs: %+v", specs)
	}
	// Parameters must be valid JSON Schema (object type).
	var parsed map[string]any
	if err := json.Unmarshal(specs[0].Parameters, &parsed); err != nil {
		t.Fatalf("parameters json: %v", err)
	}
	if parsed["type"] != "object" {
		t.Fatalf("expected object schema, got %+v", parsed)
	}

	reg := NewRegistry(specs)
	if !reg.Has("echo_num") {
		t.Fatal("registry missing echo_num")
	}

	res := reg.Run(context.Background(), "echo_num", json.RawMessage(`{"n":41}`))
	if !res.Ok {
		t.Fatalf("tool run failed: %+v", res)
	}
	if strings.TrimSpace(res.Output) != "42" {
		t.Fatalf("expected 42, got %q", res.Output)
	}
}

func TestLoadFrontmatterErrors(t *testing.T) {
	dir := t.TempDir()
	cases := map[string]string{
		"no_frontmatter.md": "just a body\n",
		"no_name.md":        "---\ndescription: x\n---\nbody\n",
		"no_desc.md":        "---\nname: x\n---\nbody\n",
	}
	for name, body := range cases {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0644); err != nil {
			t.Fatal(err)
		}
		if _, err := LoadDirs([]string{dir}); err == nil {
			t.Fatalf("%s: expected error", name)
		}
		// Remove so each iteration tests one bad file in isolation.
		_ = os.Remove(filepath.Join(dir, name))
	}
}

func TestLaterDirOverrides(t *testing.T) {
	d1, d2 := t.TempDir(), t.TempDir()
	write := func(dir, body string) {
		if err := os.WriteFile(filepath.Join(dir, "t.md"), []byte(body), 0644); err != nil {
			t.Fatal(err)
		}
	}
	write(d1, "---\nname: t\ndescription: v1\nruntime: shell\n---\necho v1\n")
	write(d2, "---\nname: t\ndescription: v2\nruntime: shell\n---\necho v2\n")
	specs, err := LoadDirs([]string{d1, d2})
	if err != nil {
		t.Fatal(err)
	}
	if len(specs) != 1 || specs[0].Description != "v2" {
		t.Fatalf("expected v2 override, got %+v", specs)
	}
}
