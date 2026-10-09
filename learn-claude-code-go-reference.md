# learn-claude-code：Go 版代码对照

课程基线为 `~/Documents/Code/learn-claude-code` 的新版 s01–s17。当前项目已实现到 **s17 Goal Loop**。

## 已实现章节

| 章节 | 代码落点 | 关键边界 |
|---|---|---|
| s01 Agent Loop | `internal/agent/runner.go`, `internal/model/` | 根据真实 `tool_use` block 推进循环 |
| s02 Tool Use | `internal/tool/registry.go`, `internal/agent/tool_executor.go`, `internal/tool/builtin/` | 工具统一注册、执行并返回匹配的 `tool_result` |
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
| s16 Workflow Runtime | `internal/workflow/`, `internal/app/` | Host 注册脚本、结构化输出、并发编排、journal、运行锁与恢复 |
| s17 Goal Loop | `internal/goal/`, `internal/hooks/`, `internal/app/` | Session 级完成条件、独立 evaluator、同循环续轮、pending defer 与退出上限 |

## 目录职责与依赖方向

课程按“逐章增加能力”组织单文件示例；Go 版按职责组织包，不复制 s01–s17 的十七套主循环。

| 边界 | 入口 | 不承担的职责 |
|---|---|---|
| 组合根 | `app/bootstrap.go` | 不维护工具 schema、领域状态或模型 SDK 转换 |
| 用户交互 | `app/cli.go` | 不做权限策略判定；任务输入与审批共用一个 Reader |
| 共享协议 | `protocol/`、`tool/`、`llm/` | 不依赖 app、agent 或具体功能模块 |
| 循环编排 | `agent/session.go`、`runner.go`、`worker.go` | 不存放 Memory 提示词或普通工具实现 |
| 上下文生命周期 | `agentctx/manager.go`、`model.go` | 不管理 Team、Cron 或 CLI 输入 |
| 功能模块 | `internal/<feature>/` | 不反向调用 app，不直接读取 stdin |
| 外部适配 | `model/anthropic*.go` | 不向其他模块泄漏 SDK 类型 |

主要依赖可概括为 `app → agent / 功能模块 → llm / tool / protocol`；具体模型适配器 `model` 实现 `llm.Model`，由 app 注入。Subagent 与 Team 有意复用 `agent.Worker`；Goal、Workflow 和 MCP 不再为取得基础类型而依赖 agent。

功能间有必要的依赖不强行消除：Team 需要任务领域类型，但通过使用方定义的 `TaskBoard` / `WorkspaceProvider` 调用；Worktree 通过 `TaskBindings` 修改绑定。这样可以替换存储或提供测试替身，不必引入通用 service 层。

`internal/architecture/architecture_test.go` 持续检查基础协议层、组合根和主要功能包的依赖方向，防止后续扩展重新引入反向依赖。

大模块内部的阅读顺序：

- Memory：`types.go → store.go → recall.go → extract.go → consolidate.go`，分别对应数据结构、存储、召回、准入、合并。
- Task：`types.go → task.go → store.go → tools.go`，分别对应领域类型、状态流转、锁与落盘、工具适配。
- Cron：`cron_expression.go → cron.go → cron_store.go → cron_tools.go`，分别对应纯表达式计算、调度状态、持久化、工具适配。
- 新增普通工具：在所属模块声明 schema / handler，在 `app/bootstrap.go` 选择注册；只有改变循环控制的能力才需要改 Runner。

## 核心调用关系

```text
CLI / Cron / Team Event / Background Completion
    -> agent.Session
    -> agent.Runner
    -> agentctx.Manager.StartRequest（每次请求召回一次 Memory）
    -> refresh live system prompt（以下步骤逐轮执行）
    -> inject background results
    -> agentctx.Manager.Prepare(activeRequest)
    -> recovery -> agent.Worker.RunTurnWithOptions
    -> model.Anthropic
    -> agent.ToolExecutor
    -> Hooks / Registry / Runtime interceptor
    -> workflow.Manager（仅 workflow tool_use）
    -> tool_result
    -> 下一轮
    -> 无 tool_use 时进入 Goal Stop Hook
       -> block：反馈进入同一个 messages[] 后继续
       -> 其他决策：返回当前 Session 调用方
```

