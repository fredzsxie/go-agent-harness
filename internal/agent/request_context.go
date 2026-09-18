package agent

import (
	"context"
	"strings"
)

type activeRequestKey struct{}

// WithActiveRequest 将当前 turn 的权威请求与会话历史分离传递。
func WithActiveRequest(ctx context.Context, request string) context.Context {
	return context.WithValue(ctx, activeRequestKey{}, strings.TrimSpace(request))
}

func activeRequestFromContext(ctx context.Context) (string, bool) {
	request, ok := ctx.Value(activeRequestKey{}).(string)
	return strings.TrimSpace(request), ok
}
