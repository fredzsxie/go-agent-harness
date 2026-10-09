// Package builtin 中的路径包装层将文件工具统一接到 Workspace Resolver，
// 避免各工具重复实现工作区边界判断。
package builtin

import (
	"errors"

	"go-agent-harness/internal/workspace"
)

type ResolverProvider func() (*workspace.Resolver, error)

// Tools 将同一组 Builtin Tool 绑定到静态或动态 Workspace。
type Tools struct {
	resolver ResolverProvider
}

func New(resolver *workspace.Resolver) *Tools {
	return NewDynamic(func() (*workspace.Resolver, error) { return resolver, nil })
}

func NewDynamic(provider ResolverProvider) *Tools {
	return &Tools{resolver: provider}
}

func (t *Tools) currentResolver() (*workspace.Resolver, error) {
	if t == nil || t.resolver == nil {
		return nil, errors.New("workspace resolver is not configured")
	}
	resolver, err := t.resolver()
	if err != nil {
		return nil, err
	}
	if resolver == nil {
		return nil, errors.New("workspace resolver is not available")
	}
	return resolver, nil
}

func (t *Tools) SafePath(path string) (string, error) {
	resolver, err := t.currentResolver()
	if err != nil {
		return "", err
	}
	return resolver.Resolve(path)
}
