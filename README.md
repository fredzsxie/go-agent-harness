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

当前学习进度到 **s11 Background Tasks**。s01–s11 的主体能力已经接入 Go 版主循环，并对齐了新版课程中影响正确性的主要边界；s12–s17 暂不实现。

| 章节 | 主题 | 状态 | Go 项目落点 |
|---|---|---|---|
| s01 | Agent Loop | 已完成 | `loop/runner.go`, `loop/anthropic.go` |
| s02 | Tool Use | 已完成 | `loop/registry.go`, `tools/` |
| s03 | Permission | 已完成 | `internal/permission/` |
| s04 | Hooks | 已完成 | `internal/hooks/`, `loop/tooluse.go` |
| s05 | TodoWrite | 已完成 | `internal/todo/` |
| s06 | Subagent | 已完成 | `internal/subagent/` |
| s07 | Skill Loading | 已完成 | `internal/skill/`, `internal/prompt/` |
| s08 | Context Compact | 已完成 | `internal/compact/`, `loop/runner.go` |
| s09 | Memory | 已完成 | `internal/memory/`, `loop/runner.go` |
| s10 | Task System | 已完成 | `internal/task/`, `.tasks/` |
| s11 | Background Tasks | 已完成 | `internal/scheduler/`, `loop/runner.go` |
| s12 | Cron Scheduler | 待学习 | `internal/scheduler/` 目前仅含 s11 逻辑 |
| s13 | Agent Teams | 待学习 | `internal/team/`, `internal/worktree/` 目前为空包 |
| s14 | MCP Plugin | 待学习 | 尚未创建 `internal/mcp/` |
| s15 | Integrated Harness | 待学习 | 等 s10–s14 完成后总装 |
| s16 | Workflow Runtime | 待学习 | 尚未实现 |
| s17 | Goal Loop | 待学习 | 尚未实现 |

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

本项目使用 [internal/conversation/types.go](./internal/conversation/types.go) 中的轻量类型保存消息历史：

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

- `Blocks` 为空时，[ToAnthropicBlocks](./loop/anthropic.go) 把非空 `Content` 转成一个 `text` block。
- `Blocks` 非空时以 `Blocks` 为准，`Content` 不会再次发送。模型响应解析时仍会把所有文本汇总到 `Content`，方便提取最终回答。
- `ParseAnthropicAssistantMessage` 当前只解析 `text` 和 `tool_use`；其他 Anthropic block 类型尚未进入本地消息历史。
- 本地字段名是统一存储模型，不是可直接发送的 Anthropic JSON。例如本地 `tool_use.ToolUseID` 必须由适配层转换为 API 的 `id`，`ToolName` 必须转换为 `name`。

上面的工具调用在本项目中可表示为：

```go
messages := []loop.Message{
    {
        Role:    loop.RoleUser,
        Content: "读取 README.md 的前 20 行",
    },
    {
        Role: loop.RoleAssistant,
        Blocks: []loop.ContentBlock{
            {Type: loop.BlockText, Text: "我先读取文件。"},
            {
                Type:      loop.BlockToolUse,
                ToolUseID: "toolu_01",
                ToolName:  "read_file",
                Input:     map[string]any{"path": "README.md", "limit": 20},
            },
        },
    },
    {
        Role: loop.RoleUser,
        Blocks: []loop.ContentBlock{
            {
                Type:      loop.BlockToolResult,
                ToolUseID: "toolu_01",
                Text:      "# go-agent-harness\n...",
                IsError:   false,
            },
        },
    },
}
```

发送请求前，[ToAnthropicMessages](./loop/anthropic.go) 会把这些内部对象转换成 Anthropic SDK 的 `MessageParam` 和 `ContentBlockParamUnion`；收到响应后，`ParseAnthropicAssistantMessage` 再将 SDK block 转回本地结构。这样主循环、compact、memory 与 subagent 可以共享同一套消息协议，而 API 字段差异集中留在适配层。

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

所有工具通过 `loop.Registry` 注册，Runner 不关心具体工具来源。

### 权限与 Hooks

工具执行顺序为：

```text
PreToolUse permission/log hooks
        -> Registry.Dispatch
        -> PostToolUse hooks
        -> tool_result
```

危险命令可以直接拒绝；可能破坏工作区的操作需要用户确认；文件工具还受到 workspace path resolver 的约束。

### Todo 与 Subagent

TodoWrite 支持数组以及部分兼容服务返回的数组字符串，强制最多 20 项、最多一个 `in_progress`，无效更新不会覆盖已有状态。连续三个工具回合未更新 Todo 时，提醒会附在第三个工具结果批次中。

Subagent 使用独立的 `messages[]` 和受限工具池，不会继续派生子 agent，只把最终文本返回给父 agent。

### Skill、Compact 与 Memory

- Skill：启动时只加载目录，正文通过 `load_skill` 按需读取。
- Compact：先处理超大工具结果，只有上下文超出预算时才依次执行 snip、micro 和摘要压缩；未被模型消费的最新工具结果批次不会被提前裁剪。
- Memory：每条记忆独立存为 Markdown；每轮先选择相关记忆，结束后提取长期信息，并在达到阈值时整理；仅持久信息可以写入，临时任务状态和重复内容会被过滤。

### Background Tasks

只有显式设置 `bash.run_in_background=true` 的命令才会异步执行。工具调用会立即返回 `bg_id`，完成结果则在后续 LLM 调用前以独立的 `<task_notification>` 注入；退出应用时会取消仍在运行的后台命令。

## 项目结构

```text
go-agent-harness/
├── main.go
├── config/
│   └── config.go
├── loop/
│   ├── anthropic.go
│   ├── message.go
│   ├── registry.go
│   ├── runner.go
│   ├── tooluse.go
│   └── types.go
├── tools/
│   ├── bash.go
│   ├── read.go
│   ├── write.go
│   ├── edit.go
│   └── glob.go
└── internal/
    ├── app/          # CLI 装配
    ├── conversation/ # 共享消息协议
    ├── hooks/        # s04
    ├── permission/   # s03
    ├── todo/         # s05
    ├── subagent/     # s06
    ├── skill/        # s07
    ├── compact/      # s08
    ├── memory/       # s09
    ├── prompt/       # 运行时 prompt 组装
    ├── retry/        # 预留，尚未实现
    ├── task/         # s10 持久化任务图
    ├── scheduler/    # s11 后台任务；s12 定时调度待学习
    ├── team/         # s13 待学习
    └── worktree/     # s13 待学习
```

## 快速开始

准备 `.env`：

```bash
ANTHROPIC_API_KEY=your_api_key
ANTHROPIC_BASE_URL=https://api.anthropic.com
MODEL_ID=claude-sonnet-4-6
```

运行：

```bash
go run .
```

验证：

```bash
GOCACHE=/private/tmp/go-agent-harness-go-cache go test ./...
GOCACHE=/private/tmp/go-agent-harness-go-cache go vet ./...
```

## 下一步

下一课从新版 **s12 Cron Scheduler** 开始。在开始 s12 前，不提前实现 Cron、Agent Teams、MCP、Workflow 或 Goal Loop。
