# learn-claude-code：Go 版代码对照

课程基线为 `~/Documents/Code/learn-claude-code` 的新版 s01–s17。当前项目实现到 **s13 Agent Teams**；s14–s17 只预留 package 文档或接口，不包含运行逻辑。

## 已实现章节

| 章节 | 代码落点 | 关键边界 |
|---|---|---|
| s01 Agent Loop | `internal/agent/runner.go`, `internal/model/` | 根据真实 `tool_use` block 推进循环 |
| s02 Tool Use | `internal/agent/registry.go`, `internal/agent/tool_executor.go`, `internal/tool/builtin/` | 工具统一注册、执行并返回匹配的 `tool_result` |
| s03 Permission | `internal/permission/`, `internal/workspace/` | deny list、规则审批、工作区路径限制 |
| s04 Hooks | `internal/hooks/`, `internal/app/hooks.go` | UserPromptSubmit、PreToolUse、PostToolUse、Stop |
| s05 TodoWrite | `internal/todo/` | 20 项上限、单一 in-progress、原子更新 |
| s06 Subagent | `internal/subagent/`, `internal/agent/worker.go` | 独立消息、受限工具、复用 Worker |
| s07 Skill Loading | `internal/skill/`, `internal/prompt/` | 目录常驻，SKILL.md 按需加载 |
| s08 Context Compact | `internal/compact/`, `internal/agentctx/` | 保留未消费结果，按层压缩，超大结果落盘 |
| s09 Memory | `internal/memory/`, `internal/agentctx/` | 召回、提取和合并持久 Memory |
| s10 Task System | `internal/task/` | 持久任务图、依赖、claim 与 complete |
| s11 Background Tasks | `internal/runtime/background.go` | 显式异步执行，结果在后续 turn 注入 |
| s12 Cron Scheduler | `internal/runtime/cron.go`, `internal/runtime/cron_store.go` | 到期队列、空闲投递、失败恢复和 durable job |
| s13 Agent Teams | `internal/team/`, `internal/worktree/`, `internal/app/` | 独立上下文、文件邮箱、原子认领、Plan Gate、类型化控制协议与可选 Worktree |

## 核心调用关系

```text
CLI / Cron / Team Event
    -> agent.Session
    -> agent.Runner
    -> agentctx.Manager.Prepare
    -> agent.Worker.RunTurn
    -> model.Anthropic
    -> agent.ToolExecutor
    -> Hooks / Registry / Runtime interceptor
    -> tool_result
    -> 下一轮或返回
```

`internal/protocol` 只定义 Message 与 ContentBlock，不依赖业务包。`internal/model` 负责 Anthropic SDK 转换；SDK 类型不会进入 Agent Loop。主 Agent 和 Subagent 共用 Worker 与 ToolExecutor，不维护第二套模型和工具调用实现。

Background 与 Cron 同属 `internal/runtime`，但状态完全隔离：`BackgroundManager` 管理命令生命周期，`CronScheduler` 管理未来输入和持久化。Cron 使用 Session 的非阻塞入口，只有成功占用 Session 后才切换为非交互权限模式。

Agent Teams 的 `team.Runtime` 为每个 Teammate 维护独立 `messages[]` 和受限 Registry。`.mailboxes/` 负责跨线程投递，`.tasks/` 是共享任务状态源；IDLE 先处理消息，再扫描并认领 ready Task。Lead 邮箱事件由 App 单点消费，并通过同一个 Session 非阻塞入口启动新 turn。

Plan Approval 与 Shutdown 使用 `request_id`、类型、参与方和状态共同匹配。Task 可选绑定 Git Worktree；动态 Resolver 让 Bash 和文件工具使用当前 assignment 目录，失效绑定采用 fail-closed。Worktree 删除保留为 Host API，不暴露给模型。

## 日志与注释

运行日志统一使用：

- `logger.Debug(format, args...)`
- `logger.Info(format, args...)`
- `logger.Warn(format, args...)`
- `logger.Error(format, args...)`

标准格式为 `日期 时间 [Level] 原内容`。CLI、最终回答、Todo 展示和权限询问属于交互输出，不使用 Logger。

核心注释统一使用中文，重点解释消息协议、并发、持久化和状态机约束；Agent、LLM、ContentBlock、tool_use、tool_result、Cron 等专业术语保留英文。

## 后续占位

| 章节 | 占位位置 | 当前限制 |
|---|---|---|
| s14 MCP Plugin | `internal/mcp/` | 不连接 MCP Server，不注册 MCP 工具 |
| s15 Integrated Harness | `internal/app/` | 不新增独立 package |
| s16 Workflow Runtime | `internal/workflow/` | 不定义 Step、Checkpoint 或执行器 |
| s17 Goal Loop | `internal/goal/` | 不实现 Evaluator、自动续轮或 `/goal` |

开始下一章前保持这些占位 package 无副作用：不注册工具、不启动 goroutine、不读写文件、不调用 LLM。

## 验证

```bash
GOCACHE=/private/tmp/go-agent-harness-go-cache go test ./...
GOCACHE=/private/tmp/go-agent-harness-go-cache go vet ./...
GOCACHE=/private/tmp/go-agent-harness-go-cache go test -race ./...
```
