# go-agent-harness

> 用 Go 语言从零复刻 [learn-claude-code](https://github.com/shareAI-lab/learn-claude-code) 的 **Agent Harness 工程骨架**。
> 目标不是跑模型的 Python 代码，而是把它的核心设计——**一个 while 循环 + tool dispatch + 若干可插拔机制**——翻译成 Go 后端的语言。

![Go Version](https://img.shields.io/badge/Go-1.22+-00ADD8?style=flat-square&logo=go)
![Status](https://img.shields.io/badge/status-learning-yellow?style=flat-square)
![License](https://img.shields.io/badge/license-MIT-blue?style=flat-square)

---

## 为什么存在这个项目

learn-claude-code 教会你一件事：

> **Agent = Model（你改不了权重）+ Harness（你写的运行基础设施）**
>
> 你不是在写智能，你是在构建智能栖居的世界。

这个项目就是把这个「Harness」拆成 Go 的 package / struct / interface / channel，
让你从后端工程师的视角（路由表、中间件、上下文预算、落盘、异步通知）重新理解 AI Agent 是怎么跑起来的。

---

## 核心理念：One Loop & Plug-in Everything

整个体系只有一个不变的 `while`（在 Go 里是 `for` + `select`）：

```text
User Msg
  → append messages
  → LLM 返回 (tool_use / end_turn)
  → tool_use? → dispatch(name) → handler(input) → tool_result append → 回到顶部
  → end_turn → 输出给用户
```

**每学一章只做两件事：**

1. 新增一个机制（tool / hook / manager / policy）
2. 把它挂到 loop 外面——**绝不改写循环体的形状**

---

## 项目结构

```text
go-agent-harness/
├── go.mod
├── main.go                          # 入口：跑 mock loop / 未来挂真 LLM client
├── config/
│   └── config.go                    # LLM 配置入口：base_url / api_key
├── loop/                            # ★ 最核心：Agent 循环 + 消息类型
│   ├── types.go                     # Message / ContentBlock / Role
│   ├── agent_loop_mock.go           # mock 版循环（不用 API Key 就能学）
│   └── runner.go                    # 未来：真 LLM client 接入点（Anthropic / OpenAI compat)
├── tools/                           # Tool Dispatch（= Python 版的 TOOL_HANDLERS）
│   ├── registry.go                  # Register / Dispatch / Middleware chain
│   ├── bash.go                      # bash handler（exec.CommandContext）
│   ├── readfile.go                  # read_file handler（带 safe_path 沙箱）
│   └── ...
├── internal/                        # 按 learn-claude-code 章节组织的机制
│   ├── permission/                  # s03 Permission Gate（deny / ask / allow）
│   ├── hooks/                       # s04 PreToolUse / PostToolUse 中间件链
│   ├── todo/                        # s05 TodoManager（plan-then-execute 结构化计划）
│   ├── subagent/                    # s06 SubagentSession（独立 messages[] 隔离上下文）
│   ├── skill/                       # s07 SkillLoader（两层注入：索引→全文）
│   ├── compact/                     # s08 TokenBudget + CompactHistory
│   ├── memory/                      # s09 Memory selection / extraction / consolidation
│   ├── prompt/                      # s10 System Prompt 组装流水线（section 拼接）
│   ├── retry/                       # s11 RetryPolicy + fallback model
│   ├── task/                        # s12 TaskRecord + 依赖图（.tasks/*.json）
│   ├── scheduler/                   # s13+s14 background notifier + cron ticker
│   ├── team/                        # s15-s17 Mailbox / Protocol FSM / 自治认领
│   └── worktree/                    # s18 Worktree 目录绑定隔离
└── _testdata/                       # 测试用临时目录 / fixture
```

---

## 学习路线 ↔ 代码映射（learn-claude-code s01–s20）

> 每一行告诉你：**学哪一课 → 哪个包动刀 → 预期产物是什么**

| 阶段 | 课 | learn-claude-code 主题 | 你在 `go-agent-harness` 动哪里 | 验收标志 |
|------|----|------------------------|-------------------------------|----------|
| **一：单 Agent 核心** | s01 | Agent Loop：`while + messages[]` | `loop/types.go` + `loop/agent_loop_mock.go` | 不翻代码能画消息流转图 |
| | s02 | Tool Use：TOOL_HANDLERS dispatch map | `tools/registry.go` + `bash.go` / `readfile.go` | 加 `list_dir` 只需 Register 一行 |
| | s03 | Permission：deny / ask / allow 闸门 | `internal/permission/` | dispatch 前拦危险命令 & 敏感路径写操作 |
| | s04 | Hook System：PreToolUse / PostToolUse | `internal/hooks/` | 工具执行前后自动打 JSON 审计日志 |
| | s05 | TodoWrite：结构化计划（plan-then-execute） | `internal/todo/` | Agent 状态机从 "飘" 变 "有 checklist" |
| | s06 | Subagent：独立 `messages[]` 隔离上下文 | `internal/subagent/` | 父子 messages 生命周期画得出来 |
| | s07 | Skills：两层注入（索引在 prompt / 全文在 tool_result） | `internal/skill/` | SkillLoader 扫描本地 md + LoadFullContent |
| | s08 | Context Compact：token budget + 截断/摘要 | `internal/compact/` | Estimate → ShouldCompact → CompactHistory 可单测 |
| **二：安全 & 系统工程化** | s09 | Memory：跨会话沉淀（select→extract→consolidate） | `internal/memory/` | 落盘 `.agent/memory.md` 启动时 rehydrate |
| | s10 | System Prompt：运行时 section 拼装流水线 | `internal/prompt/` | 用 `text/template` 或 ordered sections 组装 |
| | s11 | Error Recovery：retry / make room / fallback model | `internal/retry/` | mock loop 里模拟两次 fail → compact → 第三次 success |
| **三：会话 → 运行时** | s12 | Task System：`.tasks/*.json` + blockedBy DAG | `internal/task/` | Create / MarkDone / ListRunnable 可落盘恢复 |
| | s13 | Background Tasks：goroutine + channel 注入主循环 | `internal/scheduler/` | notifier 把完成事件变一条 Message 塞回 loop |
| | s14 | Cron Scheduler：定时触发 | `internal/scheduler/` | time.Ticker / robfig/cron 产事件 → inject messages |
| **四：多 Agent & 外部** | s15 | Agent Teams：JSONL mailbox（append-only 信箱） | `internal/team/mailbox.go` | Send / Poll / Ack 文件级实现 |
| | s16 | Team Protocols：关机 / 审批 FSM | `internal/team/protocol.go` | `switch state` trace 不碰网络也能跑 |
| | s17 | Autonomous Agents：idle poll + 认领（去中心化） | `internal/team/claim.go` | 讨论 race 条件 & 用 file lock / atomic rename 加固 |
| | s18 | Worktree Isolation：各任务绑独立目录 | `internal/worktree/` | 每个 task 的 handler scope 锁在自己的 dir |
| | s19 | MCP Plugin：外部工具 multi-transport 路由 | `tools/` 扩展 `ToolSchema.Source = "mcp"` 接口 | handler 变成 stub RPC，transport 可换 |
| | s20 | Comprehensive：**many mechanisms, one loop** | 全部 internal/ 合流 | 指 s20 Python 版 loop：同构 s01，只是挂件更厚 |
