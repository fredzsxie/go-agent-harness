// Package mcp 提供进程内 MCP 工具发现与调用边界。
package mcp

import (
	"context"
	"fmt"
	"strings"
)

// Tool 描述 MCP Server 通过 tools/list 暴露的原始工具定义。
type Tool struct {
	Name        string
	Description string
	InputSchema map[string]any // 输入参数的JSON Schema (type, properties?, required?)
	Annotations map[string]any
}

// Handler 模拟 MCP Server 的 tools/call 处理函数。
type Handler func(context.Context, map[string]any) (string, error)

// Client 是进程内 MCP Client，用于保存一次 discovery 的工具及 Handler。
type Client struct {
	name     string
	tools    []Tool
	handlers map[string]Handler
}

func NewClient(name string) *Client {
	return &Client{name: strings.TrimSpace(name)}
}

func (c *Client) Name() string {
	if c == nil {
		return ""
	}
	return c.name
}

// Register 校验 discovery 结果后整体替换，避免无 Handler 的工具进入工具池。
func (c *Client) Register(tools []Tool, handlers map[string]Handler) error {
	if c == nil {
		return fmt.Errorf("MCP client is nil")
	}
	seen := make(map[string]struct{}, len(tools))
	for _, tool := range tools {
		name := strings.TrimSpace(tool.Name)
		if name == "" {
			return fmt.Errorf("every MCP tool needs a non-empty name")
		}
		if _, exists := seen[name]; exists {
			return fmt.Errorf("duplicate MCP tool name on server %q: %s", c.name, name)
		}
		if handlers[name] == nil {
			return fmt.Errorf("missing MCP handler: %s", name)
		}
		seen[name] = struct{}{}
	}
	c.tools = cloneTools(tools)
	c.handlers = cloneHandlers(handlers)
	return nil
}

func (c *Client) Tools() []Tool {
	if c == nil {
		return nil
	}
	return cloneTools(c.tools)
}

// CallTool 保留 Server 的原始工具名，并把输入错误限制在 MCP 调用边界内。
func (c *Client) CallTool(ctx context.Context, name string, input map[string]any) (string, error) {
	if c == nil {
		return "", fmt.Errorf("MCP error: client is nil")
	}
	handler := c.handlers[name]
	if handler == nil {
		return "", fmt.Errorf("MCP error: unknown tool %q", name)
	}
	output, err := handler(ctx, input)
	if err != nil {
		return "", fmt.Errorf("MCP error: %w", err)
	}
	return output, nil
}

func cloneTools(tools []Tool) []Tool {
	cloned := make([]Tool, len(tools))
	for i, tool := range tools {
		cloned[i] = tool
		cloned[i].InputSchema = cloneMap(tool.InputSchema)
		cloned[i].Annotations = cloneMap(tool.Annotations)
	}
	return cloned
}

func cloneHandlers(handlers map[string]Handler) map[string]Handler {
	cloned := make(map[string]Handler, len(handlers))
	for name, handler := range handlers {
		cloned[name] = handler
	}
	return cloned
}

func cloneMap(input map[string]any) map[string]any {
	if input == nil {
		return nil
	}
	cloned := make(map[string]any, len(input))
	for key, value := range input {
		cloned[key] = value
	}
	return cloned
}
