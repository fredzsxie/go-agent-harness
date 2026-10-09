package agent

import (
	"context"
	"errors"
	"fmt"
	"math/rand"
	"strings"
	"time"

	"go-agent-harness/internal/llm"
	"go-agent-harness/internal/logger"
	"go-agent-harness/internal/protocol"
)

const (
	escalatedMaxTokens int64 = 16000
	maxRecoveryRetries       = 2
	continuationPrompt       = "Continue from the previous response. Do not repeat completed work."
)

type recoveryState struct {
	currentModel   string
	fallbackModel  string
	consecutive529 int
}

type recoveryPolicy struct {
	maxAttempts int
	baseDelay   time.Duration
	sleep       func(context.Context, time.Duration) error
	jitter      func(time.Duration) time.Duration // 人为引入的随机延迟
}

func defaultRecoveryPolicy() recoveryPolicy {
	return recoveryPolicy{
		maxAttempts: 3,
		baseDelay:   500 * time.Millisecond,
		sleep: func(ctx context.Context, delay time.Duration) error {
			timer := time.NewTimer(delay)
			defer timer.Stop()
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-timer.C:
				return nil
			}
		},
		jitter: func(base time.Duration) time.Duration {
			return time.Duration(rand.Int63n(int64(base/4) + 1))
		},
	}
}

func (r *Runner) runTurnWithRetry(ctx context.Context, state *recoveryState, system string, messages []protocol.Message, maxTokens int64, intercept ToolInterceptor) (WorkerTurn, error) {
	var lastErr error
	for attempt := 0; attempt < r.recovery.maxAttempts; attempt++ {
		turn, err := r.worker.RunTurnWithOptions(ctx, system, messages, intercept, TurnOptions{
			Model: state.currentModel, MaxTokens: maxTokens,
		})
		if err == nil {
			state.consecutive529 = 0
			return turn, nil
		}
		// assistant 已生成时，错误来自工具执行，不得重放模型调用。
		if turn.Assistant.Role != "" || !isTransientModelError(err) || ctx.Err() != nil {
			return turn, err
		}

		lastErr = err
		status := modelHTTPStatus(err)
		if status == 529 {
			state.consecutive529++
			if state.consecutive529 >= 2 && state.fallbackModel != "" && state.currentModel != state.fallbackModel {
				state.currentModel = state.fallbackModel
				state.consecutive529 = 0
				logger.Warn("[LLM] 529 overloaded, switching to fallback model %s", state.fallbackModel)
			}
		} else {
			state.consecutive529 = 0
		}
		if attempt+1 >= r.recovery.maxAttempts {
			break
		}

		delay := retryDelay(r.recovery, attempt)
		logger.Warn("[LLM] transient status=%d, retry %d/%d after %s", status, attempt+1, r.recovery.maxAttempts, delay)
		if err := r.recovery.sleep(ctx, delay); err != nil {
			return WorkerTurn{}, err
		}
	}
	logger.Error("[LLM] transient retries exhausted after %d attempts: %v", r.recovery.maxAttempts, lastErr)
	return WorkerTurn{}, fmt.Errorf("model retries exhausted: %w", lastErr)
}

func retryDelay(policy recoveryPolicy, attempt int) time.Duration {
	delay := policy.baseDelay * time.Duration(1<<attempt)
	if delay > 32*time.Second {
		delay = 32 * time.Second
	}
	return delay + policy.jitter(delay)
}

func isTransientModelError(err error) bool {
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return false
	}
	status := modelHTTPStatus(err)
	return status == 429 || status == 529
}

func modelHTTPStatus(err error) int {
	var modelErr *llm.Error
	if errors.As(err, &modelErr) {
		return modelErr.HTTPStatus
	}
	// 兼容未包装 HTTP 状态的 OpenAI-compatible provider。
	message := strings.ToLower(err.Error())
	if strings.Contains(message, "429") || strings.Contains(message, "rate limit") || strings.Contains(message, "ratelimit") {
		return 429
	}
	if strings.Contains(message, "529") || strings.Contains(message, "overloaded") {
		return 529
	}
	return 0
}
