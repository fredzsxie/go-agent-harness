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

当前学习进度到 **s17 Goal Loop**。s01–s17 的主体能力已经接入同一条 Go 版 Agent Loop；具体边界与 Go 版实现差异见下方课程对照文档。

| 章节 | 主题 | 状态 | Go 项目落点 |
|---|---|---|---|
| s01 | Agent Loop | 已完成 | `internal/agent/runner.go`, `internal/model/anthropic.go` |
| s02 | Tool Use | 已完成 | `internal/tool/registry.go`, `internal/tool/builtin/` |
| s03 | Permission | 已完成 | `internal/permission/` |
| s04 | Hooks | 已完成 | `internal/hooks/`, `internal/agent/tool_executor.go` |
| s05 | TodoWrite | 已完成 | `internal/todo/` |
| s06 | Subagent | 已完成 | `internal/subagent/` |
| s07 | Skill Loading | 已完成 | `internal/skill/`, `internal/prompt/` |
| s08 | Context Compact | 已完成 | `internal/compact/`, `internal/agentctx/` |
| s09 | Memory | 已完成 | `internal/memory/`, `internal/agentctx/` |
| s10 | Task System | 已完成 | `internal/task/`, `.tasks/` |
| s11 | Background Tasks | 已完成 | `internal/runtime/`, `internal/agent/runner.go` |
| s12 | Cron Scheduler | 已完成 | `internal/runtime/cron.go`, `internal/app/app.go` |
| s13 | Agent Teams | 已完成 | `internal/team/`, `internal/worktree/`, `internal/app/app.go` |
| s14 | MCP Tools | 已完成 | `internal/mcp/`, `internal/app/` |
| s15 | Integrated Harness | 已完成 | `internal/app/`, `internal/agent/`, `internal/agentctx/` |
| s16 | Workflow Runtime | 已完成 | `internal/workflow/`, `internal/app/` |
| s17 | Goal Loop | 已完成 | `internal/goal/`, `internal/hooks/`, `internal/app/` |

详细的代码映射、验收边界和后续计划见 [learn-claude-code-go-reference.md](./learn-claude-code-go-reference.md)。

## 建议阅读顺序

先读 [课程与代码对照](./learn-claude-code-go-reference.md)，再从以下入口追踪一轮请求：

1. `internal/app/bootstrap.go`：统一注入模型、工作区、权限和各角色的工具池。
2. `internal/agent/session.go → runner.go → worker.go → tool_executor.go`：输入串行化、循环、单轮调用、工具回填。
3. `internal/protocol/`、`internal/llm/`、`internal/tool/`：共享协议，不依赖业务功能。
4. 按 s01–s17 映射阅读功能模块；优先看入口注释和相邻的 `*_test.go`。

构造应用时可通过 `app.NewWithConfig` 注入 `WorkDir / Model / In / Out`，不需要修改进程工作目录或连接真实模型。初始化错误由调用方处理，不在功能包中退出程序。

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
- `workflow`，运行 Host 注册的固定编排并支持 journal 恢复

所有工具通过 `tool.Registry` 注册；schema 与 handler 放在各功能包的 `tools.go` / `register.go`，`app/bootstrap.go` 只负责选择并组合能力。Runner 不关心普通工具的具体来源。Registry 在每轮模型调用前生成最新工具列表，因此 `connect_mcp` 发现的工具会从下一轮开始生效。

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
| `stop_reason=max_tokens` | 8000 提升到 16000；主 Runner 的纯文本最多续写两次，残缺工具调用则报错返回 |
| prompt too long | 执行一次 reactive compact 后重试 |
| Context 取消 | 立即停止，不重试 |

`max_tokens` 截断时即使响应已包含 `tool_use`，Harness 也不会执行可能不完整的参数，而是先恢复完整响应。Subagent / Teammate 通过 Worker 从同一份输入扩容重试一次；再次截断就返回错误，不把残缺片段写入历史。

### Workflow Runtime

s16 在现有主 Agent 工具池中加入 `workflow`。模型只能选择 Host 已注册的 Workflow 名称、传入 JSON 参数以及可选的恢复 run ID，不能提交脚本、Metadata 或任意可执行代码。

```text
main Agent tool_use: workflow
        -> PreToolUse / permission
        -> 解析 name、args、resume_from_run_id
        -> Host Registry 获取可信 Script
        -> snapshot + journal + run lock
        -> Script 编排多个受限 Workflow agent
        -> output + task_notification
        -> 一个最终 tool_result 返回 main Agent
```

