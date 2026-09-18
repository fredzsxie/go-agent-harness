# learn-claude-code：Go 版代码对照

课程基线为 `~/Documents/Code/learn-claude-code` 的新版 s01–s17。当前项目实现到 **s15 Integrated Harness**；s16–s17 只预留 package 文档或接口，不包含运行逻辑。

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
| s11 Background Tasks | `internal/runtime/background.go` | 显式异步执行，完成后唤醒自动 turn 注入结果 |
| s12 Cron Scheduler | `internal/runtime/cron.go`, `internal/runtime/cron_store.go` | 到期队列、空闲投递、失败恢复和 durable job |
| s13 Agent Teams | `internal/team/`, `internal/worktree/`, `internal/app/` | 独立上下文、文件邮箱、原子认领、Plan Gate、类型化控制协议与可选 Worktree |
| s14 MCP Tools | `internal/mcp/`, `internal/app/`, `internal/permission/` | 进程内 discovery/call、动态 Registry、名称冲突检查与 Host Policy |
| s15 Integrated Harness | `internal/app/`, `internal/agent/`, `internal/agentctx/`, `internal/prompt/` | 统一自动事件、模型恢复、实时 Prompt 与压缩授权边界 |

## 核心调用关系

```text
CLI / Cron / Team Event / Background Completion
    -> agent.Session
    -> agent.Runner
    -> inject background results
    -> agentctx.Manager.Prepare(activeRequest)
    -> refresh live system prompt
    -> agent.Worker.RunTurn
    -> model.Anthropic
    -> agent.ToolExecutor
    -> Hooks / Registry / Runtime interceptor
    -> tool_result
    -> 下一轮或返回
```

`internal/protocol` 只定义 Message 与 ContentBlock，不依赖业务包。`internal/model` 负责 Anthropic SDK 转换；SDK 类型不会进入 Agent Loop。主 Agent 和 Subagent 共用 Worker 与 ToolExecutor，不维护第二套模型和工具调用实现。

Background 与 Cron 同属 `internal/runtime`，但状态完全隔离：`BackgroundManager` 管理命令生命周期，`CronScheduler` 管理未来输入和持久化。`internal/app/async_runtime.go` 将 Cron、Team 与 Background 完成事件合并后，统一使用 Session 的非阻塞入口投递。权限模式通过 `context.Context` 传递，不再使用进程级布尔状态。

Agent Teams 的 `team.Runtime` 为每个 Teammate 维护独立 `messages[]` 和受限 Registry。`.mailboxes/` 负责跨线程投递，`.tasks/` 是共享任务状态源；IDLE 先处理消息，再扫描并认领 ready Task。Lead 邮箱事件由 App 单点消费，并通过同一个 Session 非阻塞入口启动新 turn。

Plan Approval 与 Shutdown 使用 `request_id`、类型、参与方和状态共同匹配。Task 可选绑定 Git Worktree；动态 Resolver 让 Bash 和文件工具使用当前 assignment 目录，失效绑定采用 fail-closed。Worktree 删除保留为 Host API，不暴露给模型。

MCP 由 `mcp.Manager` 管理连接和 discovery。`connect_mcp` 成功后，新工具以 `mcp__{server}__{tool}` 注册到主 Agent 的 Registry，下一轮同时刷新 Tools 与 System Prompt；Subagent 和 Teammate 不获得该能力。当前 `docs` 与 `deploy` 是进程内 Mock Server，只模拟 `tools/list` 和 `tools/call`，不实现真实 Transport。

MCP 名称会先规范化，再检查 64 字符限制及与 Built-in/其他 Server 的冲突。只有 Host Policy 能直接放行外部工具；Server annotations 不构成授权。未知或未配置工具默认确认，Cron 与 Team Event 等非交互 turn fail-closed。MCP 输入或 Handler 错误以 `tool_result` 返回，不终止 Agent Loop。

## s15 集成边界

- `agent.Session` 串行化用户与自动 turn，并单独保存当前权威请求。Team 和 Background 事件不会覆盖该请求；Cron 只在当次 turn 临时覆盖。
- `compact.Manager` 把权威请求与历史摘要分别放入 `Authoritative request` 和不可信 `Reference state`，reactive compact 摘要失败时仍保留最近消息继续。
- `prompt.Builder` 在每次模型调用前刷新当前时间、工具、MCP Server 和活跃 Teammate；Memory 召回在请求开始时完成并在该请求内复用。
- `agent.recovery` 对 429/529 执行最多 3 次带 jitter 的指数退避；连续两次 529 可切换备用模型。`max_tokens` 先从 8000 提升到 16000，再最多续写两次。
- Bash 执行器限时 120 秒、限制返回 50KB，并回收 process group。每个 Bash 都需前台审批，自动 turn 因无法安全读取 stdin 而 fail closed。

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
| s16 Workflow Runtime | `internal/workflow/` | 不定义 Step、Checkpoint 或执行器 |
| s17 Goal Loop | `internal/goal/` | 不实现 Evaluator、自动续轮或 `/goal` |

开始下一章前保持这些占位模块无副作用：不启动 goroutine、不读写文件、不调用 LLM。

## 验证

```bash
GOCACHE=/private/tmp/go-agent-harness-go-cache go test ./...
GOCACHE=/private/tmp/go-agent-harness-go-cache go vet ./...
GOCACHE=/private/tmp/go-agent-harness-go-cache go test -race ./...
```