`internal/protocol` 定义 Message、ContentBlock、文本提取及深拷贝；`internal/tool` 定义工具协议和注册表；`internal/llm` 定义模型请求与接口。这三层不依赖业务模块。`internal/model` 负责 Anthropic SDK 转换；SDK 类型不会进入 Agent Loop。主 Agent 和 Subagent 共用 Worker 与 ToolExecutor，不维护第二套模型和工具调用实现。

Subagent / Teammate 使用 `Worker.RunTurn` 封装有界截断恢复；主 Runner 使用更底层的 `RunTurnWithOptions`，由自身维护跨轮重试、备用模型和续写状态。

Background 与 Cron 同属 `internal/runtime`，但状态完全隔离：`BackgroundManager` 管理命令生命周期，`CronScheduler` 管理未来输入和持久化。`internal/app/async_runtime.go` 将 Cron、Team 与 Background 完成事件合并后，统一使用 Session 的非阻塞入口投递。权限模式通过 `context.Context` 传递，不再使用进程级布尔状态。

Agent Teams 的 `team.Runtime` 为每个 Teammate 维护独立 `messages[]` 和受限 Registry。`.mailboxes/` 负责跨线程投递，`.tasks/` 是共享任务状态源；IDLE 先处理消息，再扫描并认领 ready Task。Lead 邮箱事件由 App 单点消费，并通过同一个 Session 非阻塞入口启动新 turn。

Plan Approval 与 Shutdown 使用 `request_id`、类型、参与方和状态共同匹配。Task 可选绑定 Git Worktree；动态 Resolver 让 Bash 和文件工具使用当前 assignment 目录，失效绑定采用 fail-closed。Worktree 删除保留为 Host API，不暴露给模型。

MCP 由 `mcp.Manager` 管理连接和 discovery。`connect_mcp` 成功后，新工具以 `mcp__{server}__{tool}` 注册到主 Agent 的 Registry，下一轮同时刷新 Tools 与 System Prompt；Subagent 和 Teammate 不获得该能力。当前 `docs` 与 `deploy` 是进程内 Mock Server，只模拟 `tools/list` 和 `tools/call`，不实现真实 Transport。

MCP 名称会先规范化，再检查 64 字符限制及与 Built-in/其他 Server 的冲突。只有 Host Policy 能直接放行外部工具；Server annotations 不构成授权。未知或未配置工具默认确认，Cron 与 Team Event 等非交互 turn fail-closed。MCP 输入或 Handler 错误以 `tool_result` 返回，不终止 Agent Loop。

Workflow 由 `workflow.Manager` 管理一次完整的工具调用。主 Agent 只提交名称、args 和可选 run ID；可信 Script 由 Host Registry 提供。Workflow agent 直接复用 `llm.Model`，但不携带工具。并发调用共享 semaphore、agent cap 和 token budget，中间结果通过 `.workflows/<runId>.journal.jsonl` checkpoint，不逐步写入主会话。

恢复会先锁定 run，再核对 snapshot 中的 Workflow 名称与原始 args。每个 agent 调用使用基于 label、prompt 和 Schema 的稳定 key；命中 journal 时不调用模型，也不增加本次 Task usage。snapshot/output 原子替换，journal append 后同步，同一 run 由进程内状态和文件锁共同防止并发恢复。

## s15 集成边界

- `agent.Session` 串行化用户与自动 turn，并单独保存当前权威请求。Team 和 Background 事件不会覆盖该请求；Cron 只在当次 turn 临时覆盖。
- `compact.Manager` 把权威请求与历史摘要分别放入 `Authoritative request` 和不可信 `Reference state`，reactive compact 摘要失败时仍保留最近消息继续。
- `prompt.Builder` 在每次模型调用前刷新当前时间、工具、MCP Server 和活跃 Teammate；Memory 召回在请求开始时完成并在该请求内复用。
- `agent.recovery` 对 429/529 执行最多 3 次带 jitter 的指数退避；连续两次 529 可切换备用模型。`max_tokens` 先从 8000 提升到 16000，主 Runner 的纯文本最多续写两次；仍不完整的工具调用直接返回错误。Subagent / Teammate 扩容重试一次后仍截断也会返回错误。
- Bash 执行器限时 120 秒、限制返回 50KB，并回收 process group。每个 Bash 都需前台审批，自动 turn 因无法安全读取 stdin 而 fail closed。

