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

## 如何使用这份对照

原课程 [shareAI-lab/learn-claude-code](https://github.com/shareAI-lab/learn-claude-code) 用十七个逐步扩展的 Python 单文件示例讲清 Agent Harness。当前项目保留相同的学习顺序，但将最终能力整合到一套可测试的 Go 包中，因此阅读时建议区分两条线：

1. **课程递进线**：关注每一课相对上一课新增了什么，以及为什么不需要推翻 s01 的 Agent Loop。
2. **Go 工程线**：从本文给出的文件和符号进入代码，观察能力如何通过接口、组合根和独立模块接入同一个循环。

每节中的“新增”描述原课程的教学增量；“Go 落点”描述当前项目的最终集成实现。二者目标一致，但 Go 版在并发、持久化、错误恢复和模块边界上做了工程化强化。

## s01–s17 逐课精读

### s01 · Agent Loop：建立最小闭环

**要解决的问题**：普通 LLM 只会生成“应该执行的命令”，不会替用户执行，也看不到执行结果。Harness 需要成为模型和外部世界之间的执行层。

**本课新增**：建立唯一的 `messages[]` 循环。把用户消息交给模型；若响应中存在真实的 `tool_use` block，就执行工具并用相同 ID 写回 `tool_result`；没有 `tool_use` 才允许结束。终止条件来自内容块，而不是仅依赖 `stop_reason`。

```text
user message -> model -> assistant(tool_use)
                         -> execute tool
                         -> user(tool_result) -> model -> ...
model response without tool_use -> stop
```

**Go 落点**：

- [`Runner.Run`](internal/agent/runner.go#L17) 维护跨轮循环和消息历史。
- [`Worker.RunTurnWithOptions`](internal/agent/worker.go#L66) 完成一次模型调用及本轮工具执行。
- [`Message` / `ContentBlock`](internal/protocol/message.go) 定义不依赖 SDK 的消息协议。
- [`Anthropic.Complete`](internal/model/anthropic.go#L34) 只负责 `llm` 协议与 Anthropic SDK 之间的转换。

**阅读重点**：`Runner` 决定“是否继续”，`Worker` 负责“一轮怎么执行”，模型适配器只处理外部 API。三者分离后，后续课程无需复制主循环。

### s02 · Tool Use：从硬编码命令到注册表

**相对 s01 新增**：工具从单一 Bash 扩展为 `bash`、`read_file`、`write_file`、`edit_file`、`glob`。每个工具由 JSON Schema 和 handler 组成，Agent Loop 从硬编码 `run_bash` 改为按名称查表分发；一次响应中的多个工具调用按原顺序执行。

**核心机制**：Schema 告诉模型“可以怎样调用”，handler 决定“调用后实际做什么”。新增普通工具只应增加定义与处理函数，不应修改循环控制。

**Go 落点**：

- [`tool.Spec`](internal/tool/spec.go) 表达名称、描述和输入 Schema。
- [`tool.Registry`](internal/tool/registry.go#L18) 统一保存定义、handler 和工具来源。
- [`ToolExecutor.Execute`](internal/agent/tool_executor.go#L45) 顺序执行一批 `tool_use`，并生成逐一匹配的 `tool_result`。
- [`internal/tool/builtin`](internal/tool/builtin) 提供五个基础文件/命令工具。

**阅读重点**：功能模块拥有自己的工具定义，只有 [`app/bootstrap.go`](internal/app/bootstrap.go) 负责选择并注册它们；注册表不依赖具体功能包。

### s03 · Permission：在副作用发生前设置闸门

**相对 s02 新增**：工具执行前增加 `deny -> rule -> ask` 三段式权限判断。硬拒绝规则优先；需要结合上下文判断的操作进入审批；其余操作放行。路径工具同时增加工作区边界检查。

**核心机制**：权限是 Host 的责任，而不是靠模型“自觉”。拒绝也必须作为正常 `tool_result` 返回，让模型知道操作未发生并重新规划。

**Go 落点**：

- [`Authorizer.Authorize`](internal/permission/permission.go#L51) 处理内置工具策略。
- [`workspace.Resolver`](internal/workspace/path.go) 解析路径并阻止逃逸工作区。
- [`app/hooks.go`](internal/app/hooks.go) 把权限判断接入 `PreToolUse`，避免污染主循环。
- Team 使用动态 Resolver，使工具始终绑定当前 assignment/worktree，而不是进程级固定目录。

**集成后的强化**：Go 版要求所有前台 Bash 都经过审批；Cron、Team Event 等自动 turn 无法安全读取用户输入，因此需要审批时直接 fail closed。简单 deny list 只能算防误操作机制，不能当作完整 Shell 沙箱。

### s04 · Hooks：把扩展点移出循环

**相对 s03 新增**：引入 `UserPromptSubmit`、`PreToolUse`、`PostToolUse`、`Stop` 四类生命周期事件。权限从循环中的硬编码判断迁移到 Hook，日志、结果检查和结束控制也可以独立注册。

**核心机制**：

- `PreToolUse` 可以阻止单次工具执行，并把原因返回模型。
- `PostToolUse` 观察已经产生的结果，适合日志或副作用处理。
- `Stop` 在模型不再调用工具时运行；返回 `block` 可把反馈追加到同一历史并继续循环。
- `UserPromptSubmit` 只处理新提交的用户输入，不与工具结果混淆。

**Go 落点**：[`hooks.Manager`](internal/hooks/hooks.go#L48) 保存回调；[`ToolExecutor`](internal/agent/tool_executor.go) 触发工具前后 Hook；[`Runner`](internal/agent/runner.go) 触发 Stop Hook；[`app/hooks.go`](internal/app/hooks.go) 组装权限、Memory 和 Goal 等具体行为。

**阅读重点**：Hook 是受控的生命周期端口，不是任意模块间通信总线。会改变工具批次或循环状态的行为仍由 `Runner` / `ToolExecutor` 明确编排。

### s05 · TodoWrite：增加会话内规划能力

**相对 s04 新增**：增加 `todo_write` 和 reminder 计数器。Todo 是当前会话的执行清单，不负责执行任务，也不持久化依赖图。连续三个工具轮次未更新清单时，Harness 向模型追加提醒。

**状态约束**：一次提交最多 20 项；每项必须有非空内容；状态只能是 `pending`、`in_progress`、`completed`；同一时间最多一个 `in_progress`；校验全部通过后才整体替换，避免部分更新。

**Go 落点**：[`todo.Manager.RunWrite`](internal/todo/todo.go#L45) 校验并原子替换列表，[`todo/tools.go`](internal/todo/tools.go) 声明工具，[`Runner`](internal/agent/runner.go) 维护 reminder 的轮次计数。

**与 s10 的边界**：Todo 面向“我接下来做什么”，生命周期跟随 Session；Task System 面向“哪些工作可被谁认领”，支持持久化、依赖和协作。两者不应合并成同一种数据结构。

### s06 · Subagent：用独立上下文处理子任务

**相对 s05 新增**：`task` 工具同步启动一个新的 Agent Loop。Subagent 使用全新的 `messages[]`，只把最终摘要作为一个 `tool_result` 返回父 Agent，中间读取和工具结果不会挤占父上下文。

**能力边界**：父子共享进程和工作区，所以这是消息隔离，不是权限沙箱；Subagent 只获得受限基础工具，不包含 `task`，因此不能递归委派；最多运行 30 轮。

**Go 落点**：

- [`subagent.Manager.RunTask`](internal/subagent/subagent.go#L30) 创建独立历史并执行有界循环。
- [`agent.Worker`](internal/agent/worker.go#L36) 被主 Agent、Subagent 和 Teammate 共同复用。
- Subagent Registry 在 [`app/bootstrap.go`](internal/app/bootstrap.go) 单独组装，不继承 Memory、MCP、Cron、Team 等主 Agent 能力。

**集成后的强化**：Subagent 会对输出截断做一次扩容重试；仍无法得到完整响应时返回错误，而不是把不完整工具调用交给执行器。

### s07 · Skill Loading：两阶段加载知识

**相对 s06 新增**：启动时扫描 `skills/*/SKILL.md`，只把名称和描述组成目录放进 system prompt；模型判断某个 Skill 适用时，再通过 `load_skill(name)` 读取完整内容。

```text
启动阶段：name + description -> system prompt
使用阶段：load_skill(name) -> 完整 SKILL.md -> tool_result
```

**核心机制**：用渐进披露减少每轮固定 token；`name` 只查询启动时建立的映射，不当作文件路径；frontmatter 决定可发现性，正文保留完整执行说明。

**Go 落点**：[`skill.New`](internal/skill/skill.go#L37) 扫描和解析 Skill，[`skill.Manager.RunLoad`](internal/skill/skill.go#L65) 按名称加载，[`prompt.Builder`](internal/prompt/prompt.go#L35) 将目录加入实时 system prompt。

**阅读重点**：Skill 是 Host 侧可发现的说明书，不是可执行插件；真正的能力仍来自注册工具和运行时模块。

### s08 · Context Compact：按信息损失逐层压缩

**相对 s07 新增**：为持续增长的 `messages[]` 增加固定顺序的压缩管线：

1. `tool_result_budget`：最新一批超大结果先完整落盘，消息中保留路径和预览。
2. `snip_compact`：消息过多时归档完整 transcript，保留头尾和归档标记。
3. `micro_compact`：优先替换模型已读过的旧工具结果，并保留可恢复路径。
4. `compact_history`：仍超限时才调用模型生成事实摘要。
5. `reactive_compact`：API 返回 `prompt_too_long` 后进行一次补救压缩。

**不变量**：不能切断 `assistant(tool_use)` 与紧随其后的 `user(tool_result)`；当前用户请求单独保存为 authoritative request，不能从任意 `role=user` 消息猜测；摘要和旧记忆只能作为 reference state。

**Go 落点**：[`compact.Manager.Prepare`](internal/compact/compact.go#L90) 执行分层整理，[`WriteTranscript`](internal/compact/compact.go#L304) 保存可恢复历史，[`agentctx.Manager.Prepare`](internal/agentctx/manager.go#L81) 在模型调用前统一接入，[`agent/recovery.go`](internal/agent/recovery.go) 处理 API 拒绝后的恢复。

**实现差异**：课程当前版本会先闭合整批工具结果再响应手动 `compact`；Go 版把它作为 Runner 控制工具，触发后停止当前工具批次并替换历史。这是已知语义差异，不应理解为逐行移植。

### s09 · Memory：保存跨会话的可复用知识

**相对 s08 新增**：把“当前会话如何变短”和“哪些事实下次还要记得”拆开。Memory 包含存储、召回、提取、整理四条路径：每条记录单独存为 Markdown，`MEMORY.md` 只做目录。

**运行机制**：请求开始时，辅助模型根据近期用户消息从目录中选择最多若干相关记忆，失败时降级为关键词匹配；主 Agent 完成后提取候选，只有 `persistent` 且非临时、非重复的信息才写盘；记录达到阈值后合并，失败则从快照恢复。

**Go 落点**：

- [`memory/types.go`](internal/memory/types.go) 定义 `Record`、`Type`、`Scope` 和辅助模型接口。
- [`store.go`](internal/memory/store.go) 管理文件、索引、快照与回滚。
- [`recall.go`](internal/memory/recall.go) 实现模型选择及关键词降级。
- [`extract.go`](internal/memory/extract.go) 执行持久性准入和去重。
- [`consolidate.go`](internal/memory/consolidate.go) 执行合并替换。

**阅读重点**：召回内容明确标为背景知识，不能覆盖当前请求；Memory 不是 transcript 备份，也不应保存一次性命令、临时路径或本轮限制。

### s10 · Task System：把清单升级为可协调 DAG

**相对 s09 新增**：引入持久化 Task、`blockedBy` 依赖、`owner` 和三个状态。状态机只有两类主要动作：`pending --claim--> in_progress --complete--> completed`。

**核心机制**：任务图必须分两阶段创建：先创建节点并取得运行时 ID，再添加依赖边，因为同一模型响应中的同级工具调用无法引用尚未返回的 ID。认领前校验所有前置任务已完成；完成后找出刚被解锁的下游任务。

**Go 落点**：[`task/types.go`](internal/task/types.go) 定义领域对象，[`task/task.go`](internal/task/task.go) 实现依赖校验和状态流转，[`task/store.go`](internal/task/store.go) 负责带文件锁的持久化，[`task/tools.go`](internal/task/tools.go) 提供创建、更新、查看、认领和完成工具。

**集成后的强化**：`Claim`、`Complete` 和解锁判断在锁内完成，避免多个 Teammate 同时认领同一任务；依赖目标、自依赖、环和 owner 不匹配都会被拒绝。

### s11 · Background Tasks：拆开工具结果与完成通知

**相对 s10 新增**：Bash 只有显式设置 `run_in_background` 才进入后台。启动时立即返回一个“已启动”占位 `tool_result`，命令完成后再通过独立通知唤醒主 Session；同一个 `tool_use_id` 不会产生第二个结果。

```text
tool_use -> placeholder tool_result -> Agent 继续工作
background process finishes -> runtime event -> 新的自动 turn
```

**Go 落点**：[`runtime.ShouldRunBackground`](internal/runtime/background.go#L54) 识别显式请求，[`BackgroundManager.Start`](internal/runtime/background.go#L60) 管理生命周期，[`Collect`](internal/runtime/background.go#L166) 收集完成通知，[`app/async_runtime.go`](internal/app/async_runtime.go) 将通知非阻塞投递给 `Session`。

**运行边界**：后台命令仍受超时、输出上限和进程组回收约束；通知是外部事件而不是用户新指令；进程退出前会清理仍在运行的任务。

### s12 · Cron Scheduler：把未来提示排入队列

**相对 s11 新增**：增加五段式 Cron 表达式、一次性/周期任务、持久化定义和到期队列。Cron 保存的是未来要交给 Agent 的 prompt，不直接保存并执行 Shell 命令。

**核心机制**：轮询器只负责把到期任务入队；只有 `Session` 空闲时才消费。投递采用 `Consume -> Session -> Acknowledge`，失败则 `Restore`，因此语义是至少一次而非恰好一次。

**Go 落点**：[`cron_expression.go`](internal/runtime/cron_expression.go) 解析表达式，[`CronScheduler`](internal/runtime/cron.go#L40) 管理 job 和 pending 队列，[`cron_store.go`](internal/runtime/cron_store.go) 持久化 durable job，[`cron_tools.go`](internal/runtime/cron_tools.go) 暴露调度工具。

**运行边界**：这是进程内调度器，不是系统 daemon；进程停止期间错过的触发不会补跑；定义可持久化，但实际投递仍依赖 Harness 运行且 Session 可用。

### s13 · Agent Teams：建立长生命周期协作协议

**相对 s12 新增**：从“一次性 Subagent”扩展为持续存在的 Teammate。每名成员拥有独立历史和 WORK/IDLE 循环，通过文件邮箱通信，从共享 Task DAG 中原子认领工作，并可绑定独立 Git Worktree。

**关键协议**：

- IDLE 时先消费邮箱，再扫描 ready task，避免控制消息被新工作饿死。
- “产出结果”和“进入 IDLE”是两个事件，Lead 不能把一次回复误认为成员已经退出。
- Plan Approval 与 Shutdown 使用类型、`request_id`、参与方、状态和 work identity 共同匹配，拒绝过期响应。
- 计划未批准时，Plan Gate 阻止写文件、编辑和 Bash 等变更型工具。
- Worktree 的创建/移除由 Host 管理；共享目录或 Worktree 都不是安全沙箱。

**Go 落点**：[`team.Runtime`](internal/team/runtime.go#L60) 管理成员生命周期，[`teammate.go`](internal/team/teammate.go) 实现 WORK/IDLE 循环，[`bus.go`](internal/team/bus.go) 实现邮箱，[`protocol.go`](internal/team/protocol.go) 管理请求匹配，[`gate.go`](internal/team/gate.go) 定义计划门禁，[`worktree.Manager`](internal/worktree/manager.go#L40) 管理工作树。

**解耦方式**：Team 通过 [`TaskBoard` / `WorkspaceProvider`](internal/team/ports.go) 使用任务与工作区能力；Worktree 通过 [`TaskBindings`](internal/worktree/ports.go) 更新绑定，避免相互依赖具体实现。

### s14 · MCP Tools：运行时发现外部工具

**相对 s13 新增**：增加 `connect_mcp`。连接后先 discovery，再把远端工具的定义和 handler 以 `mcp__{server}__{tool}` 注册进主 Agent Registry；下一轮模型调用自然就能看到新工具，基础循环不需要改变。

**安全与一致性**：名称需规范化并限制长度；整批 Schema 和冲突检查全部通过后才提交注册，避免半连接状态；外部输入和调用错误被封装为 `tool_result`；只有 Host Policy 能授权，MCP Server annotation 不能自行获得权限。

**Go 落点**：[`mcp.Client`](internal/mcp/client.go) 保存 discovery 结果和调用入口，[`mcp.Manager.Connect`](internal/mcp/manager.go#L75) 完成连接与事务式注册，[`mcp/tools.go`](internal/mcp/tools.go) 声明连接工具，[`app/hooks.go`](internal/app/hooks.go) 将外部工具纳入权限管线。

**当前边界**：`docs` 和 `deploy` 是进程内 Mock Server，只模拟 `tools/list` 与 `tools/call`，尚未实现 stdio/HTTP 等真实 MCP Transport；动态工具只提供给主 Agent，不自动扩散到 Subagent 或 Teammate。

### s15 · Integrated Harness：把所有机制放回一个循环

**相对 s14 新增**：前十四课不再各自运行，而是由一个组合根接入同一个 Session、Runner 和 Registry。关键不是继续增加功能，而是明确消息来源、权威请求、权限模式、动态 Prompt 和恢复策略之间的优先级。

**集成主线**：

1. [`Session`](internal/agent/session.go#L25) 串行化用户输入与 Background/Cron/Team 自动事件。
2. [`agentctx.Manager.StartRequest`](internal/agentctx/manager.go#L56) 每个请求只召回一次 Memory。
3. [`prompt.Builder`](internal/prompt/prompt.go) 每轮刷新时间、工具、MCP Server 和 Teammate 等实时状态。
4. [`agentctx.Manager.Prepare`](internal/agentctx/manager.go#L81) 在模型调用前执行上下文预算。
5. [`Worker`](internal/agent/worker.go) 调模型并通过 [`ToolExecutor`](internal/agent/tool_executor.go) 执行工具。
6. 无真实 `tool_use` 时进入 Stop Hook；允许退出或把反馈放回同一历史继续。

**恢复与安全边界**：[`agent/recovery.go`](internal/agent/recovery.go) 对 429/529 做最多三次带 jitter 的指数退避，连续 529 可切换备用模型；输出上限可从 8000 扩到 16000，纯文本最多续写两次；工具调用仍不完整则报错。自动 turn 不允许弹出 stdin 审批。Bash 最长运行 120 秒、最多返回 50KB，并在超时或退出时回收 process group。

**请求归属**：`Session` 单独保存当前 authoritative request。Background 和 Team Event 只能补充运行时事实，不能覆盖用户任务；Cron prompt 只在该次自动 turn 临时成为活动请求。压缩时请求和不可信的历史摘要分别写入 `Authoritative request` 与 `Reference state`。

**组合根**：[`app.NewWithConfig`](internal/app/bootstrap.go#L45) 构造各模块并显式注入依赖，[`app/app.go`](internal/app/app.go) 管理进程生命周期，[`app/cli.go`](internal/app/cli.go) 只负责交互协议。

### s16 · Workflow Runtime：由脚本控制确定性编排

**相对 s15 新增**：把复杂任务从“模型一轮轮临时决定”提升为 Host 注册的可信 Workflow。模型只能选择 Workflow 名称和参数，不能提交任意可执行脚本；Workflow 内部再使用无工具的模型调用完成结构化子任务。

**编排语义**：`Parallel` 等待整组任务完成后继续，是 barrier；`Pipeline` 只保证每个 item 的 stage 顺序，不要求不同 item 同步跨阶段。子调用必须返回符合 JSON Schema 的结果，校验失败只重试一次。

**持久化与恢复**：snapshot 保存运行定义和状态，JSONL journal 保存每个模型调用结果；稳定 key 由 label、prompt 和 Schema 构成。`resume` 会先核对 Workflow 名称和原始参数，命中 journal 的结果重新校验后复用。snapshot/output 原子替换，journal append 后同步，进程锁与文件锁共同防止同一 run 并发恢复。

**Go 落点**：[`workflow.Manager.Run`](internal/workflow/manager.go#L67) 管理完整调用，[`Execution`](internal/workflow/execution.go) 提供并发原语和共享限额，[`ModelRunner`](internal/workflow/runner.go#L34) 执行结构化模型请求，[`Store`](internal/workflow/store.go#L47) 管理 snapshot/journal/output，[`sample.go`](internal/workflow/sample.go) 注册 `review-changes` 示例。

**运行边界**：Workflow 对主 Agent 是同步的一次工具调用；`async_launched` 只是结果字段，不进入 Background 事件系统。所有并发调用共享 semaphore、agent cap 和 token budget，最多嵌套一层；只有最终 JSON 进入主会话。当前 `review-changes` 要求通过 `args.changes` 显式提供材料，Workflow agent 不会自行读取工作区。

### s17 · Goal Loop：把“是否完成”交给独立判断器

**相对 s16 新增**：`/goal <condition>` 设置 Session 级完成条件。主模型没有工具调用时只是“提出停止”；独立 evaluator 读取 Goal 和执行证据，决定完成、继续或不可继续。

**控制流**：

- `ok=false`：Stop Hook 返回 `block`，理由作为新消息追加到原 `messages[]`，主 Agent 在同一个循环继续。
- Background 仍运行，或 Teammate 处于 working、stopping、waiting_approval：返回 `defer`，等待事件进入原 Session 后再评估。
- `ok=true`：允许本轮结束；`impossible=true`：向 Host 报告当前条件无法达成。

**Go 落点**：[`goal.Controller`](internal/goal/controller.go#L18) 保存 Goal 状态和退出上限，[`EvaluateAfterTurn`](internal/goal/controller.go#L138) 实现 Stop 决策，[`PromptEvaluator`](internal/goal/evaluator.go#L18) 用无工具模型解析严格的 `{ok, reason, impossible?}` JSON，[`transcript.go`](internal/goal/transcript.go) 保留近期文本及工具证据。

**运行边界**：evaluator 的请求、响应和 token 不进入主 Session；transcript 默认保留最近 24,000 字符但不能丢掉关键 `tool_use/tool_result` 证据；达到 `MAX_TURNS`、连续 block 上限或 evaluator 出错时保留活动 Goal，交由用户检查、继续、替换或清理。`/goal` 可查看状态，清理命令不会启动模型 turn；`goal_status` Event 和 `goal.Restore` 为 Host 提供持久化接口，但当前 CLI 不会跨进程自动恢复 Goal。

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

## 日志与注释

运行日志统一使用：

- `logger.Debug(format, args...)`
- `logger.Info(format, args...)`
- `logger.Warn(format, args...)`
- `logger.Error(format, args...)`

标准格式为 `日期 时间 [Level] 原内容`。CLI、最终回答、Todo 展示和权限询问属于交互输出，不使用 Logger。

核心注释统一使用中文，重点解释消息协议、并发、持久化和状态机约束；Agent、LLM、ContentBlock、tool_use、tool_result、Cron 等专业术语保留英文。

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
