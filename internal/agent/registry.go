// Registry 维护工具定义与 Handler，为主 Agent 和 Subagent 提供统一调度入口。
package agent

import (
	"context"
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

	args := NormalizeToolInput(input)
	return entry.handler(ctx, args)
}

// Select 复制指定工具，供 Teammate 等受限 Runtime 组装最小工具集。
func (r *Registry) Select(names ...string) (*Registry, error) {
	selected := NewRegistry()
	for _, name := range names {
		entry, ok := r.entries[name]
		if !ok {
			return nil, fmt.Errorf("tool is not registered: %s", name)
		}
		selected.Register(entry.spec, entry.handler)
	}
	return selected, nil
}

// Rebind 保留工具 schema，仅替换与当前 Runtime 绑定的 Handler。
func (r *Registry) Rebind(name string, handler Handler) error {
	entry, ok := r.entries[name]
	if !ok {
		return fmt.Errorf("tool is not registered: %s", name)
	}
	if handler == nil {
		return fmt.Errorf("tool handler is required: %s", name)
	}
	entry.handler = handler
	r.entries[name] = entry
	return nil
}