模型侧输入格式：

```json
{
  "name": "review-changes",
  "args": {
    "changes": "diff --git a/main.go b/main.go ...",
    "budget": 12000
  }
}
```

恢复中断运行时使用：

```json
{
  "name": "review-changes",
  "resume_from_run_id": "wf_review-changes_0123456789abcdef"
}
```

省略 `args` 会复用原始参数；如果重新传入 args，则必须与原始值完全一致。未知字段、未知 Workflow、非法 run ID 或不匹配的参数会成为带 `is_error` 的普通 `tool_result`，主 Agent 可以在下一轮修正。

可信 Script 可使用的编排原语：

| 原语 | 行为 |
|---|---|
| `Agent` | 执行一次无工具的受限模型调用，可要求结构化输出 |
| `Parallel` | 并发执行全部 Step，等待所有 Step 结束后返回，结果保持输入顺序 |
| `Pipeline` | 每个 item 独立依次通过所有 Stage，不设置跨 item 的 Stage barrier |
| `Phase` | 记录一次去重后的阶段进度 |
| `Log` | 记录 Workflow 进度，不写入普通会话历史 |
| `Workflow` | 内联运行另一个已注册 Workflow，最多嵌套一层 |

Workflow agent 复用 `llm.Model`，但不会获得 Bash、文件、MCP、Subagent 或其他工具，只能读取 Script 通过 prompt 明确传入的内容。带 Schema 的输出会先解析并校验；失败时仅追加一次 JSON 提示重试，第二次仍不合法则结束本次 Workflow。当前 Schema 子集支持 object、array、string、boolean、number、required、properties、items、enum 和 `additionalProperties`。

默认每个 run 最多调用 1000 次 agent，最多同时运行 8 次模型请求。可通过正整数 `args.budget` 设置共享 token budget；嵌套 Workflow 共享调用次数、并发限制、预算、journal 和 Task usage。`review-changes` 是当前内置示例，会对 correctness、security、performance、style 四个维度执行 audit → verify pipeline，并按严重程度汇总确认的问题。

#### 同步工具调用与内部并发

`workflow` 对主 Agent 来说仍是一次同步工具调用：当前 Session 会等整个 Script 完成，然后收到一个最终 `tool_result`。返回值中的 `async_launched` 是课程约定的生命周期字段，不表示它被交给 BackgroundManager，也不会创建自动 turn、注入额外 `<task_notification>` 或占用另一个 Session。

Workflow 内部的多个 agent 请求可以并发执行，中间值只存在于 Script 变量、Task progress 和 journal 中，不会逐步进入主会话 `messages[]`，因此不会因为每个内部步骤单独触发主会话上下文压缩。最终 Workflow JSON 作为一个工具结果进入主会话，仍会计入后续上下文预算。

#### Artifact 与恢复

每个 run 在 `.workflows/` 中保存：

| 文件 | 内容 |
|---|---|
| `<runId>.json` | Workflow 名称、原始 args 和最终 Task 状态 |
| `<runId>.journal.jsonl` | 每次成功 agent 调用的 append-only checkpoint |
| `<runId>.output.json` | 完整结果或失败信息 |
| `<runId>.lock` | 同一 run 的跨进程文件锁 |

snapshot 和 output 使用临时文件原子替换；journal 每条记录写入后执行 `Sync`。恢复时 Script 会重新运行，但每个 `Agent` 根据 kind、label、prompt 和 Schema 计算与并发完成顺序无关的 semantic key。未变化的调用直接复用 journal，只有变化的调用及其下游步骤重新执行。缓存值也必须重新通过 Schema 校验，损坏记录不会被静默接受。

可以使用下面的请求验证真实调用链：

```text
读取当前 Git diff，把完整 diff 放入 args.changes，运行 review-changes workflow，并汇总确认的问题。
```

### Goal Loop

s17 把 `/goal` 实现为当前 Session 上的 Stop Hook。主模型停止调用工具只表示本轮想结束；存在活动 Goal 时，独立 evaluator 会根据对话中的实际证据判断整个完成条件是否已经满足。

```text
main Agent 无 tool_use
        -> Goal Stop Hook
        -> 是否有仍运行的 Background / Team 工作？
             yes -> defer，保留 Goal 并归还用户控制
             no  -> 独立 evaluator（无工具、独立模型调用）
                       |
             +---------+----------+
             |                    |
           block               terminal
             |          achieved / failed /
             |          error / limit / defer
             v                    |
追加 [Goal still active]           v
到同一个 messages[]          返回当前 Session 调用方
             |
        同一个 Runner 继续
```

