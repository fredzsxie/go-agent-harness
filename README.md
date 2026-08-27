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

当前学习进度到 **s10 Task System**。s01–s10 的主体能力已经接入 Go 版主循环，并对齐了新版课程中影响正确性的主要边界；s11–s17 暂不实现。

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
| s11 | Background Tasks | 待学习 | `internal/scheduler/` 目前为空包 |
| s12 | Cron Scheduler | 待学习 | `internal/scheduler/` 目前为空包 |
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
    ├── scheduler/    # s11/s12 待学习
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

下一课从新版 **s11 Background Tasks** 开始。在开始 s11 前，不提前实现 Background、Cron、Agent Teams、MCP、Workflow 或 Goal Loop。
