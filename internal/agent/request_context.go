package agent

import (
	"context"
	"strings"
)

type activeRequestKey struct{}
type tokenBaselineKey struct{}

// WithActiveRequest 将当前 turn 的权威请求与会话历史分离传递。
func WithActiveRequest(ctx context.Context, request string) context.Context {
	return context.WithValue(ctx, activeRequestKey{}, strings.TrimSpace(request))
}

func activeRequestFromContext(ctx context.Context) (string, bool) {
	request, ok := ctx.Value(activeRequestKey{}).(string)
	return strings.TrimSpace(request), ok
}

// withTokenBaseline 让 Stop Hook 在本次 Run 尚未提交前也能看到 Session 累计用量。
func withTokenBaseline(ctx context.Context, tokens int64) context.Context {
	return context.WithValue(ctx, tokenBaselineKey{}, tokens)
}

func tokenBaselineFromContext(ctx context.Context) int64 {
	tokens, _ := ctx.Value(tokenBaselineKey{}).(int64)
	return tokens
}
