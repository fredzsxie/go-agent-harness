// Package prompt 根据稳定片段与运行时状态组装 System Prompt。
package prompt

import (
	"encoding/json"
	"sort"
	"strings"
)

// Context 保存控制条件 Prompt 片段的真实运行时状态，不通过用户消息关键词猜测能力。
type Context struct {
	EnabledTools []string `json:"enabled_tools"`
	Workspace    string   `json:"workspace"`
	Memories     string   `json:"memories"`
}

// Builder 组装并缓存主 Agent 的 System Prompt；这里只减少字符串组装，API Prompt Cache 由模型供应商处理。
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

// Get 在 Context 未变化时复用上次结果；JSON 序列化为 map 和嵌套值提供稳定的缓存键。
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
	// 仅在任务工具实际可用时注入建图规则，避免提示不存在的能力。
	if hasTool(context.EnabledTools, "create_task") {
		sections = append(sections, "Use task tools to track dependencies and progress. Create all task nodes first. After create_task returns runtime-generated IDs, use update_task with those exact IDs to add dependencies.")
	}
	if hasTool(context.EnabledTools, "bash") {
		sections = append(sections, "Set bash run_in_background to true only for independent slow commands whose results are not needed immediately.")
	}
	if hasTool(context.EnabledTools, "schedule_cron") {
		sections = append(sections, "Use schedule_cron for work that should start at a future local time. Cron jobs run only while this Agent process is running; durable jobs are restored after restart but missed times are not replayed.")
	}
	if hasTool(context.EnabledTools, "spawn_teammate") {
		sections = append(sections, "For substantial parallel work, first propose a small team with clear responsibilities and wait for the user's confirmation. Do not call spawn_teammate before confirmation. After confirmation, create Tasks for delegated work and pass their IDs when spawning teammates. Use create_worktree only when an isolated Git working directory prevents conflicting edits. After spawning, end the current turn instead of polling; Team events will be delivered by the runtime. Shut teammates down after coordination completes.")
	}
	if context.Memories != "" {
		sections = append(sections, context.Memories)
	}
	return Build(sections...)
}

func hasTool(tools []string, target string) bool {
	index := sort.SearchStrings(tools, target)
	return index < len(tools) && tools[index] == target
}

// Main 为静态 Prompt 调用方保留；新代码应使用 Builder 和运行时 Context。
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
