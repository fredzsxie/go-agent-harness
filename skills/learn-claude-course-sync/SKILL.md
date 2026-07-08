---
name: learn-claude-course-sync
description: |
  对照本地 learn-claude-code 课程仓库，为当前 Go agent harness 项目补齐或对齐对应课程能力。适用于用户提到
  `~/Documents/Code/learn-claude-code`、`s01` 到 `s20`、`README.md`、`code.py`、`参考版`、`添加对应代码逻辑`、`在最新项目代码基础上完善` 等场景。
  Keywords: learn-claude-code, s01, s02, s03, s04, s05, s06, s07, s08, s09, s10,
  s11, s12, s13, s14, s15, s16, s17, s18, s19, s20, README.md, code.py, Claude Code 学习项目
---

# Learn Claude Course Sync

当用户要求“参考课程目录中的 `README.md` 和 `code.py`，在当前项目最新代码基础上添加或对齐代码逻辑”时，使用这个 skill。

目标不是逐行翻译 Python，而是把参考课的行为和边界，落实到当前 Go 项目的既有结构里。

## 先做路径确认

仓库路径为：`~/Documents/Code/learn-claude-code`

对每个目标课程目录，都要同时阅读：

- `<lesson>/README.md`
- `<lesson>/code.py`

规则：

- `README.md` 用来确认这节课的目标、边界和学习重点。
- `code.py` 用来确认参考实现的运行流程、状态结构和工具行为。

## 实施原则

1. 先读当前仓库，再改代码。
2. 优先复用当前 Go 项目的扩展点，不直接把参考版的大类或大文件搬进来。
3. 主循环相关逻辑优先放在 `loop/`。
4. 组合与装配优先放在 `internal/app/`。
5. 具体能力优先放在对应的 `internal/<feature>/` 包。
6. 工具注册走 `loop.Registry`，不要在 `Runner` 里塞特判。
7. 除非课程明确要求，否则不要把主 agent 的能力自动扩散到 subagent。
8. 不改无关逻辑，不顺手做大重构。

## 当前项目的落点约束

优先按下面的结构集成能力：

- `main.go`：极薄入口
- `loop/runner.go`：agent loop
- `loop/registry.go`：工具注册与分发
- `loop/tooluse.go`：tool_use 执行流程
- `internal/app/`：默认 registry、hooks、runner 装配
- `internal/prompt/`：system prompt 组装
- `internal/permission/`：权限闸门
- `internal/hooks/`：hook pipeline
- `internal/todo/`：todo_write
- `internal/subagent/`：子 agent
- `internal/skill/`：skill index / load
- 其他后续课程能力见 [references/course-map.md](references/course-map.md)

## 标准工作流

1. 明确目标课程。
2. 阅读对应课程的 `README.md` 和 `code.py`。
3. 阅读当前仓库中与该课程最相关的 Go 文件。
4. 找出“行为差异”，而不是只看“代码长得像不像”。
5. 在当前架构中补齐最小闭环：
   - 新状态
   - 新工具
   - 新 prompt 片段
   - 新 hook / permission 逻辑
   - 新后台运行机制
6. 只在必要处补测试，优先覆盖解析、状态机、调度分发这类易回归逻辑。
7. 对改动过的 Go 文件运行 `gofmt -w`。
8. 运行：

```bash
GOCACHE=/private/tmp/go-agent-harness-go-cache go test ./...
```

9. 输出结果时说明：
   - 对齐了哪些课程行为
   - 哪些地方保留了当前 Go 版结构差异
   - 是否还有未覆盖的后续扩展点

## 对齐判断规则

- 用户说“参考某课添加逻辑”，默认以该课为主，不自动连带实现后续多课内容。
- 用户说“与参考版对齐”，优先对齐行为和边界，不追求文件组织完全一致。
- 当前 Go 项目已有更稳定的扩展点时，保留 Go 版分层，只迁移必要行为。
- 参考版是单文件实现而当前项目是多包结构时，按职责拆入现有模块。
- 若参考课程依赖前置课程概念，允许补最小前置能力，但不要扩大范围。

## 常见实现判断

- 新工具：优先改 `internal/app/registry.go` 和对应 feature package。
- 新 prompt 片段：优先改 `internal/prompt/`，不要在 `runner.go` 里硬编码更多文本。
- 新运行时状态：优先建 `internal/<feature>/manager` 风格对象。
- 新 tool_use 执行编排：优先改 `loop/tooluse.go` 或 `loop/registry.go`。
- 新主循环控制：只在确实影响 loop 终止、重试、压缩、恢复时修改 `loop/runner.go`。

## 推荐检查顺序

如果用户没有明确指出要改哪些文件，先检查：

1. `README.md`
2. `main.go`
3. `internal/app/app.go`
4. `internal/app/registry.go`
5. `loop/runner.go`
6. `loop/tooluse.go`
7. 目标功能对应的 `internal/<feature>/`

## 参考映射

课程目录与本项目推荐落点，先读 [references/course-map.md](references/course-map.md)。
