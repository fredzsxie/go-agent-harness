# go-agent-harness

Go 版 Claude Code agent harness 学习项目。

本项目参考 `~/Documents/Code/learn-claude-code` 的新版根目录课程，用 Go 逐章实现 coding agent harness。当前课程主线为 **s01–s17**；`docs/` 和 `agents/` 下的旧 12 章内容只用于兼容旧链接，不作为本项目的学习基线。

核心认识不变：

```text
Agent = Model + Harness

Harness = tools + knowledge + context + permissions + runtime
```

模型负责判断下一步行动，harness 负责提供上下文、工具、执行边界和持久化运行环境。

## 当前进度

当前学习进度到 **s15 Integrated Harness**。s01–s15 的主体能力已经接入同一条 Go 版 Agent Loop，并对齐了新版课程中影响正确性的主要边界；s16–s17 暂不实现。

| 章节 | 主题 | 状态 | Go 项目落点 |
|---|---|---|---|
| s01 | Agent Loop | 已完成 | `internal/agent/runner.go`, `internal/model/anthropic.go` |
| s02 | Tool Use | 已完成 | `internal/agent/registry.go`, `internal/tool/builtin/` |
| s03 | Permission | 已完成 | `internal/permission/` |
| s04 | Hooks | 已完成 | `internal/hooks/`, `internal/agent/tool_executor.go` |
| s05 | TodoWrite | 已完成 | `internal/todo/` |
| s06 | Subagent | 已完成 | `internal/subagent/` |
| s07 | Skill Loading | 已完成 | `internal/skill/`, `internal/prompt/` |
| s08 | Context Compact | 已完成 | `internal/compact/`, `internal/agent/runner.go` |
| s09 | Memory | 已完成 | `internal/memory/`, `internal/agent/runner.go` |
| s10 | Task System | 已完成 | `internal/task/`, `.tasks/` |
| s11 | Background Tasks | 已完成 | `internal/runtime/`, `internal/agent/runner.go` |
| s12 | Cron Scheduler | 已完成 | `internal/runtime/cron.go`, `internal/app/app.go` |
| s13 | Agent Teams | 已完成 | `internal/team/`, `internal/worktree/`, `internal/app/app.go` |
| s14 | MCP Tools | 已完成 | `internal/mcp/`, `internal/app/` |
| s15 | Integrated Harness | 已完成 | `internal/app/`, `internal/agent/`, `internal/agentctx/` |
| s16 | Workflow Runtime | 仅占位 | `internal/workflow/` |
| s17 | Goal Loop | 仅占位 | `internal/goal/` |

详细的代码映射、验收边界和后续计划见 [learn-claude-code-go-reference.md](./learn-claude-code-go-reference.md)。

## 已实现的核心行为

### 一条稳定的 Agent Loop

```text
messages[] -> LLM -> assistant content
                         |
                  contains tool_use?
                    /          \
                  yes           no
                   |             |
            execute tools     run Stop hooks
            append results    return final text
                   |
              loop again
```

循环根据实际 `tool_use` block 决定是否执行工具，不依赖兼容接口可能不准确的 `stop_reason`。空的 `tool_use` 响应不会生成空 `tool_result` 回合。

### Anthropic 消息与 Content Block

