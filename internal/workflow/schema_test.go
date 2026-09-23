package workflow

import "testing"

func TestValidateValueSupportsWorkflowSchemaSubset(t *testing.T) {
	schema := map[string]any{
		"type":                 "object",
		"required":             []string{"findings"},
		"additionalProperties": false,
		"properties": map[string]any{
			"findings": map[string]any{
				"type": "array",
				"items": map[string]any{
					"type":     "object",
					"required": []string{"title", "severity", "verified"},
					"properties": map[string]any{
						"title":    map[string]any{"type": "string"},
						"severity": map[string]any{"type": "string", "enum": []string{"high", "medium", "low"}},
						"verified": map[string]any{"type": "boolean"},
						"score":    map[string]any{"type": "number"},
					},
				},
			},
		},
	}
	value := map[string]any{"findings": []any{map[string]any{
		"title": "SQL injection", "severity": "high", "verified": true, "score": 0.9,
	}}}
	if err := ValidateValue(value, schema); err != nil {
		t.Fatal(err)
	}

	value["extra"] = true
	if err := ValidateValue(value, schema); err == nil {
		t.Fatal("expected additional property to fail")
	}
}

func TestValidateValueRejectsInvalidNestedOutput(t *testing.T) {
	schema := map[string]any{
		"type": "array",
		"items": map[string]any{"type": "object", "required": []string{"severity"}, "properties": map[string]any{
			"severity": map[string]any{"type": "string", "enum": []any{"high", "low"}},
		}},
	}
	if err := ValidateValue([]any{map[string]any{"severity": "medium"}}, schema); err == nil {
		t.Fatal("expected invalid enum to fail")
	}
	if err := ValidateValue([]any{map[string]any{}}, schema); err == nil {
		t.Fatal("expected missing required property to fail")
	}
}

func TestValidateSchemaRejectsUnsupportedOrMalformedSchema(t *testing.T) {
	tests := []map[string]any{
		{"type": "integer"},
		{"type": "array"},
		{"type": "object", "properties": map[string]any{"name": "string"}},
		{"type": "object", "required": []string{"missing"}, "properties": map[string]any{}},
		{"type": "string", "enum": "a"},
	}
	for _, schema := range tests {
		if err := ValidateSchema(schema); err == nil {
			t.Fatalf("expected invalid schema to fail: %#v", schema)
		}
	}
}