## 日志与注释

运行日志统一使用：

- `logger.Debug(format, args...)`
- `logger.Info(format, args...)`
- `logger.Warn(format, args...)`
- `logger.Error(format, args...)`

标准格式为 `日期 时间 [Level] 原内容`。CLI、最终回答、Todo 展示和权限询问属于交互输出，不使用 Logger。

核心注释统一使用中文，重点解释消息协议、并发、持久化和状态机约束；Agent、LLM、ContentBlock、tool_use、tool_result、Cron 等专业术语保留英文。

## s16 Workflow 边界

- `workflow` 是同步的主 Agent 工具调用；`async_launched` 只是生命周期字段，不进入统一自动事件运行时。
- `Parallel` 是 barrier；`Pipeline` 只保证单个 item 的 Stage 顺序，不设置跨 item barrier。
- 结构化结果校验失败只重试一次；恢复缓存同样重新校验。
- Script 最多嵌套一层 Workflow，嵌套调用共享 journal、限额和 Task usage。
- 当前内置 `review-changes`，输入必须通过 `args.changes` 显式提供，不允许 Workflow agent 自行读取工作区。
- Workflow 的最终 JSON 才会作为 `tool_result` 进入主会话，中间结果不消耗主 Session 的消息历史。

## s17 Goal Loop 边界

- `/goal <condition>` 设置 Session 范围内唯一的活动 Goal，并把 condition 作为当前用户任务提交；`/goal` 和清理命令不启动模型 turn。
- Stop Hook 只在主 Agent 没有真实 `tool_use` 时评估。`block` 反馈追加到同一个 `messages[]`，不会创建第二个 Session 或隐藏队列。
- `PromptEvaluator` 复用 `llm.Model`，但不携带工具；其请求、响应和 token usage 不进入主 Session。
- evaluator transcript 默认保留最近 24,000 字符的完整消息，并保留 tool_use/tool_result 证据。响应必须满足严格的 `{ok, reason, impossible?}` JSON 契约。
- Background 正在运行，或 Teammate 处于 working、stopping、waiting_approval 时返回 defer；完成事件进入原 Session 后再评估。Workflow 是同步工具调用，不参与 pending 判断。
- `MAX_TURNS` 和连续 Stop block cap 都是 Host 退出边界。达到上限或 evaluator 出错时保留活动 Goal，用户可以检查、继续、替换或清理。
- `goal_status` Event 和 `goal.Restore` 提供 Host 持久化接口；当前 CLI 不持久化完整 Session，也不会在进程重启后自动恢复 Goal。

## 与参考课程的实现差异与限制

这里对照的是本地课程 README 的设计目标，不能把“章节能力已接入”理解为生产级隔离或逐项完全等价：

- Go 版将单文件示例拆成包，统一复用 Worker、工具执行器和模型协议；模型辅助调用不自动获得主 Agent 工具。
- 手动 `compact` 仍是 Runner 的控制工具：触发后停止当前工具批次并替换历史。参考课程当前版本是在完整工具批次结束后压缩，本次结构重构保留 Go 版行为，没有声称两者一致。
- MCP 的 `docs / deploy` 仍是进程内 Mock Server；没有实现真实远端传输。
- Worktree 不是权限沙箱；Bash 的 deny list 也不是完整 Shell 安全解析器。
- Memory 当前由 Session 串行维护，不提供多进程事务；Cron 是进程内轮询、至少一次投递；完整 Session / Goal 不会自动跨进程恢复。
- `internal/` 内部 API 有调整：模型类型从 agent 移到 llm，Registry 从 agent 移到 tool；文件工具通过 `builtin.New(resolver)` 构造，取消隐式工作区单例；`app.New` 返回 `(*App, error)`。

## 验证

```bash
GOCACHE=/private/tmp/go-agent-harness-go-cache go test ./...
GOCACHE=/private/tmp/go-agent-harness-go-cache go vet ./...
GOCACHE=/private/tmp/go-agent-harness-go-cache go test -race ./...
```
