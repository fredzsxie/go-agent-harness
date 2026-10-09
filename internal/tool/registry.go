package tool

import (
	"context"
	"fmt"
	"sync"

	"go-agent-harness/internal/protocol"
)

type toolEntry struct {
	spec    Spec
	handler Handler
}

// Registry 把 s02 的 schema 和执行函数保存在同一条记录中。
// s14 可在运行中注册 MCP 工具；s11 后台调用也读取此表，因此同步边界属于 Registry 本身。
type Registry struct {
	mu      sync.RWMutex
	entries map[string]toolEntry
	order   []string
}

func NewRegistry() *Registry { return &Registry{entries: make(map[string]toolEntry)} }

// Register 按首次注册顺序展示工具；同名注册替换定义和执行函数。
func (r *Registry) Register(spec Spec, handler Handler) {
	if spec.Name == "" {
		panic("tool name is required")
	}
	if handler == nil {
		panic("tool handler is required: " + spec.Name)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.entries[spec.Name]; !exists {
		r.order = append(r.order, spec.Name)
	}
	r.entries[spec.Name] = toolEntry{spec: cloneSpec(spec), handler: handler}
}

// Specs 返回独立快照，模型请求或调用方修改 schema 都不会改变注册表。
func (r *Registry) Specs() []Spec {
	r.mu.RLock()
	defer r.mu.RUnlock()
	specs := make([]Spec, 0, len(r.order))
	for _, name := range r.order {
		specs = append(specs, cloneSpec(r.entries[name].spec))
	}
	return specs
}

func (r *Registry) Dispatch(ctx context.Context, name string, input any) (string, error) {
	r.mu.RLock()
	entry, ok := r.entries[name]
	r.mu.RUnlock()
	if !ok {
		return "", fmt.Errorf("unknown tool: %s", name)
	}
	// 只在查表时持锁；connect_mcp 的 Handler 会再次 Register，持锁执行会导致死锁。
	return entry.handler(ctx, normalizeInput(input))
}

// Select 复制受限工具集合，供 s06 Subagent 和 s13 Teammate 独立绑定自己的执行上下文。
func (r *Registry) Select(names ...string) (*Registry, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
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

// Rebind 保留工具 schema，只替换当前 Runtime 使用的 Handler。
func (r *Registry) Rebind(name string, handler Handler) error {
	if handler == nil {
		return fmt.Errorf("tool handler is required: %s", name)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	entry, ok := r.entries[name]
	if !ok {
		return fmt.Errorf("tool is not registered: %s", name)
	}
	entry.handler = handler
	r.entries[name] = entry
	return nil
}

func cloneSpec(spec Spec) Spec {
	spec.Required = append([]string(nil), spec.Required...)
	spec.Properties = protocol.CloneMap(spec.Properties)
	return spec
}
