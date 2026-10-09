package goal

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"go-agent-harness/internal/hooks"
	"go-agent-harness/internal/logger"
	"go-agent-harness/internal/protocol"
)

// Controller 实现 s17 的停止闸门，而不是另一套 Agent 循环或隐藏任务队列。
// evaluator 独立检查完成证据；block 让原历史继续，defer 等待运行中工作，上限与错误保留 Goal。
type Controller struct {
	mu                sync.Mutex
	evaluator         Evaluator
	blockCap          int
	now               func() time.Time
	pendingReason     func() string
	active            *State
	lastStatus        *Event
	events            []Event
	consecutiveBlocks int
	generation        uint64
}

func New(config Config) (*Controller, error) {
	if config.BlockCap == 0 {
		config.BlockCap = DefaultBlockCap
	}
	if config.BlockCap < 1 {
		return nil, fmt.Errorf("goal block cap must be at least 1")
	}
	if config.Now == nil {
		config.Now = time.Now
	}
	return &Controller{
		evaluator:     config.Evaluator,
		blockCap:      config.BlockCap,
		now:           config.Now,
		pendingReason: config.PendingReason,
		events:        make([]Event, 0, 8),
	}, nil
}

// Set 创建或替换活动 Goal；token baseline 用于后续状态展示，不作为隐藏预算。
func (c *Controller) Set(condition string, tokensAtStart int64) (State, error) {
	condition = strings.TrimSpace(condition)
	if condition == "" {
		return State{}, fmt.Errorf("goal condition cannot be empty")
	}
	if utf8.RuneCountInString(condition) > MaxConditionLen {
		return State{}, fmt.Errorf("goal condition cannot exceed %d characters", MaxConditionLen)
	}

	c.mu.Lock()
	defer c.mu.Unlock()
	if c.active != nil {
		c.recordLocked(false, false, false, "replaced by a new goal")
	}
	c.generation++
	c.active = &State{Condition: condition, SetAt: c.now(), TokensAtStart: max(0, tokensAtStart)}
	c.consecutiveBlocks = 0
	c.recordLocked(true, false, false, "goal set")
	logger.Info("[Goal] set condition=%q", condition)
	return *c.active, nil
}

// Clear 清理活动 Goal；已完成或已失败的最后状态仍可通过 Status 查看。
func (c *Controller) Clear(reason string) (State, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.active == nil {
		return State{}, false
	}
	if strings.TrimSpace(reason) == "" {
		reason = "cleared"
	}
	cleared := *c.active
	c.recordLocked(false, false, false, reason)
	c.active = nil
	c.consecutiveBlocks = 0
	c.generation++
	logger.Info("[Goal] cleared condition=%q reason=%s", cleared.Condition, reason)
	return cleared, true
}

func (c *Controller) Active() (State, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.active == nil {
		return State{}, false
	}
	return *c.active, true
}

// BeginQuery 重置“连续阻止停止”的计数；每次用户主动继续时都获得新的自动执行窗口。
func (c *Controller) BeginQuery() {
	c.mu.Lock()
	c.consecutiveBlocks = 0
	c.mu.Unlock()
}

func (c *Controller) Status(currentTokens int64) string {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.active == nil {
		if c.lastStatus != nil && c.lastStatus.Met {
			return fmt.Sprintf("Goal achieved: %s\nReason: %s", c.lastStatus.Condition, c.lastStatus.Reason)
		}
		if c.lastStatus != nil && c.lastStatus.Failed {
			return fmt.Sprintf("Goal failed: %s\nReason: %s", c.lastStatus.Condition, c.lastStatus.Reason)
		}
		return "No goal set"
	}
	elapsed := max(0, int(c.now().Sub(c.active.SetAt)/time.Second))
	spent := max(int64(0), currentTokens-c.active.TokensAtStart)
	status := fmt.Sprintf("Goal active: %s\nElapsed: %ds\nEvaluations: %d\nTokens: %d",
		c.active.Condition, elapsed, c.active.Iterations, spent)
	if c.active.LastReason != "" {
		status += "\nLast reason: " + c.active.LastReason
	}
	return status
}

func (c *Controller) Events() []Event {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]Event(nil), c.events...)
}

// EvaluateAfterTurn 在锁外调用 evaluator，避免慢模型请求阻塞 /goal 查询或清理。
// generation 防止旧评估结果覆盖在请求期间被替换的新 Goal。
func (c *Controller) EvaluateAfterTurn(ctx context.Context, messages []protocol.Message, pendingWork bool) hooks.StopDecision {
	pendingReason := ""
	if pendingWork {
		pendingReason = "background work is still running"
	}
	return c.evaluateAfterTurn(ctx, messages, pendingReason)
}

func (c *Controller) evaluateAfterTurn(ctx context.Context, messages []protocol.Message, pendingReason string) hooks.StopDecision {
	c.mu.Lock()
	if c.active == nil {
		c.mu.Unlock()
		return hooks.StopDecision{Action: hooks.StopAllow}
	}
	if pendingReason != "" {
		c.mu.Unlock()
		logger.Info("[Goal] evaluation deferred: %s", pendingReason)
		return hooks.StopDecision{Action: hooks.StopDefer, Reason: pendingReason}
	}
	condition := c.active.Condition
	generation := c.generation
	evaluator := c.evaluator
	c.mu.Unlock()

	if evaluator == nil {
		return c.finishEvaluation(generation, Evaluation{}, fmt.Errorf("goal evaluator is not configured"))
	}
	logger.Info("[Goal] evaluating condition=%q", condition)
	evaluation, err := evaluator.Evaluate(ctx, condition, protocol.CloneMessages(messages))
	return c.finishEvaluation(generation, evaluation, err)
}

