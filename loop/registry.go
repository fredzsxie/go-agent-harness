package loop

import (
	"context"
	"encoding/json"
	"fmt"
)

type Handler func(ctx context.Context, input any) (string, error)

type toolEntry struct {
	spec    ToolSpec
	handler Handler
}

type Registry struct {
	entries map[string]toolEntry
	order   []string
}

func NewRegistry() *Registry {
	return &Registry{entries: make(map[string]toolEntry)}
}

func (r *Registry) Register(spec ToolSpec, handler Handler) {
	if spec.Name == "" {
		panic("tool name is required")
	}
	if _, exists := r.entries[spec.Name]; !exists {
		r.order = append(r.order, spec.Name)
	}
	r.entries[spec.Name] = toolEntry{spec: spec, handler: handler}
}

func (r *Registry) Specs() []ToolSpec {
	specs := make([]ToolSpec, 0, len(r.order))
	for _, name := range r.order {
		specs = append(specs, r.entries[name].spec)
	}
	return specs
}

func (r *Registry) Dispatch(ctx context.Context, name string, input any) (string, error) {
	entry, ok := r.entries[name]
	if !ok {
		return "", fmt.Errorf("unknown tool: %s", name)
	}

	args := parseToolInput(input)
	return entry.handler(ctx, args)
}

func parseToolInput(input any) map[string]any {
	if input == nil {
		return map[string]any{}
	}
	if m, ok := input.(map[string]any); ok {
		return m
	}
	if raw, err := json.Marshal(input); err == nil {
		var parsed map[string]any
		if json.Unmarshal(raw, &parsed) == nil {
			return parsed
		}
	}
	return map[string]any{"command": fmt.Sprint(input)}
}
