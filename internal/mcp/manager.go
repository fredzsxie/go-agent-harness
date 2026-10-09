package mcp

import (
	"context"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"sync"

	"go-agent-harness/internal/logger"
	toolkit "go-agent-harness/internal/tool"
)

const maxToolNameLength = 64

var disallowedNameChars = regexp.MustCompile(`[^a-zA-Z0-9_-]`)

type Policy string

const (
	PolicyAllow   Policy = "allow"
	PolicyConfirm Policy = "confirm"
)

type Origin struct {
	Server string
	Tool   string
}

type Factory func() (*Client, error)

type Config struct {
	Registry   *toolkit.Registry
	Servers    map[string]Factory
	HostPolicy map[Origin]Policy
}

// Manager 保存已连接 Server 与模型工具名的 Host 权限策略。
type Manager struct {
	mu         sync.RWMutex
	registry   *toolkit.Registry
	servers    map[string]Factory
	hostPolicy map[Origin]Policy
	connected  map[string]*Client
	policies   map[string]Policy
}

func New(registry *toolkit.Registry) *Manager {
	return NewWithConfig(Config{
		Registry: registry,
		Servers:  mockServers(),
		HostPolicy: map[Origin]Policy{
			{Server: "docs", Tool: "search"}:      PolicyAllow,
			{Server: "docs", Tool: "get_version"}: PolicyAllow,
			{Server: "deploy", Tool: "status"}:    PolicyAllow,
			{Server: "deploy", Tool: "trigger"}:   PolicyConfirm,
		},
	})
}

func NewWithConfig(config Config) *Manager {
	return &Manager{
		registry:   config.Registry,
		servers:    cloneFactories(config.Servers),
		hostPolicy: clonePolicies(config.HostPolicy),
		connected:  make(map[string]*Client),
		policies:   make(map[string]Policy),
	}
}

// Connect 先完整校验 discovery 结果，再同步提交 Registry、Policy 与连接状态。
// s14 只发现并注册工具；下一轮 Worker 重新读取 Specs 才把新能力交给模型。
// Server annotations 不能自行授权，外部调用仍由 app 注入的 Host Policy 与审批 Hook 决定。
func (m *Manager) Connect(name string) (string, error) {
	if m == nil || m.registry == nil {
		return "", fmt.Errorf("MCP registry is required")
	}
	name = strings.TrimSpace(name)
	m.mu.Lock()
	defer m.mu.Unlock()

	if client := m.connected[name]; client != nil {
		return fmt.Sprintf("MCP server %q already connected", name), nil
	}
	factory := m.servers[name]
	if factory == nil {
		err := fmt.Errorf("unknown MCP server %q; available: %s", name, strings.Join(m.availableLocked(), ", "))
		logger.Error("[MCP] Connect failed: %v", err)
		return "", err
	}
	client, err := factory()
	if err != nil {
		logger.Error("[MCP] Connect %s failed: %v", name, err)
		return "", err
	}
	if client == nil {
		return "", fmt.Errorf("MCP server %q returned a nil client", name)
	}
	if client.Name() != name {
		return "", fmt.Errorf("MCP server name mismatch: requested %q, got %q", name, client.Name())
	}

	entries, policies, err := m.discoverLocked(client)
	if err != nil {
		logger.Error("[MCP] Discovery %s failed: %v", name, err)
		return "", err
	}
	// Register 本身不会失败；所有冲突和 Schema 已在此之前完成检查。
	for _, entry := range entries {
		m.registry.Register(entry.spec, entry.handler)
	}
	for toolName, policy := range policies {
		m.policies[toolName] = policy
	}
	m.connected[name] = client

	names := make([]string, len(entries))
	for i, entry := range entries {
		names[i] = entry.spec.Name
	}
	logger.Info("[MCP] Connected %s with %d tool(s): %s", name, len(names), strings.Join(names, ", "))
	return fmt.Sprintf("Connected to MCP server %q. Discovered %d tools: %s", name, len(names), strings.Join(names, ", ")), nil
}

func (m *Manager) RunConnect(_ context.Context, input any) (string, error) {
	args, ok := input.(map[string]any)
	if !ok {
		return "", fmt.Errorf("MCP connect input must be an object")
	}
	name, ok := args["name"].(string)
	if !ok || strings.TrimSpace(name) == "" {
		return "", fmt.Errorf("MCP server name is required")
	}
	return m.Connect(name)
}

