package tools

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

// Run executes a tool Spec with the given JSON-encoded args and returns
// a Result suitable for serialisation back to the LLM. The bash runner
// passes each arg as an environment variable named ARG_<param>; the
// script can reference it with standard shell expansion, which avoids
// any injection risk from string interpolation.
func Run(ctx context.Context, spec Spec, args json.RawMessage) Result {
	switch spec.Runtime {
	case RuntimeShell:
		return runShell(ctx, spec, args)
	default:
		return Result{Ok: false, Error: "unsupported runtime: " + string(spec.Runtime)}
	}
}

func runShell(ctx context.Context, spec Spec, args json.RawMessage) Result {
	env, err := buildEnv(args)
	if err != nil {
		return Result{Ok: false, Error: err.Error()}
	}
	env = append(env, "UUID="+randomHex(8))
	env = append(env, fmt.Sprintf("TS_UNIX=%d", time.Now().Unix()))
	env = append(env, fmt.Sprintf("TS_MS=%d", time.Now().UnixMilli()))

	timeout := time.Duration(spec.TimeoutMS) * time.Millisecond
	cctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	cmd := exec.CommandContext(cctx, "bash", "-c", spec.Body)
	cmd.Env = append(append([]string{}, env...), passthroughEnv()...)

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err = cmd.Run()
	if cctx.Err() == context.DeadlineExceeded {
		return Result{Ok: false, Error: fmt.Sprintf("timeout after %dms", spec.TimeoutMS)}
	}
	if err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		return Result{Ok: false, Error: msg}
	}
	return Result{Ok: true, Output: strings.TrimSpace(stdout.String())}
}

// buildEnv marshals each top-level key of the args JSON into an env var
// named ARG_<key>. Non-string values are re-encoded as JSON so scripts
// can use `jq` or similar if they need structured data.
func buildEnv(args json.RawMessage) ([]string, error) {
	if len(args) == 0 {
		return nil, nil
	}
	var m map[string]interface{}
	if err := json.Unmarshal(args, &m); err != nil {
		return nil, fmt.Errorf("args not an object: %w", err)
	}
	out := make([]string, 0, len(m))
	for k, v := range m {
		out = append(out, "ARG_"+k+"="+stringifyArg(v))
	}
	return out, nil
}

func stringifyArg(v interface{}) string {
	switch t := v.(type) {
	case nil:
		return ""
	case string:
		return t
	case bool:
		if t {
			return "true"
		}
		return "false"
	case float64:
		if t == float64(int64(t)) {
			return fmt.Sprintf("%d", int64(t))
		}
		return fmt.Sprintf("%g", t)
	default:
		b, _ := json.Marshal(t)
		return string(b)
	}
}

func randomHex(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// passthroughEnv returns the environment variables a shell tool is
// allowed to inherit. We keep it minimal to avoid accidentally leaking
// secrets into tool output, but tools need PATH and HOME to be useful.
func passthroughEnv() []string {
	return []string{
		"PATH=" + envLookup("PATH"),
		"HOME=" + envLookup("HOME"),
		"LANG=" + envLookup("LANG"),
		"TMPDIR=" + envLookup("TMPDIR"),
	}
}

func envLookup(k string) string {
	// Separated so tests can override via a replacement package.
	return osGetenv(k)
}
