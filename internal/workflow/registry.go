package workflow

import (
	"fmt"
	"regexp"
	"strings"
	"sync"
)

var workflowNamePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`)

type Registry struct {
	mu      sync.RWMutex
	entries map[string]Definition
	order   []string
}

func NewRegistry() *Registry {
	return &Registry{entries: make(map[string]Definition)}
}

// Register 在进程启动阶段注册可信脚本，并提前拒绝无效或重名定义。
func (r *Registry) Register(definition Definition) error {
	if err := ValidateMetadata(definition.Metadata); err != nil {
		return err
	}
	if definition.Script == nil {
		return fmt.Errorf("workflow %q script is required", definition.Metadata.Name)
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.entries[definition.Metadata.Name]; exists {
		return fmt.Errorf("workflow %q is already registered", definition.Metadata.Name)
	}
	definition.Metadata = cloneMetadata(definition.Metadata)
	r.entries[definition.Metadata.Name] = definition
	r.order = append(r.order, definition.Metadata.Name)
	return nil
}

func (r *Registry) Get(name string) (Definition, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	definition, ok := r.entries[name]
	definition.Metadata = cloneMetadata(definition.Metadata)
	return definition, ok
}

func (r *Registry) List() []Metadata {
	r.mu.RLock()
	defer r.mu.RUnlock()
	items := make([]Metadata, 0, len(r.order))
	for _, name := range r.order {
		items = append(items, cloneMetadata(r.entries[name].Metadata))
	}
	return items
}

func ValidateMetadata(metadata Metadata) error {
	if !workflowNamePattern.MatchString(metadata.Name) {
		return fmt.Errorf("workflow name must be a safe 1-64 character slug")
	}
	if strings.TrimSpace(metadata.Description) == "" {
		return fmt.Errorf("workflow %q description is required", metadata.Name)
	}
	for _, phase := range metadata.Phases {
		if strings.TrimSpace(phase) == "" {
			return fmt.Errorf("workflow %q phases must be non-empty strings", metadata.Name)
		}
	}
	return nil
}

// ParseToolInput 只接受公开 schema 中的三个字段，防止模型注入脚本或元数据。
func ParseToolInput(input any) (ToolInput, error) {
	payload, ok := input.(map[string]any)
	if !ok {
		return ToolInput{}, fmt.Errorf("workflow input must be an object")
	}
	for key := range payload {
		switch key {
		case "name", "args", "resume_from_run_id":
		default:
			return ToolInput{}, fmt.Errorf("unknown workflow input field %q", key)
		}
	}

	name, ok := payload["name"].(string)
	if !ok || strings.TrimSpace(name) == "" {
		return ToolInput{}, fmt.Errorf("workflow name is required")
	}
	parsed := ToolInput{Name: name, Args: map[string]any{}}
	if rawArgs, exists := payload["args"]; exists {
		args, ok := rawArgs.(map[string]any)
		if !ok {
			return ToolInput{}, fmt.Errorf("workflow args must be an object")
		}
		parsed.Args = args
	}
	if rawRunID, exists := payload["resume_from_run_id"]; exists {
		runID, ok := rawRunID.(string)
		if !ok || strings.TrimSpace(runID) == "" {
			return ToolInput{}, fmt.Errorf("resume_from_run_id must be a non-empty string")
		}
		parsed.ResumeFromRunID = runID
	}
	return parsed, nil
}

func cloneMetadata(metadata Metadata) Metadata {
	metadata.Phases = append([]string(nil), metadata.Phases...)
	return metadata
}
