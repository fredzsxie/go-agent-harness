// Package permission 实现 s03 的执行前权限判断，通过 s04 PreToolUse 接入。
// 本包不读取终端；审批由宿主注入，自动回合不能竞争前台输入。
package permission

import (
	"context"
	"fmt"
	"strings"
)

type interactionKey struct{}

// WithInteractive 让 s12/s15 自动回合显式禁用人工审批。
func WithInteractive(ctx context.Context, interactive bool) context.Context {
	return context.WithValue(ctx, interactionKey{}, interactive)
}
func IsInteractive(ctx context.Context) bool {
	interactive, configured := ctx.Value(interactionKey{}).(bool)
	return !configured || interactive
}

// Approver 只负责交互，不改变权限规则；false 或未配置都表示拒绝。
type Approver func(toolName string, args map[string]any, reason string) bool
type PathResolver func(string) (string, error)

// Authorizer 绑定当前 Agent 的工作区和审批入口；Teammate 可注入随 assignment 变化的解析函数。
type Authorizer struct {
	resolve PathResolver
	approve Approver
}

func New(resolve PathResolver, approve Approver) *Authorizer {
	return &Authorizer{resolve: resolve, approve: approve}
}

// denyList 是教学用硬拒绝规则，不是完整 Shell 沙箱。
// s15 的宿主策略更保守：所有 Bash 命令都需要前台用户明确批准。
var denyList = []string{"rm -rf /", "sudo", "shutdown", "reboot", "mkfs", "dd if=", "> /dev/sda"}

func CheckDenyList(command string) string {
	for _, deny := range denyList {
		if strings.Contains(command, deny) {
			return fmt.Sprintf("Blocked: '%s' is on the deny list", deny)
		}
	}
	return ""
}

// Authorize 的顺序固定为参数与硬拒绝检查、工作区规则、必要时申请批准。
// 文件工具自身也校验路径；权限批准不能扩大文件工具的工作区边界。
func (a *Authorizer) Authorize(toolName string, args map[string]any, interactive bool) error {
	if toolName == "bash" {
		command, ok := args["command"].(string)
		if !ok || strings.TrimSpace(command) == "" {
			return fmt.Errorf("permission denied: shell command must be a non-empty string")
		}
		if reason := CheckDenyList(command); reason != "" {
			return fmt.Errorf("%s", reason)
		}
		if !interactive {
			return fmt.Errorf("permission denied: asynchronous turns cannot request shell approval")
		}
		return a.request(toolName, args, "Shell command requires approval")
	}
	if toolName == "write_file" || toolName == "edit_file" {
		path, _ := args["path"].(string)
		if strings.TrimSpace(path) == "" {
			return nil
		} // 参数缺失交给工具的输入校验报告。
		if a == nil || a.resolve == nil {
			return fmt.Errorf("permission denied: workspace resolver is not configured")
		}
		if _, err := a.resolve(path); err != nil {
			if !interactive {
				return fmt.Errorf("permission denied: non-interactive turns cannot request interactive approval")
			}
			return a.request(toolName, args, "Writing outside workspace")
		}
	}
	return nil
}

// AuthorizeExternal 仅用于宿主未明确放行的 s14 外部工具，Server annotations 不构成授权。
func (a *Authorizer) AuthorizeExternal(toolName string, args map[string]any, interactive bool) error {
	if !interactive {
		return fmt.Errorf("permission denied: non-interactive turns cannot request interactive approval")
	}
	return a.request(toolName, args, "External tool requires approval")
}

func (a *Authorizer) request(toolName string, args map[string]any, reason string) error {
	if a == nil || a.approve == nil || !a.approve(toolName, args, reason) {
		return fmt.Errorf("permission denied: %s", reason)
	}
	return nil
}
