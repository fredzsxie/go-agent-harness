# go-agent-harness

Go 版 Claude Code agent harness 学习项目。

本项目参考 [shareAI-lab/learn-claude-code](https://github.com/shareAI-lab/learn-claude-code) 的当前 `s01` 到 `s20` 主线课程，用 Go 重新组织一个可渐进扩展的初始框架。目标不是复刻某个具体产品，而是理解 Claude Code 这类 coding agent 的 harness 结构：

```text
Agent = Model + Harness

Harness = agent loop
        + tools
        + permission gates
        + hooks
        + todo/task state
        + subagents
        + skills
        + compact/memory/prompt/retry
        + background/cron runtime
        + team coordination
        + worktree isolation
        + MCP/external capability routing
```

核心原则：模型负责判断，harness 负责提供上下文、工具、边界和执行环境。

## 当前初始框架

```text
go-agent-harness/
├── main.go                    # 极薄入口：加载配置，启动 app
├── config/
│   └── config.go              # .env 加载与 LLM 配置
├── loop/
│   ├── types.go               # Message / Role / ToolSpec
│   ├── registry.go            # 工具注册、工具调度、权限 authorizer 挂载点
│   └── runner.go              # Anthropic Messages API agent loop
├── tools/
│   ├── bash.go                # bash 工具
│   ├── read.go                # read_file 工具
│   ├── write.go               # write_file 工具
│   ├── edit.go                # edit_file 工具
│   ├── glob.go                # glob 工具
│   └── path.go                # workspace safe path
└── internal/
    ├── app/                   # CLI 组合层：registry + permission + runner + REPL
    ├── permission/            # s03：deny list / rules / user approval
    ├── hooks/                 # s04 预留
    ├── todo/                  # s05 预留
    ├── subagent/              # s06 预留
    ├── skill/                 # s07 预留
    ├── compact/               # s08 预留
    ├── memory/                # s09 预留
    ├── prompt/                # s10 预留
    ├── retry/                 # s11 预留
    ├── task/                  # s12 预留
    ├── scheduler/             # s13/s14 预留
    ├── team/                  # s15-s17 预留
    └── worktree/              # s18 预留
```

`main.go` 只做启动，不再直接关心工具、权限和消息循环细节。真正的 harness 组合在 `internal/app`，核心 loop 在 `loop.Runner`，工具 schema 与 handler 统一从 `loop.Registry` 注册。

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
```

## 一条不变的 Agent Loop

所有课程都围绕这一条循环叠加能力：

```text
messages[] -> LLM -> response
                    |
                    v
              stop_reason == tool_use ?
                    |
       yes ---------+--------- no
       |                      |
       v                      v
execute tool              return text
append tool_result
loop back to messages[]
```

学习重点不是把 loop 写复杂，而是把机制挂到 loop 周围：

```text
tool registry
  -> permission gate
  -> hook pipeline
  -> handler execution
  -> tool_result
```

## s01-s20 学习计划

建议按远端仓库的当前主线 `s01_agent_loop/` 到 `s20_comprehensive/` 顺序学习。每一课都对应本项目一个明确落点。

| 课 | 主题 | 核心问题 | Go 项目落点 | 产出 |
|---|---|---|---|---|
| s01 | Agent Loop | messages 如何在 user、assistant、tool_result 之间流动 | `loop/types.go`, `loop/runner.go` | 能解释一次完整 tool_use 回合 |
| s02 | Tool Use | 新增工具时为什么不应改主循环 | `loop/registry.go`, `tools/` | 新增一个工具只需要注册 spec + handler |
| s03 | Permission System | 哪些操作必须禁止、哪些需要用户确认 | `internal/permission/`, `loop.Registry.UseAuthorizer` | bash/write/edit 执行前经过权限闸门 |
| s04 | Hook System | 如何在 loop 周围扩展审计、日志、埋点 | `internal/hooks/` | 实现 PreToolUse / PostToolUse hook 链 |
| s05 | TodoWrite | agent 为什么需要显式计划 | `internal/todo/` | TodoItem 状态机：pending / in_progress / done |
| s06 | Subagent | 子任务为什么要隔离上下文 | `internal/subagent/` | 子 agent 使用独立 messages，只返回结果摘要 |
| s07 | Skill Loading | 知识为什么要按需加载 | `internal/skill/` | skill index 常驻，全文按需注入 |
| s08 | Context Compact | 长会话如何腾出上下文空间 | `internal/compact/` | token 估算、摘要、裁剪策略 |
| s09 | Memory System | 什么应该跨会话保留 | `internal/memory/` | selection / extraction / consolidation 三段式记忆 |
| s10 | System Prompt | prompt 为什么要运行时组装 | `internal/prompt/` | section-based prompt builder |
| s11 | Error Recovery | 工具失败、上下文不足、模型失败时怎么恢复 | `internal/retry/` | retry policy、fallback model、compact retry |
| s12 | Task System | 大目标如何拆成可恢复的任务图 | `internal/task/` | TaskRecord、blockedBy、磁盘持久化 |
| s13 | Background Tasks | 慢操作如何不阻塞 agent 思考 | `internal/scheduler/` | goroutine 执行，完成后注入通知消息 |
| s14 | Cron Scheduler | 无人触发时如何定时唤起任务 | `internal/scheduler/` | durable schedule + ticker/cron trigger |
| s15 | Agent Teams | 多 agent 如何异步协作 | `internal/team/` | mailbox、inbox、outbox、permission bubbling |
| s16 | Team Protocols | 团队协作为什么需要固定协议 | `internal/team/` | request/reply、shutdown、approval 协议 |
| s17 | Autonomous Agents | agent 如何自己认领工作 | `internal/team/` | idle poll、claim、self-organization |
| s18 | Worktree Isolation | 并行任务如何避免互相污染 | `internal/worktree/` | task id 绑定独立目录或 git worktree |
| s19 | MCP Plugin | 外部能力如何进入同一个工具池 | `tools/`, 后续 `internal/mcp/` | MCP tool schema 转成本地 ToolSpec |
| s20 | Comprehensive Agent | 多机制如何回到同一条 loop | 全部模块 | many mechanisms, one loop |

## 阶段节奏

### 阶段一：单 Agent 核心

覆盖 s01-s06。先把最小闭环跑稳：消息、模型调用、工具调用、权限、hook、todo、subagent。

验收标准：

- `loop.Runner` 不因新增工具而改变主体结构。
- `loop.Registry` 是唯一工具入口。
- 权限检查发生在 handler 前。
- 子任务可以用独立上下文执行。

### 阶段二：上下文与恢复

覆盖 s07-s11。重点从“能跑”转向“长时间可用”：skill、compact、memory、prompt builder、retry。

验收标准：

- prompt 不再硬编码成一个大字符串。
- skill 不是启动时全量塞入上下文。
- 上下文接近预算时可以 compact。
- 常见失败能通过 retry/fallback/compact 自动恢复。

### 阶段三：任务运行时

覆盖 s12-s14。把一次性对话升级成可恢复、可异步、可定时的 runtime。

验收标准：

- task graph 可以落盘恢复。
- background task 完成后能通知主 loop。
- cron/scheduler 能自动创建或唤醒任务。

### 阶段四：多 Agent 与隔离

覆盖 s15-s18。重点是异步协作、协议约束、自主认领和目录隔离。

验收标准：

- team mailbox 使用 append-only 或等价持久化结构。
- 协议状态机可测试。
- agent 能从任务板自主 claim 工作。
- 每个任务有独立 workspace/worktree 边界。

### 阶段五：外部能力与总装

覆盖 s19-s20。把 MCP 或其他外部能力收敛到统一工具池，最后回到同一条 agent loop。

验收标准：

- 本地工具和 MCP 工具都表现为 `ToolSpec + Handler`。
- runner 不关心工具来源。
- 综合版本仍能用同一条 loop 解释。

## 近期 TODO

- 为 `loop.Registry` 增加单元测试：注册顺序、未知工具、权限拒绝。
- 为 `tools.SafePath` 增加路径逃逸测试。
- 把 `internal/hooks` 接入 `Registry.Dispatch`。
- 把 `systemPrompt` 从 `loop/runner.go` 移到 `internal/prompt`。
- 增加一个 mock LLM runner，降低无 API Key 时的学习门槛。

## 参考

- learn-claude-code: https://github.com/shareAI-lab/learn-claude-code
- 当前主线课程：`s01_agent_loop/` 到 `s20_comprehensive/`
