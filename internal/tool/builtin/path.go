// Package builtin 中的路径包装层将文件工具统一接到 Workspace Resolver，
// 避免各工具重复实现工作区边界判断。
package builtin

import "go-agent-harness/internal/workspace"

func SafePath(path string) (string, error) {
	return workspace.Resolve(path)
}