以下说明以 Anthropic 官方 [Messages API](https://platform.claude.com/docs/en/api/messages/create) 和 [Tool call handling](https://platform.claude.com/docs/en/agents-and-tools/tool-use/handle-tool-calls) 为准。这里讨论的是本项目使用的客户端工具协议；服务端工具、图片、文档、thinking 等其他 block 暂不在当前实现范围内。

#### Anthropic 消息格式

Messages API 请求的核心结构如下：

```json
{
  "model": "claude-sonnet-4-6",
  "max_tokens": 8000,
  "system": "You are a coding agent.",
  "messages": [
    {
      "role": "user",
      "content": "读取 README.md"
    }
  ],
  "tools": []
}
```

`messages` 是按时间排序的消息数组。每条消息至少包含：

| 字段 | 含义 |
|---|---|
| `role` | 消息角色，当前项目支持 `user` 和 `assistant` |
| `content` | 一个字符串，或由多个 content block 组成的数组 |

字符串 content 是单个 `text` block 的简写，因此下面两种写法等价：

```json
{"role":"user","content":"你好"}
```

```json
{"role":"user","content":[{"type":"text","text":"你好"}]}
```

模型响应本身也是一条 `assistant` 消息，但外层还包含 `id`、`model`、`stop_reason` 和 token usage 等元数据；真正需要加入历史的是它的 `role` 与 `content`。官方协议按 `user`、`assistant` 对话轮次工作，相邻的同角色输入可能被 API 合并。当前项目始终通过请求顶层的 `system` 参数传递系统提示，不把它保存为普通历史消息。

Content block 是由 `type` 区分结构的联合类型。当前项目只处理下面三种：

| block 类型 | 所在角色 | Anthropic 字段 | 用途 |
|---|---|---|---|
| `text` | `user` 或 `assistant` | `type`, `text` | 普通文本 |
| `tool_use` | `assistant` | `type`, `id`, `name`, `input` | 模型请求客户端执行工具 |
| `tool_result` | `user` | `type`, `tool_use_id`, `content`, `is_error?` | Harness 返回对应工具的执行结果 |

工具调用必须满足以下关系：

1. `assistant` 返回一个或多个 `tool_use` block。
2. Harness 使用 `name` 分发工具，并把 `input` 作为参数。
3. 紧接着追加一条 `user` 消息，其中每个 `tool_result.tool_use_id` 必须匹配对应的 `tool_use.id`。
4. 一次 `tool_use` 只返回一次 `tool_result`。如果同一轮有多个调用，下一条 `user` 消息应包含全部匹配结果。
5. 如果一条 `user` 消息同时包含工具结果和普通文本，所有 `tool_result` block 必须排在文本 block 前面。当前项目的后台 `<task_notification>` 会遵守此顺序追加为文本，并且不复用原来的 `tool_use_id`。

一个完整的工具往返在 Anthropic JSON 中类似：

```json
[
  {
    "role": "user",
    "content": [{"type": "text", "text": "读取 README.md 的前 20 行"}]
  },
  {
    "role": "assistant",
    "content": [
      {"type": "text", "text": "我先读取文件。"},
      {
        "type": "tool_use",
        "id": "toolu_01",
        "name": "read_file",
        "input": {"path": "README.md", "limit": 20}
      }
    ]
  },
  {
    "role": "user",
    "content": [
      {
        "type": "tool_result",
        "tool_use_id": "toolu_01",
        "content": "# go-agent-harness\n...",
        "is_error": false
      }
    ]
  },
  {
    "role": "assistant",
    "content": [{"type": "text", "text": "README 的前 20 行已读取。"}]
  }
]
```

#### 当前项目中的 Message 与 ContentBlock

本项目使用 [internal/protocol/message.go](./internal/protocol/message.go) 中的轻量类型保存消息历史：

```go
type Message struct {
    Role    Role
    Content string
    Blocks  []ContentBlock
}

type ContentBlock struct {
    Type      BlockType
    Text      string
    ToolUseID string
    ToolName  string
    Input     map[string]any
    IsError   bool
}
```

二者关系为：

```text
[]Message                         一段按时间排序的会话
└── Message                       一个 user 或 assistant 回合
    ├── Content string            纯文本消息的便捷表示
    └── Blocks []ContentBlock     结构化内容；一个回合可以包含多个 block
        ├── text
        ├── tool_use
        └── tool_result
```

字段映射如下：

| 当前项目字段 | `type=text` | `type=tool_use` | `type=tool_result` |
|---|---|---|---|
| `Text` | Anthropic `text` | 不使用 | Anthropic `content` 文本 |
| `ToolUseID` | 不使用 | Anthropic `id` | Anthropic `tool_use_id` |
| `ToolName` | 不使用 | Anthropic `name` | 不使用 |
| `Input` | 不使用 | Anthropic `input` | 不使用 |
| `IsError` | 不使用 | 不使用 | Anthropic `is_error` |

`Message.Content` 与 `Message.Blocks` 是两种内部表示，不应同时当成两份内容发送：

- `Blocks` 为空时，[Anthropic 消息适配层](./internal/model/anthropic_message.go) 把非空 `Content` 转成一个 `text` block。
- `Blocks` 非空时以 `Blocks` 为准，`Content` 不会再次发送。模型响应解析时仍会把所有文本汇总到 `Content`，方便提取最终回答。
- Anthropic 响应适配当前只解析 `text` 和 `tool_use`；其他 block 类型尚未进入本地消息历史。
- 本地字段名是统一存储模型，不是可直接发送的 Anthropic JSON。例如本地 `tool_use.ToolUseID` 必须由适配层转换为 API 的 `id`，`ToolName` 必须转换为 `name`。

上面的工具调用在本项目中可表示为：

```go
messages := []protocol.Message{
    {
        Role:    protocol.RoleUser,
        Content: "读取 README.md 的前 20 行",
    },
    {
        Role: protocol.RoleAssistant,
        Blocks: []protocol.ContentBlock{
            {Type: protocol.BlockText, Text: "我先读取文件。"},
            {
                Type:      protocol.BlockToolUse,
                ToolUseID: "toolu_01",
                ToolName:  "read_file",
                Input:     map[string]any{"path": "README.md", "limit": 20},
            },
        },
    },
    {
        Role: protocol.RoleUser,
        Blocks: []protocol.ContentBlock{
            {
                Type:      protocol.BlockToolResult,
                ToolUseID: "toolu_01",
                Text:      "# go-agent-harness\n...",
                IsError:   false,
            },
        },
    },
}
```

发送请求前，[Anthropic 消息适配层](./internal/model/anthropic_message.go) 会把内部对象转换成 Anthropic SDK 类型；收到响应后再转回本地结构。Agent Loop、Compact、Memory 与 Subagent 因此共享同一套消息协议，API 字段差异只存在于模型适配层。

### 统一工具池

内置工具包括：

- `bash`
- `read_file`，支持 UTF-8 和可选行数限制
- `write_file`
- `edit_file`
- `glob`，支持递归 `**` 并限制最大返回数量
- `todo_write`
- `task`，启动一次性 subagent
- `load_skill`
- `compact`
- `create_task`、`update_task`、`list_tasks`、`get_task`、`claim_task`、`complete_task`
- `schedule_cron`、`list_crons`、`cancel_cron`
- `spawn_teammate`、`list_teammates`、`send_message`、`request_shutdown`
- `request_plan`、`review_plan`、`create_worktree`
- `connect_mcp`，连接课程内置的 Mock MCP Server
- 连接后动态加入的 `mcp__{server}__{tool}`

所有工具通过 `agent.Registry` 注册，Runner 不关心具体工具来源。Registry 在每轮模型调用前生成最新工具列表，因此 `connect_mcp` 发现的工具会从下一轮开始生效。

### 权限与 Hooks

工具执行顺序为：

```text
PreToolUse permission/log hooks
        -> Registry.Dispatch
        -> PostToolUse hooks
        -> tool_result
```

硬拒绝列表中的危险命令始终禁止；其他 Bash 命令也必须在前台用户 turn 中获得确认。Cron、Team 事件和后台结果唤醒的自动 turn 通过 `context.Context` 标记为非交互，需要终端确认的 Bash 或 MCP 操作会 fail closed，不会与 CLI 争抢 stdin。文件工具仍受 workspace path resolver 约束。MCP 权限只信任 Host Policy，不把 Server 提供的 annotation 当作授权。

### Todo 与 Subagent

TodoWrite 支持数组以及部分兼容服务返回的数组字符串，强制最多 20 项、最多一个 `in_progress`，无效更新不会覆盖已有状态。连续三个工具回合未更新 Todo 时，提醒会附在第三个工具结果批次中。

Subagent 使用独立的 `messages[]` 和受限工具池，不会继续派生子 agent，只把最终文本返回给父 agent。

### Skill、Compact 与 Memory

- Skill：启动时只加载目录，正文通过 `load_skill` 按需读取。
- Compact：先处理超大工具结果，只有上下文超出预算时才依次执行 snip、micro 和摘要压缩；未被模型消费的最新工具结果批次不会被提前裁剪。压缩消息中只有 `Authoritative request` 是指令，摘要放在不可信的 `Reference state` 中。
- Memory：每条记忆独立存为 Markdown；每轮先选择相关记忆，结束后提取长期信息，并在达到阈值时整理；仅持久信息可以写入，临时任务状态和重复内容会被过滤。

### Background Tasks

只有显式设置 `bash.run_in_background=true` 的命令才会异步执行。工具调用会立即返回 `bg_id`；命令完成后 `BackgroundManager` 会唤醒统一自动运行时，并在 Session 空闲时将 `<task_notification>` 注入同一消息历史。后台 `PostToolUse` 在真实命令完成后触发，而不是在返回占位结果时触发。

Bash 前台与后台路径共用同一执行器：默认最长运行 120 秒，最多向模型返回 50KB 输出，并使用独立 process group 在超时或结束时回收子进程。退出应用时会取消仍在运行的后台命令。

### Cron Scheduler

`schedule_cron`、`list_crons` 和 `cancel_cron` 用于管理五段式本地时间计划。支持 `*`、`*/N`、单值、范围和逗号列表；到期任务先进入待投递队列，Agent 空闲后才以 `[Scheduled] prompt` 开始新 turn。同一分钟不会重复入队，周期任务确认投递后等待下次匹配，一次性任务确认后删除。

默认任务为 recurring 且 durable，持久化到 `.scheduled_tasks.json` 并使用临时文件原子替换。重启会恢复任务定义及尚未确认的投递，但不会补跑进程关闭期间错过的时间。定时 turn 不允许弹出交互式权限确认，需要确认的操作会直接拒绝。

s11 与 s12 同放在 `internal/runtime/`，但分别由 `BackgroundManager` 与 `CronScheduler` 管理。前者负责执行和收集后台命令，后者负责未来时间的调度与持久化；二者只共享应用生命周期，不共享状态。

### Agent Teams

Lead 可以在用户确认团队方案后，通过 `spawn_teammate` 启动拥有独立消息历史和工具池的持久化 Teammate。Teammate 在 WORK 与 IDLE 之间循环：有直接消息时优先处理邮箱，没有消息时才扫描共享 Task Board 并原子认领 ready Task。

```text
Lead / App
  ├─ .mailboxes/<name>.jsonl ──> Teammate WORK
  ├─ .tasks/<id>.json         ──> IDLE 自动认领
  └─ Team events              <── result / idle / protocol response
                                      |
                               Session 空闲后启动 Lead turn
```

普通协作消息、`result`、`idle_notification` 和控制事件通过文件邮箱传递，不共享 Lead 与 Teammate 的 `messages[]`。App 是 `lead` 邮箱的唯一消费者；Lead 忙碌时事件保留在内存待投递队列，随后通过 `Session.TrySubmit` 重试，不要求模型轮询 inbox。

Shutdown 与 Plan Approval 使用 `request_id` 关联请求和响应，并校验协议类型、发送方、接收方和状态，避免错配或重复响应改变状态。启用 Plan Gate 后，计划获批前禁止 Teammate 使用 Bash、写入、编辑或完成 Task；审批还会绑定当前 Task ID 和工作版本，过期计划不能解锁新任务。

Task 可以选择绑定 `.worktrees/<name>`，对应分支为 `wt/<name>`。Teammate 的 Bash 和文件工具会动态使用 Task Workspace；绑定损坏时直接失败，不回退主仓库。Worktree 只隔离 Git 工作目录和分支，不是安全沙箱。Task 完成后由 Host 决定检查、合并或删除 Worktree，模型没有删除工具。

Lead 与 Teammate 工具边界：

- Lead 可以创建/更新 Task、创建 Worktree、启动和管理 Teammate。
- Teammate 只能读取 Task、认领、完成、发送消息和提交计划。
- Teammate 不会获得 `update_task`、Cron、Subagent、Compact 或 Worktree 删除能力。
- `result` 与 `idle_notification` 分开投递，分别表示工作产出和可再次接单状态。

测试用例：
请为一次只读的 Agent Teams 验证提出一个两人团队方案：alice 计算17*19，bob 计算5的阶乘。不要修改任何文件，先只给出方案并等待我确认。

### MCP Tools

`connect_mcp` 负责连接一个课程内置的进程内 Mock Server，并模拟 MCP 的 `tools/list` 与 `tools/call` 边界。当前提供：

- `docs`：`search`、`get_version`
- `deploy`：`status`、`trigger`

本章不实现 stdio、HTTP 或 SSE Transport，也不会启动外部 MCP 进程。连接后的调用链如下：

```text
connect_mcp("docs")
        -> MCP Manager discovery
        -> 校验 Schema / 名称 / 冲突
        -> Registry 注册 mcp__docs__search 等工具
        -> 下一轮 LLM 获得最新 Tools 与 System Prompt
        -> PreToolUse Host Policy
        -> MCPClient.CallTool("search", input)
        -> tool_result
```

模型侧名称统一为 `mcp__{server}__{tool}`。Server 和 Tool 名称中不符合 `[a-zA-Z0-9_-]` 的字符会转换为 `_`；规范化后发生冲突或最终名称超过 64 字符时，整个连接失败且不会留下半注册状态。调用缺少参数、包含 Mock Handler 不接受的参数或执行失败时，会返回带 `is_error` 的 `tool_result`，Agent Loop 可以在下一轮修正输入。

默认 Host Policy：

| MCP 工具 | 策略 |
|---|---|
| `mcp__docs__search` | allow |
| `mcp__docs__get_version` | allow |
| `mcp__deploy__status` | allow |
| `mcp__deploy__trigger` | confirm |
| 其他 MCP 工具 | confirm |

可以使用以下请求验证动态发现：

```text
连接 docs server，搜索 agent hooks，并告诉我当前 documentation API version。
```

### Integrated Harness

s15 不再引入独立的业务 package，而是把已有机制收敛到同一条运行链：

```text
user / cron / team / background event
        -> Session（串行化 + 权威请求）
        -> 注入后台结果
        -> context budget / compact
        -> 刷新 time + tools + MCP + teammates + memory Prompt
        -> LLM recovery
        -> tool_use ?
             yes -> PreToolUse / permission -> dispatch -> PostToolUse -> tool_result
             no  -> Stop Hook -> memory finalize -> return
```

`internal/app/async_runtime.go` 统一处理 Cron、Lead 邮箱和后台完成信号。它通过 `Session.TrySubmit` 非阻塞占用主 Agent；忙碌时保留待投递状态，空闲后重试。三种事件可合并到一次自动 turn，不会各自创建互相竞争的 Agent Loop。

Session 单独保存当前用户请求。Team 事件和 `<task_notification>` 是事件数据，不会覆盖该请求；Cron 只在当次自动 turn 使用 `Run scheduled task: ...` 作为权威请求。当历史需要摘要时，当前请求与历史参考会被明确分区，避免历史中的文本重新获得授权。

LLM 恢复策略：

| 情况 | 处理 |
|---|---|
| HTTP 429 | 最多 3 次指数退避重试，保持主模型 |
| HTTP 529 | 指数退避；连续两次后可切换 `FALLBACK_MODEL_ID` |
| `stop_reason=max_tokens` | 8000 提升到 16000；仍截断时最多进行两次断点续写 |
| prompt too long | 执行一次 reactive compact 后重试 |
| Context 取消 | 立即停止，不重试 |

`max_tokens` 截断时即使响应已包含 `tool_use`，Harness 也不会执行可能不完整的参数，而是先恢复完整响应。

### 日志

运行日志统一使用 `logger.Debug`、`logger.Info`、`logger.Warn` 和 `logger.Error`。通过 `LOG_MODE` 设置最低输出等级，默认为 `info`；例如 `warn` 只输出 Warn 和 Error，只有 `debug` 会输出 Debug。格式为本地日期时间、Level 和原日志内容，例如：

| `LOG_MODE` | 输出等级 |
|---|---|
| `debug` | Debug、Info、Warn、Error |
| `info` | Info、Warn、Error |
| `warn` | Warn、Error |
| `error` | Error |

```text
2026/09/02 15:04:05 [INFO] [Background] started bg_0001: go test ./...
2026/09/02 15:04:05 [WARN] [cron] agent busy, retry later
2026/09/02 15:04:06 [ERROR] [cron] delivery failed: context canceled
```

CLI 提示、Agent 最终回答、Todo 展示和权限确认属于交互输出，不添加日志时间或 Level。

### 注释规范

核心逻辑注释统一使用中文，重点说明协议约束、并发边界、状态迁移和错误恢复原因，不逐行复述实现。Agent、LLM、Session、ContentBlock、tool_use、tool_result、Hook、Cron、Anthropic 等专业术语保留英文。

## 项目结构

```text
go-agent-harness/
├── main.go            # 程序入口
└── internal/
    ├── config/       # 环境与模型配置
    ├── app/          # CLI、统一自动事件运行时与依赖装配
    ├── agent/        # Session、Agent Loop、恢复、Registry 与 ToolExecutor
    ├── protocol/     # Message 与 ContentBlock 协议
    ├── model/        # Model 接口的 Anthropic 适配
    ├── agentctx/     # Prompt、Compact 与 Memory 编排
    ├── tool/builtin/ # Shell 与文件工具
    ├── hooks/        # s04
    ├── permission/   # s03
    ├── todo/         # s05
    ├── subagent/     # s06
    ├── skill/        # s07
    ├── compact/      # s08
    ├── memory/       # s09
    ├── prompt/       # 运行时 prompt 组装
    ├── task/         # s10 持久化任务图
    ├── runtime/      # s11 Background 与 s12 Cron
    ├── logger/       # 带时间及 Debug/Info/Warn/Error Level 的极简日志
    ├── team/         # s13 Mailbox、Protocol、Plan Gate 与 Teammate Runtime
    ├── worktree/     # s13 Task 绑定的 Git Worktree
    ├── mcp/          # s14 MCP discovery、Mock Server、动态工具与 Host Policy
    ├── workflow/     # s16 仅占位
    └── goal/         # s17 仅占位
```

## 快速开始

准备 `.env`：

```bash
ANTHROPIC_API_KEY=your_api_key
ANTHROPIC_BASE_URL=https://api.anthropic.com
MODEL_ID=claude-sonnet-4-6
# 可选：主模型连续返回 529 时切换
FALLBACK_MODEL_ID=your_fallback_model_id
LOG_MODE=info
```

运行：

```bash
go run .
```

验证：

```bash
GOCACHE=/private/tmp/go-agent-harness-go-cache go test ./...
GOCACHE=/private/tmp/go-agent-harness-go-cache go vet ./...
GOCACHE=/private/tmp/go-agent-harness-go-cache go test -race ./...
```

## 下一步

下一课是新版 **s16 Workflow Runtime**。当前只保留 `internal/workflow/` 与 `internal/goal/` 占位，不实现 s16–s17 的具体运行逻辑。
