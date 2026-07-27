// Package prompt builds system prompts from stable sections and runtime state.
package prompt

import (
	"encoding/json"
	"sort"
	"strings"
)

// Context contains the real runtime state that controls conditional prompt
// sections. It deliberately does not inspect user messages: a section should
// be enabled by available capabilities, not keyword guesses.
type Context struct {
	EnabledTools []string `json:"enabled_tools"`
	Workspace    string   `json:"workspace"`
	Memories     string   `json:"memories"`
}

// Builder assembles and caches the main-agent system prompt. The cache avoids
// redundant string assembly only; API-level prompt caching is handled by the
// model provider.
type Builder struct {
	skillCatalog string
	workspace    string

	lastContextKey string
	lastPrompt     string
}

func NewBuilder(skillCatalog, workspace string) *Builder {
	if strings.TrimSpace(skillCatalog) == "" {
		skillCatalog = "(no skills found)"
	}
	return &Builder{
		skillCatalog: skillCatalog,
		workspace:    strings.TrimSpace(workspace),
	}
}

// Get returns an assembled prompt, reusing the previous result when context is
// unchanged. JSON serialization is deterministic for maps and nested values,
// unlike Go values used directly as cache keys.
func (b *Builder) Get(context Context) string {
	context = b.normalize(context)
	key, err := json.Marshal(context)
	if err == nil && key != nil && string(key) == b.lastContextKey && b.lastPrompt != "" {
		return b.lastPrompt
	}

	prompt := b.assemble(context)
	if err == nil {
		b.lastContextKey = string(key)
		b.lastPrompt = prompt
	}
	return prompt
}

func (b *Builder) normalize(context Context) Context {
	context.Workspace = strings.TrimSpace(context.Workspace)
	if context.Workspace == "" {
		context.Workspace = b.workspace
	}
	context.Memories = strings.TrimSpace(context.Memories)
	context.EnabledTools = append([]string(nil), context.EnabledTools...)
	sort.Strings(context.EnabledTools)
	return context
}

func (b *Builder) assemble(context Context) string {
	tools := "(no tools registered)"
	if len(context.EnabledTools) > 0 {
		tools = strings.Join(context.EnabledTools, ", ")
	}

	sections := []string{
		"You are Claude Code, Anthropic's official CLI for Claude.\nYou are a helpful coding assistant. Act on the task; do not merely describe what you would do.\nFor complex sub-problems, use task to spawn a subagent.\nBefore starting any multi-step task, use todo_write to plan your steps and keep statuses updated.\nWhen the user says \"remember\" or expresses a stable preference, save it as memory after the turn.",
		"Available tools: " + tools,
		"Working directory: " + context.Workspace,
		"Skills available:\n" + b.skillCatalog + "\nUse load_skill to get full details when needed.",
	}
	if context.Memories != "" {
		sections = append(sections, context.Memories)
	}
	return Build(sections...)
}

// Main is retained for callers that need a static prompt. New code should use
// Builder with a runtime Context.
func Main(skillCatalog string, memorySections ...string) string {
	return Build(NewBuilder(skillCatalog, "").Get(Context{}), Build(memorySections...))
}

func Subagent() string {
	return Build(
		"You are a coding assistant.",
		"Complete the task you were given, then return a concise summary.",
		"Do not delegate further.",
	)
}

func TodoReminder() string {
	return "<reminder>Update your todos.</reminder>"
}

func Build(sections ...string) string {
	parts := make([]string, 0, len(sections))
	for _, section := range sections {
		section = strings.TrimSpace(section)
		if section != "" {
			parts = append(parts, section)
		}
	}
	return strings.Join(parts, "\n\n")
}