func (m *Manager) Available() []string {
	if m == nil {
		return nil
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.availableLocked()
}

func (m *Manager) Connected() []string {
	if m == nil {
		return nil
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	names := make([]string, 0, len(m.connected))
	for name := range m.connected {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// Policy 仅返回 Host 配置；Server annotations 永远不能授予执行权限。
func (m *Manager) Policy(toolName string) Policy {
	if m == nil {
		return PolicyConfirm
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	if policy := m.policies[toolName]; policy == PolicyAllow {
		return PolicyAllow
	}
	return PolicyConfirm
}

type discoveredEntry struct {
	spec    toolkit.Spec
	handler toolkit.Handler
}

func (m *Manager) discoverLocked(client *Client) ([]discoveredEntry, map[string]Policy, error) {
	origins := make(map[string]string)
	for _, spec := range m.registry.Specs() {
		origins[spec.Name] = fmt.Sprintf("built-in or registered tool %q", spec.Name)
	}

	tools := client.Tools()
	entries := make([]discoveredEntry, 0, len(tools))
	policies := make(map[string]Policy, len(tools))
	serverName, err := NormalizeName(client.Name())
	if err != nil {
		return nil, nil, err
	}
	for _, tool := range tools {
		toolName, err := NormalizeName(tool.Name)
		if err != nil {
			return nil, nil, err
		}
		modelName := "mcp__" + serverName + "__" + toolName
		if len(modelName) > maxToolNameLength {
			return nil, nil, fmt.Errorf("MCP tool name is longer than %d characters: %s", maxToolNameLength, modelName)
		}
		origin := fmt.Sprintf("MCP tool %q/%q", client.Name(), tool.Name)
		if previous := origins[modelName]; previous != "" {
			return nil, nil, fmt.Errorf("MCP tool name collision after normalization: %q maps both %s and %s", modelName, previous, origin)
		}
		spec, err := toolSpec(modelName, tool, origin)
		if err != nil {
			return nil, nil, err
		}
		rawName := tool.Name
		entries = append(entries, discoveredEntry{
			spec: spec,
			handler: func(ctx context.Context, input any) (string, error) {
				args, ok := input.(map[string]any)
				if !ok {
					return "", fmt.Errorf("MCP tool input must be an object")
				}
				logger.Debug("[MCP] Calling %s/%s", client.Name(), rawName)
				output, err := client.CallTool(ctx, rawName, args)
				if err != nil {
					logger.Error("[MCP] Call %s/%s failed: %v", client.Name(), rawName, err)
				} else {
					logger.Info("[MCP] Called %s/%s", client.Name(), rawName)
				}
				return output, err
			},
		})
		origins[modelName] = origin
		policies[modelName] = m.hostPolicy[Origin{Server: client.Name(), Tool: tool.Name}]
	}
	return entries, policies, nil
}

func toolSpec(modelName string, tool Tool, origin string) (toolkit.Spec, error) {
	schema := tool.InputSchema
	if schema == nil {
		schema = map[string]any{"type": "object", "properties": map[string]any{}}
	}
	if schemaType, _ := schema["type"].(string); schemaType != "" && schemaType != "object" {
		return toolkit.Spec{}, fmt.Errorf("invalid input schema for %s: type must be object", origin)
	}
	properties, ok := schema["properties"].(map[string]any)
	if !ok && schema["properties"] != nil {
		return toolkit.Spec{}, fmt.Errorf("invalid input schema for %s: properties must be an object", origin)
	}
	required, err := stringSlice(schema["required"])
	if err != nil {
		return toolkit.Spec{}, fmt.Errorf("invalid input schema for %s: %w", origin, err)
	}
	return toolkit.Spec{
		Name: modelName, Description: tool.Description,
		Properties: cloneMap(properties), Required: required,
	}, nil
}

func stringSlice(value any) ([]string, error) {
	if value == nil {
		return nil, nil
	}
	switch items := value.(type) {
	case []string:
		return append([]string(nil), items...), nil
	case []any:
		result := make([]string, len(items))
		for i, item := range items {
			text, ok := item.(string)
			if !ok || strings.TrimSpace(text) == "" {
				return nil, fmt.Errorf("required must contain non-empty strings")
			}
			result[i] = text
		}
		return result, nil
	default:
		return nil, fmt.Errorf("required must be an array")
	}
}

func NormalizeName(name string) (string, error) {
	normalized := disallowedNameChars.ReplaceAllString(strings.TrimSpace(name), "_")
	if normalized == "" {
		return "", fmt.Errorf("MCP names cannot normalize to an empty string")
	}
	return normalized, nil
}

func (m *Manager) availableLocked() []string {
	names := make([]string, 0, len(m.servers))
	for name := range m.servers {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func cloneFactories(input map[string]Factory) map[string]Factory {
	cloned := make(map[string]Factory, len(input))
	for name, factory := range input {
		cloned[name] = factory
	}
	return cloned
}

func clonePolicies(input map[Origin]Policy) map[Origin]Policy {
	cloned := make(map[Origin]Policy, len(input))
	for origin, policy := range input {
		cloned[origin] = policy
	}
	return cloned
}
