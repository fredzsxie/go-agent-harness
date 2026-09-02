// Package permission 实现工具调用前的三段式权限闸门，
// 将危险命令拦截、规则匹配和人工确认集中在一个入口。
package permission

import (
	"bufio"
	"fmt"
	"os"
	"strings"

	"go-agent-harness/internal/workspace"
)

/*
Three gates inserted before tool execution:

    Gate 1: Hard deny list (rm -rf /, sudo, ...)
    Gate 2: Rule matching (write outside workspace? destructive cmd?)
    Gate 3: User approval (pause and wait for confirmation)

    +-------+    +--------+    +--------+    +--------+    +------+
    | Tool  | -> | Gate 1 | -> | Gate 2 | -> | Gate 3 | -> | Exec |
    | call  |    | deny?  |    | match? |    | allow? |    |      |
    +-------+    +--------+    +--------+    +--------+    +------+
         |            |             |             |
         v            v             v             v
      (normal)     (blocked)    (ask user)   (user says no?)
*/

// Gate 1: Hard deny list — always forbidden
var DenyList = []string{
	"rm -rf /",
	"sudo",
	"shutdown",
	"reboot",
	"mkfs",
	"dd if=",
	"> /dev/sda",
}

func CheckDenyList(command string) string {
	for _, deny := range DenyList {
		if strings.Contains(command, deny) {
			return fmt.Sprintf("Blocked: '%s' is on the deny list", deny)
		}
	}
	return ""
}

// Gate 2: Rule matching — context-dependent checks
type Rule struct {
	Tools   []string
	Check   func(args map[string]any) bool
	Message string
}

var PermissionRules = []Rule{
	{
		Tools: []string{"write_file", "edit_file"},
		Check: func(args map[string]any) bool {
			path, _ := args["path"].(string)
			return pathEscapesWorkspace(path)
		},
		Message: "Writing outside workspace",
	},
	{
		Tools: []string{"bash"},
		Check: func(args map[string]any) bool {
			command, _ := args["command"].(string)
			return strings.Contains(command, "rm ") ||
				strings.Contains(command, "> /etc/") ||
				strings.Contains(command, "chmod 777")
		},
		Message: "Potentially destructive command",
	},
}

func CheckRules(toolName string, args map[string]any) string {
	for _, rule := range PermissionRules {
		if toolMatches(toolName, rule.Tools) && rule.Check(args) {
			return rule.Message
		}
	}
	return ""
}

func toolMatches(toolName string, tools []string) bool {
	for _, tool := range tools {
		if tool == toolName {
			return true
		}
	}
	return false
}

func pathEscapesWorkspace(path string) bool {
	if strings.TrimSpace(path) == "" {
		return false
	}
	_, err := workspace.Resolve(path)
	return err != nil
}

// Gate 3: User approval — wait for confirmation after rule match
func AskUser(toolName string, args map[string]any, reason string) string {
	fmt.Printf("\n⚠  %s\n", reason)
	fmt.Printf("   Tool: %s(%v)\n", toolName, args)
	fmt.Print("   Allow? [y/N] ")

	reader := bufio.NewReader(os.Stdin)
	choice, _ := reader.ReadString('\n')
	choice = strings.ToLower(strings.TrimSpace(choice))
	if choice == "y" || choice == "yes" {
		return "allow"
	}
	return "deny"
}

// Authorize chains all three gates before a tool executes.
func Authorize(toolName string, args map[string]any) error {
	return authorize(toolName, args, true)
}

// AuthorizeNonInteractive 对定时触发的 Agent turn 禁止任何需要终端确认的操作。
func AuthorizeNonInteractive(toolName string, args map[string]any) error {
	return authorize(toolName, args, false)
}

func authorize(toolName string, args map[string]any, interactive bool) error {
	if toolName == "bash" {
		command, _ := args["command"].(string)
		if reason := CheckDenyList(command); reason != "" {
			return fmt.Errorf("%s", reason)
		}
	}

	if reason := CheckRules(toolName, args); reason != "" {
		if !interactive {
			return fmt.Errorf("permission denied: scheduled turns cannot request interactive approval")
		}
		decision := AskUser(toolName, args, reason)
		if decision == "deny" {
			return fmt.Errorf("permission denied: %s", reason)
		}
	}

	return nil
}

// CheckPermission is kept as a boolean teaching helper for s03.
func CheckPermission(toolName string, args map[string]any) bool {
	return Authorize(toolName, args) == nil
}
