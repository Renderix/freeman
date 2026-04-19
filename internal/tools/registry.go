package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
)

// Registry holds loaded tool specs and executes them by name. Safe for
// concurrent use; specs are loaded once at construction time.
type Registry struct {
	mu    sync.RWMutex
	specs map[string]Spec
}

func NewRegistry(specs []Spec) *Registry {
	m := make(map[string]Spec, len(specs))
	for _, s := range specs {
		m[s.Name] = s
	}
	return &Registry{specs: m}
}

func (r *Registry) Specs() []Spec {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]Spec, 0, len(r.specs))
	for _, s := range r.specs {
		out = append(out, s)
	}
	return out
}

// Has reports whether a tool with the given name is registered.
func (r *Registry) Has(name string) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	_, ok := r.specs[name]
	return ok
}

// Run executes the named tool with JSON-encoded args. If the tool is not
// registered, returns a Result with Ok=false.
func (r *Registry) Run(ctx context.Context, name string, args json.RawMessage) Result {
	r.mu.RLock()
	spec, ok := r.specs[name]
	r.mu.RUnlock()
	if !ok {
		return Result{Ok: false, Error: fmt.Sprintf("unknown tool: %s", name)}
	}
	return Run(ctx, spec, args)
}
