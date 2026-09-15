package mcp

import (
	"context"
	"fmt"
	"strings"
)

func mockServers() map[string]Factory {
	return map[string]Factory{
		"docs":   newDocsServer,
		"deploy": newDeployServer,
	}
}

func newDocsServer() (*Client, error) {
	client := NewClient("docs")
	err := client.Register([]Tool{
		{Name: "search", Description: "Search the documentation.", InputSchema: objectSchema("query"), Annotations: map[string]any{"readOnlyHint": true}},
		{Name: "get_version", Description: "Get the documentation API version.", InputSchema: objectSchema(), Annotations: map[string]any{"readOnlyHint": true}},
	}, map[string]Handler{
		"search": func(_ context.Context, input map[string]any) (string, error) {
			query, err := requiredString(input, "query")
			if err != nil {
				return "", err
			}
			return fmt.Sprintf("[docs] Found 3 results for %q", query), nil
		},
		"get_version": func(context.Context, map[string]any) (string, error) {
			return "[docs] API v2.1.0", nil
		},
	})
	return client, err
}

func newDeployServer() (*Client, error) {
	client := NewClient("deploy")
	err := client.Register([]Tool{
		{Name: "trigger", Description: "Trigger a deployment.", InputSchema: objectSchema("service"), Annotations: map[string]any{"destructiveHint": true}},
		{Name: "status", Description: "Check deployment status.", InputSchema: objectSchema("service"), Annotations: map[string]any{"readOnlyHint": true}},
	}, map[string]Handler{
		"trigger": func(_ context.Context, input map[string]any) (string, error) {
			service, err := requiredString(input, "service")
			if err != nil {
				return "", err
			}
			return "[deploy] Triggered: " + service, nil
		},
		"status": func(_ context.Context, input map[string]any) (string, error) {
			service, err := requiredString(input, "service")
			if err != nil {
				return "", err
			}
			return fmt.Sprintf("[deploy] %s: running (v1.4.2)", service), nil
		},
	})
	return client, err
}

func objectSchema(required ...string) map[string]any {
	properties := make(map[string]any, len(required))
	for _, name := range required {
		properties[name] = map[string]any{"type": "string"}
	}
	return map[string]any{"type": "object", "properties": properties, "required": required}
}

func requiredString(input map[string]any, name string) (string, error) {
	value, ok := input[name].(string)
	if !ok || strings.TrimSpace(value) == "" {
		return "", fmt.Errorf("%s is required", name)
	}
	return strings.TrimSpace(value), nil
}
