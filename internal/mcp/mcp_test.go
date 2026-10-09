package mcp

import (
	"context"
	"errors"
	"strings"
	"testing"

	"go-agent-harness/internal/tool"
)

func TestClientValidatesDiscoveryAndContainsCallErrors(t *testing.T) {
	client := NewClient("test")
	if err := client.Register([]Tool{{Name: "same"}, {Name: "same"}}, map[string]Handler{"same": echoHandler}); err == nil {
		t.Fatal("duplicate tool names should fail")
	}
	if err := client.Register([]Tool{{Name: "missing"}}, nil); err == nil {
		t.Fatal("missing handler should fail")
	}
	if err := client.Register([]Tool{{Name: "fail"}}, map[string]Handler{
		"fail": func(context.Context, map[string]any) (string, error) { return "", errors.New("boom") },
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := client.CallTool(context.Background(), "unknown", nil); err == nil || !strings.Contains(err.Error(), "MCP error") {
		t.Fatalf("unknown call should remain at MCP boundary: %v", err)
	}
	if _, err := client.CallTool(context.Background(), "fail", nil); err == nil || err.Error() != "MCP error: boom" {
		t.Fatalf("handler error should remain at MCP boundary: %v", err)
	}
}

func TestManagerConnectsAndDispatchesMockTools(t *testing.T) {
	registry := tool.NewRegistry()
	registry.Register(tool.Spec{Name: "bash"}, echoAgentHandler)
	manager := New(registry)

	result, err := manager.Connect("docs")
	if err != nil || !strings.Contains(result, "mcp__docs__search") {
		t.Fatalf("connect failed: %q, %v", result, err)
	}
	if got := manager.Connected(); len(got) != 1 || got[0] != "docs" {
		t.Fatalf("unexpected connected servers: %v", got)
	}
	output, err := registry.Dispatch(context.Background(), "mcp__docs__search", map[string]any{"query": "agent hooks"})
	if err != nil || output != `[docs] Found 3 results for "agent hooks"` {
		t.Fatalf("unexpected MCP output: %q, %v", output, err)
	}
	if manager.Policy("mcp__docs__search") != PolicyAllow {
		t.Fatal("known read-only tool should follow host allow policy")
	}
	if manager.Policy("mcp__unknown__read") != PolicyConfirm {
		t.Fatal("unknown external tools must require confirmation")
	}
	if _, err := registry.Dispatch(context.Background(), "mcp__docs__search", map[string]any{}); err == nil || !strings.Contains(err.Error(), "query is required") {
		t.Fatalf("missing input should return an MCP tool error: %v", err)
	}
	if _, err := registry.Dispatch(context.Background(), "mcp__docs__get_version", map[string]any{"extra": true}); err == nil || !strings.Contains(err.Error(), "unexpected argument") {
		t.Fatalf("unexpected input should return an MCP tool error: %v", err)
	}

	again, err := manager.Connect("docs")
	if err != nil || !strings.Contains(again, "already connected") {
		t.Fatalf("duplicate connect should be idempotent: %q, %v", again, err)
	}
}

func TestManagerRejectsUnknownServerWithoutMutation(t *testing.T) {
	registry := tool.NewRegistry()
	manager := New(registry)
	if _, err := manager.Connect("missing"); err == nil {
		t.Fatal("unknown server should fail")
	}
	if len(manager.Connected()) != 0 || len(registry.Specs()) != 0 {
		t.Fatal("failed connection must not mutate state")
	}
}

func TestManagerRejectsInvalidSchemaBeforeRegisteringAnyTool(t *testing.T) {
	registry := tool.NewRegistry()
	manager := NewWithConfig(Config{Registry: registry, Servers: map[string]Factory{
		"broken": serverFactory("broken", []Tool{
			{Name: "valid", InputSchema: objectSchema()},
			{Name: "invalid", InputSchema: map[string]any{"type": "array"}},
		}),
	}})
	if _, err := manager.Connect("broken"); err == nil || !strings.Contains(err.Error(), "type must be object") {
		t.Fatalf("invalid schema should fail: %v", err)
	}
	if len(manager.Connected()) != 0 || len(registry.Specs()) != 0 {
		t.Fatal("discovery failure must not partially register tools")
	}
}

func TestManagerRejectsNormalizedCollisionAndLongName(t *testing.T) {
	registry := tool.NewRegistry()
	manager := NewWithConfig(Config{Registry: registry, Servers: map[string]Factory{
		"docs.one": serverFactory("docs.one", []Tool{{Name: "get.version", InputSchema: objectSchema()}}),
		"docs_one": serverFactory("docs_one", []Tool{{Name: "get_version", InputSchema: objectSchema()}}),
		"long":     serverFactory("long", []Tool{{Name: strings.Repeat("a", 60), InputSchema: objectSchema()}}),
	}})
	if _, err := manager.Connect("docs.one"); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.Connect("docs_one"); err == nil || !strings.Contains(err.Error(), "collision") {
		t.Fatalf("normalized collision should fail: %v", err)
	}
	if _, err := manager.Connect("long"); err == nil || !strings.Contains(err.Error(), "longer than 64") {
		t.Fatalf("long model tool name should fail: %v", err)
	}
	if got := manager.Connected(); len(got) != 1 || got[0] != "docs.one" {
		t.Fatalf("failed connections changed state: %v", got)
	}
}

func TestServerAnnotationsDoNotGrantPermission(t *testing.T) {
	registry := tool.NewRegistry()
	manager := NewWithConfig(Config{Registry: registry, Servers: map[string]Factory{
		"external": func() (*Client, error) {
			client := NewClient("external")
			err := client.Register([]Tool{{
				Name: "read", InputSchema: objectSchema(), Annotations: map[string]any{"readOnlyHint": true},
			}}, map[string]Handler{"read": echoHandler})
			return client, err
		},
	}})
	if _, err := manager.Connect("external"); err != nil {
		t.Fatal(err)
	}
	if manager.Policy("mcp__external__read") != PolicyConfirm {
		t.Fatal("server annotations must not grant host permission")
	}
}

func serverFactory(name string, tools []Tool) Factory {
	return func() (*Client, error) {
		client := NewClient(name)
		handlers := make(map[string]Handler, len(tools))
		for _, tool := range tools {
			handlers[tool.Name] = echoHandler
		}
		return client, client.Register(tools, handlers)
	}
}

func echoHandler(context.Context, map[string]any) (string, error) {
	return "ok", nil
}

func echoAgentHandler(context.Context, any) (string, error) {
	return "ok", nil
}
