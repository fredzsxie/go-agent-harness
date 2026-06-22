package tools

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
)

var blockedCommands = []string{
	"rm -rf",
	"sudo ",
	":(){",
	"mkfs",
	"dd if=",
	"shutdown",
	"reboot",
	"poweroff",
	"kill -9 -1",
}

func RunBash(ctx context.Context, input any) (string, error) {
	command, _ := input.(map[string]any)["command"].(string)
	if command == "" {
		return "", fmt.Errorf("missing command")
	}

	lower := strings.ToLower(command)
	for _, blocked := range blockedCommands {
		if strings.Contains(lower, blocked) {
			return "", fmt.Errorf("blocked dangerous command")
		}
	}

	cmd := exec.CommandContext(ctx, "/bin/bash", "-lc", command)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return string(output), err
	}
	return string(output), nil
}
