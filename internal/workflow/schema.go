package workflow

import (
	"fmt"
	"math"
	"reflect"
)

var supportedSchemaTypes = map[string]bool{
	"object": true, "array": true, "string": true, "boolean": true, "number": true,
}

// ValidateSchema 只开放 Workflow 结构化输出需要的 JSON Schema 子集。
func ValidateSchema(schema map[string]any) error {
	return validateSchemaAt(schema, "$schema")
}

func validateSchemaAt(schema map[string]any, path string) error {
	typeName, ok := schema["type"].(string)
	if !ok || !supportedSchemaTypes[typeName] {
		return fmt.Errorf("%s.type must be one of object, array, string, boolean, number", path)
	}
	if rawEnum, exists := schema["enum"]; exists {
		if _, ok := enumValues(rawEnum); !ok {
			return fmt.Errorf("%s.enum must be an array", path)
		}
	}

	switch typeName {
	case "object":
		properties, err := schemaMap(schema["properties"], path+".properties", true)
		if err != nil {
			return err
		}
		for name, raw := range properties {
			child, ok := raw.(map[string]any)
			if !ok {
				return fmt.Errorf("%s.properties.%s must be an object", path, name)
			}
			if err := validateSchemaAt(child, path+".properties."+name); err != nil {
				return err
			}
		}
		if rawRequired, exists := schema["required"]; exists {
			required, ok := rawRequired.([]string)
			if !ok {
				return fmt.Errorf("%s.required must be a string array", path)
			}
			for _, name := range required {
				if _, exists := properties[name]; !exists {
					return fmt.Errorf("%s.required references unknown property %q", path, name)
				}
			}
		}
		if additional, exists := schema["additionalProperties"]; exists {
			if _, ok := additional.(bool); !ok {
				return fmt.Errorf("%s.additionalProperties must be a boolean", path)
			}
		}
	case "array":
		items, err := schemaMap(schema["items"], path+".items", false)
		if err != nil {
			return err
		}
		if err := validateSchemaAt(items, path+".items"); err != nil {
			return err
		}
	}
	return nil
}

// ValidateValue 在 agent 输出进入后续阶段前校验其结构，避免脚本处理不可信 JSON。
func ValidateValue(value any, schema map[string]any) error {
	if err := ValidateSchema(schema); err != nil {
		return err
	}
	return validateValueAt(value, schema, "$value")
}

func validateValueAt(value any, schema map[string]any, path string) error {
	if rawEnum, exists := schema["enum"]; exists {
		matched := false
		values, _ := enumValues(rawEnum)
		for _, candidate := range values {
			if reflect.DeepEqual(value, candidate) {
				matched = true
				break
			}
		}
		if !matched {
			return fmt.Errorf("%s must match one of the allowed values", path)
		}
	}

	switch schema["type"].(string) {
	case "object":
		object, ok := value.(map[string]any)
		if !ok {
			return fmt.Errorf("%s must be an object", path)
		}
		properties, _ := schemaMap(schema["properties"], "", true)
		if required, ok := schema["required"].([]string); ok {
			for _, name := range required {
				if _, exists := object[name]; !exists {
					return fmt.Errorf("%s.%s is required", path, name)
				}
			}
		}
		if additional, _ := schema["additionalProperties"].(bool); !additional {
			if _, declared := schema["additionalProperties"]; declared {
				for name := range object {
					if _, exists := properties[name]; !exists {
						return fmt.Errorf("%s.%s is not allowed", path, name)
					}
				}
			}
		}
		for name, childSchema := range properties {
			child, exists := object[name]
			if !exists {
				continue
			}
			if err := validateValueAt(child, childSchema.(map[string]any), path+"."+name); err != nil {
				return err
			}
		}
	case "array":
		array, ok := value.([]any)
		if !ok {
			return fmt.Errorf("%s must be an array", path)
		}
		items := schema["items"].(map[string]any)
		for index, item := range array {
			if err := validateValueAt(item, items, fmt.Sprintf("%s[%d]", path, index)); err != nil {
				return err
			}
		}
	case "string":
		if _, ok := value.(string); !ok {
			return fmt.Errorf("%s must be a string", path)
		}
	case "boolean":
		if _, ok := value.(bool); !ok {
			return fmt.Errorf("%s must be a boolean", path)
		}
	case "number":
		if !isJSONNumber(value) {
			return fmt.Errorf("%s must be a finite number", path)
		}
	}
	return nil
}

func enumValues(value any) ([]any, bool) {
	switch values := value.(type) {
	case []any:
		return values, true
	case []string:
		result := make([]any, len(values))
		for index, item := range values {
			result[index] = item
		}
		return result, true
	default:
		return nil, false
	}
}

func schemaMap(value any, path string, optional bool) (map[string]any, error) {
	if value == nil && optional {
		return map[string]any{}, nil
	}
	result, ok := value.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("%s must be an object", path)
	}
	return result, nil
}

func isJSONNumber(value any) bool {
	switch number := value.(type) {
	case float64:
		return !math.IsNaN(number) && !math.IsInf(number, 0)
	case float32:
		return !math.IsNaN(float64(number)) && !math.IsInf(float64(number), 0)
	case int, int8, int16, int32, int64, uint, uint8, uint16, uint32, uint64:
		return true
	default:
		return false
	}
}