// Stop 将 Goal 评估接到通用 Stop Hook，并为 block 决策生成下一轮可直接使用的反馈。
func (c *Controller) Stop(ctx context.Context, input hooks.StopContext) (hooks.StopDecision, error) {
	pendingReason := ""
	if c.pendingReason != nil {
		pendingReason = strings.TrimSpace(c.pendingReason())
	}
	decision := c.evaluateAfterTurn(ctx, input.Messages, pendingReason)
	if decision.Action != hooks.StopBlock {
		return decision, nil
	}
	state, _ := c.Active()
	decision.Reason = fmt.Sprintf(
		"[Goal still active]\nCondition: %s\nEvaluator: %s\nContinue working and surface the missing evidence.",
		state.Condition, decision.Reason,
	)
	return decision, nil
}

func (c *Controller) finishEvaluation(generation uint64, evaluation Evaluation, evaluationErr error) hooks.StopDecision {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.generation != generation {
		if c.active == nil {
			return hooks.StopDecision{Action: hooks.StopAllow}
		}
		return hooks.StopDecision{Action: hooks.StopBlock, Reason: "goal changed while evaluation was running"}
	}
	if c.active == nil {
		return hooks.StopDecision{Action: hooks.StopAllow}
	}

	if evaluationErr != nil {
		reason := fmt.Sprintf("%T: %v", evaluationErr, evaluationErr)
		c.active.LastReason = reason
		c.recordLocked(true, false, false, reason)
		logger.Error("[Goal] evaluator failed: %v", evaluationErr)
		return hooks.StopDecision{Action: hooks.StopError, Reason: reason}
	}
	evaluation.Reason = strings.TrimSpace(evaluation.Reason)
	if evaluation.Reason == "" {
		evaluation.Reason = "evaluator returned no reason"
	}
	c.active.Iterations++
	c.active.LastReason = evaluation.Reason

	if evaluation.OK {
		c.recordLocked(false, true, false, evaluation.Reason)
		c.active = nil
		c.consecutiveBlocks = 0
		c.generation++
		logger.Info("[Goal] achieved: %s", evaluation.Reason)
		return hooks.StopDecision{Action: hooks.StopAchieved, Reason: evaluation.Reason}
	}
	if evaluation.Impossible {
		c.recordLocked(false, false, true, evaluation.Reason)
		c.active = nil
		c.consecutiveBlocks = 0
		c.generation++
		logger.Warn("[Goal] failed: %s", evaluation.Reason)
		return hooks.StopDecision{Action: hooks.StopFailed, Reason: evaluation.Reason}
	}

	c.consecutiveBlocks++
	c.recordLocked(true, false, false, evaluation.Reason)
	if c.consecutiveBlocks > c.blockCap {
		reason := fmt.Sprintf("goal remains active, but the Stop hook blocked %d consecutive turns", c.blockCap)
		logger.Warn("[Goal] continuation limit reached: %s", reason)
		return hooks.StopDecision{Action: hooks.StopLimit, Reason: reason}
	}
	logger.Info("[Goal] stop blocked attempt=%d/%d: %s", c.consecutiveBlocks, c.blockCap, evaluation.Reason)
	return hooks.StopDecision{Action: hooks.StopBlock, Reason: evaluation.Reason}
}

func (c *Controller) recordLocked(active, met, failed bool, reason string) {
	state := c.active
	event := Event{Type: "goal_status", Active: active, Met: met, Failed: failed, Reason: reason}
	if state != nil {
		event.Condition = state.Condition
		event.Iterations = state.Iterations
		event.DurationSeconds = max(int64(0), int64(c.now().Sub(state.SetAt)/time.Second))
	}
	c.events = append(c.events, event)
	c.lastStatus = &c.events[len(c.events)-1]
}

// Restore 只恢复最后一个仍活动的 Goal；计数、耗时和 token baseline 从当前 Session 重新开始。
func Restore(config Config, events []Event, tokensAtStart int64) (*Controller, error) {
	controller, err := New(config)
	if err != nil {
		return nil, err
	}
	controller.events = append(controller.events, events...)
	for index := len(events) - 1; index >= 0; index-- {
		event := events[index]
		if event.Type != "goal_status" {
			continue
		}
		copyEvent := event
		controller.lastStatus = &copyEvent
		if event.Active {
			condition := strings.TrimSpace(event.Condition)
			if condition == "" || utf8.RuneCountInString(condition) > MaxConditionLen {
				return nil, fmt.Errorf("invalid active goal event")
			}
			controller.active = &State{
				Condition: condition, SetAt: controller.now(), TokensAtStart: max(int64(0), tokensAtStart),
			}
			controller.generation++
			logger.Info("[Goal] restored condition=%q", condition)
		}
		break
	}
	return controller, nil
}
