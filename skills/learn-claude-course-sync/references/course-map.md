# 课程映射

这个文件用于把 `learn-claude-code` 的课程目录，映射到当前 `go-agent-harness` 的主要改动落点。

## 参考仓库路径

- `~/Documents/Code/learn-claude-code`

## 课程与代码落点

| 课程 | 参考目录 | 当前项目主要落点 |
| --- | --- | --- |
| s01 | `s01_agent_loop/` | `loop/types.go`, `loop/runner.go` |
| s02 | `s02_tool_use/` | `loop/registry.go`, `loop/tooluse.go`, `tools/` |
| s03 | `s03_permission/` | `internal/permission/`, `internal/app/hooks.go` |
| s04 | `s04_hooks/` | `internal/hooks/`, `loop/tooluse.go`, `internal/app/hooks.go` |
| s05 | `s05_todo_write/` | `internal/todo/`, `internal/app/registry.go`, `loop/runner.go` |
| s06 | `s06_subagent/` | `internal/subagent/`, `internal/app/registry.go` |
| s07 | `s07_skill_loading/` | `internal/skill/`, `internal/prompt/`, `internal/app/` |
| s08 | `s08_context_compact/` | `internal/compact/`, `loop/runner.go` |
| s09 | `s09_memory/` | `internal/memory/`, `internal/prompt/` |
| s10 | `s10_system_prompt/` | `internal/prompt/`, `internal/app/app.go`, `loop/runner.go` |
| s11 | `s11_error_recovery/` | `internal/retry/`, `loop/runner.go` |
| s12 | `s12_task_system/` | `internal/task/`, `internal/subagent/`, `internal/app/registry.go` |
| s13 | `s13_background_tasks/` | `internal/scheduler/`, `internal/task/`, `loop/runner.go` |
| s14 | `s14_cron_scheduler/` | `internal/scheduler/` |
| s15 | `s15_agent_teams/` | `internal/team/` |
| s16 | `s16_team_protocols/` | `internal/team/` |
| s17 | `s17_autonomous_agents/` | `internal/team/`, `internal/task/` |
| s18 | `s18_worktree_isolation/` | `internal/worktree/`, `internal/workspace/` |
| s19 | `s19_mcp_plugin/` | `tools/`, 后续可扩到 `internal/mcp/` |
| s20 | `s20_comprehensive/` | 全局联调，以 `loop/runner.go` 为中心 |

## 阅读顺序

针对单课对齐，建议按这个顺序读取：

1. 参考课 `README.md`
2. 参考课 `code.py`
3. 当前项目对应 feature package
4. `internal/app/registry.go`
5. `loop/runner.go` 或 `loop/tooluse.go`

## 差异处理规则

- 参考版使用单文件时，当前项目按模块职责拆开实现。
- 当前项目已有公共能力时，优先接到现有扩展点，不新造并行入口。
- 行为一致比结构一致更重要。
- 只有当课程目标明确要求，才修改主 loop 控制流。

## 交付标准

完成课程对齐后，至少要做到：

1. 新逻辑能从当前 app 装配层真正接入运行时。
2. 新工具能通过 `loop.Registry` 暴露。
3. 新状态不是散落在多个无关结构体里。
4. 改动后的 Go 文件已 `gofmt`。
5. `GOCACHE=/private/tmp/go-agent-harness-go-cache go test ./...` 通过。
