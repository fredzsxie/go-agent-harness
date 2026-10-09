// Package memory 实现 s09 的长期知识生命周期，而不是另一份会话记录。
// store / recall / extract / consolidate 分别负责存储、召回、准入和合并；模型通过函数参数注入。
// 当前由单个 Session 串行使用，同一工作区的跨进程并发写入不在本包保证范围内。
package memory

import (
	"context"
)

const (
	DefaultMaxRelevant          = 5  // 最多注入相关记忆数
	DefaultConsolidateThreshold = 10 // 触发合并的文件数阈值
	DefaultRecallCharLimit      = 20000
	MaxConsolidatedRecords      = 30
)

type Type string

const (
	TypeUser      Type = "user"
	TypeFeedback  Type = "feedback"
	TypeProject   Type = "project"
	TypeReference Type = "reference"
)

type Scope string

const (
	ScopePersistent  Scope = "persistent"
	ScopeCurrentTask Scope = "current_task"
)

type Record struct {
	Filename    string `json:"filename,omitempty"`
	Name        string `json:"name"`
	Type        Type   `json:"type"`
	Scope       Scope  `json:"scope,omitempty"`
	Description string `json:"description"`
	Body        string `json:"body"`
}

type CatalogItem struct {
	Index       int
	Filename    string
	Name        string
	Description string
	Type        Type
}

type Selector func(ctx context.Context, recent string, catalog []CatalogItem, maxItems int) ([]int, error)

type Extractor func(ctx context.Context, dialogue string, existing []CatalogItem) ([]Record, error)

type Consolidator func(ctx context.Context, records []Record) ([]Record, error)

type Config struct {
	WorkDir              string
	MemoryDir            string
	MaxRelevant          int
	ConsolidateThreshold int
	RecallCharLimit      int
}

type Manager struct {
	cfg Config
}
