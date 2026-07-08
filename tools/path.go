// Package tools 中的路径包装层将文件工具统一接到 workspace 解析器上，
// 避免各工具重复实现工作区边界判断。
package tools

import "go-agent-harness/internal/workspace"

func SafePath(path string) (string, error) {
	return workspace.Resolve(path)
}