Goal 不会创建第二个 Session，也没有隐藏的 CommandQueue。`block` 反馈直接进入当前 `messages[]`；evaluator 响应本身不会进入主历史，其 token 也不计入主 Agent usage。evaluator 默认读取最近 24,000 个字符的完整消息；只有最新单条消息本身超限时才保留头尾并裁掉中间。

#### CLI 使用

设置完成条件并立即开始工作：

```text
/goal go test ./... exits with code 0 and lint reports no errors
```

查看活动条件、耗时、评估次数、主 Agent token 消耗和最近判断：

```text
/goal
```

清理 Goal：

```text
/goal clear
```

`stop`、`off`、`reset`、`none` 和 `cancel` 也是清理别名。设置新条件会替换旧 Goal。`goal.Restore` 可以从 Host 提供的 `goal_status` 事件恢复最后一个活动 Goal，但当前 CLI 不持久化完整 Session，因此进程重启后不会自动恢复。

#### Stop 决策与退出边界

| action | 行为 |
|---|---|
| `allow` | 没有活动 Goal，按普通 Agent Loop 返回 |
| `block` | evaluator 认为证据不足，追加反馈并在同一个 Runner 内继续 |
| `defer` | Background、Team 或人工审批尚未完成，保留 Goal 并归还控制权 |
| `achieved` | 条件已由对话证据证明，记录成功并清理活动 Goal |
| `failed` | evaluator 判断条件无法完成，记录失败并清理活动 Goal |
| `limit` | 达到 turn 或连续 block 上限，保留活动 Goal 并归还控制权 |
| `error` | evaluator 调用或响应校验失败，保留活动 Goal 并归还控制权 |

后台命令只有处于 `running` 才触发 defer；已完成结果会先作为 `<task_notification>` 注入会话。Teammate 的 `working`、`stopping` 和 `waiting_approval` 会跳过 evaluator，其中等待审批会立即把控制权交给用户；`idle` 不构成 pending。Workflow 对主 Agent 仍是同步工具调用，不伪装成 pending work。

evaluator 只接受严格 JSON：

```json
{"ok": false, "reason": "missing test output", "impossible": false}
```

缺失 `ok` 或 `reason`、未知字段、尾随内容、空 reason，以及同时返回 `ok=true` 和 `impossible=true` 都会成为 `error`，不会被误判为完成；省略 `impossible` 等价于 `false`。

#### Goal 配置

| 环境变量 | 默认值 | 含义 |
|---|---|---|
| `GOAL_EVALUATOR_MODEL_ID` | `MODEL_ID` | 独立 evaluator 使用的模型 |
| `MAX_TURNS` | `0` | 单次 Runner 的主模型调用上限；`0` 表示不限制 |
| `CLAUDE_CODE_STOP_HOOK_BLOCK_CAP` | `8` | 一次用户请求中允许连续 block 的次数 |

达到任一上限都只停止自动继续，不会将 Goal 标记为成功，也不会静默清理。用户发起新的普通请求时会获得新的连续 block 窗口。

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
    ├── agent/        # Session、Agent Loop、恢复与 ToolExecutor
    ├── protocol/     # Message、ContentBlock 与消息快照
    ├── llm/          # 供应商无关 Model 接口、Request / Response / Usage
    ├── model/        # llm.Model 的 Anthropic 适配
    ├── agentctx/     # Prompt、Compact 与 Memory 编排
    ├── tool/         # Spec、Handler、并发安全 Registry
    │   └── builtin/  # 绑定工作区的 Shell 与文件工具
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
    ├── workflow/     # s16 Registry、编排原语、journal、恢复与示例 Workflow
    └── goal/         # s17 Goal 状态、独立 evaluator、transcript 与 Stop gate
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
# 可选：Goal evaluator 默认复用 MODEL_ID
GOAL_EVALUATOR_MODEL_ID=
# 0 表示不限制单次 Runner 的主模型调用次数
MAX_TURNS=0
CLAUDE_CODE_STOP_HOOK_BLOCK_CAP=8
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

## 课程完成状态

新版 s01–s17 主线已全部实现。后续扩展应继续复用当前 Session、Runner、Hook 和协议边界，避免为新能力复制第二套 Agent Loop。
