package builtin

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"sync"
	"syscall"
	"time"

	"go-agent-harness/internal/logger"
)

const (
	bashTimeout   = 120 * time.Second
	bashOutputMax = 50000
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
	return defaultTools.RunBash(ctx, input)
}

func (t *Tools) RunBash(ctx context.Context, input any) (string, error) {
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

	resolver, err := t.currentResolver()
	if err != nil {
		return "", err
	}
	logger.Debug("[Bash] starting: %s", previewCommand(command, 80))
	started := time.Now()
	output, truncated, err := runBashCommand(ctx, command, resolver.Root(), bashTimeout)
	if truncated {
		logger.Warn("[Bash] output truncated to %d bytes", bashOutputMax)
	}
	if err != nil {
		logger.Error("[Bash] failed after %s: %v", time.Since(started).Round(time.Millisecond), err)
		return output, err
	}
	logger.Info("[Bash] completed in %s", time.Since(started).Round(time.Millisecond))
	return output, nil
}

func runBashCommand(ctx context.Context, command, workDir string, timeout time.Duration) (string, bool, error) {
	commandCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	buffer := &limitedBuffer{limit: bashOutputMax}
	cmd := exec.CommandContext(commandCtx, "/bin/bash", "-lc", command)
	cmd.Dir = workDir
	cmd.Stdout = buffer
	cmd.Stderr = buffer
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.WaitDelay = 250 * time.Millisecond
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return os.ErrProcessDone
		}
		return stopProcessGroup(cmd.Process.Pid)
	}
	if err := cmd.Start(); err != nil {
		return "", false, err
	}
	pid := cmd.Process.Pid
	err := cmd.Wait()
	// shell 退出后仍可能留下子进程，统一回收整个 process group。
	_ = stopProcessGroup(pid)
	output, truncated := buffer.Result()
	if errors.Is(commandCtx.Err(), context.DeadlineExceeded) {
		return output, truncated, fmt.Errorf("bash command timed out after %s", timeout)
	}
	if err != nil {
		return output, truncated, err
	}
	return output, truncated, nil
}

func stopProcessGroup(pid int) error {
	if pid <= 0 {
		return os.ErrProcessDone
	}
	if err := syscall.Kill(-pid, syscall.SIGTERM); err != nil {
		if errors.Is(err, syscall.ESRCH) {
			return nil
		}
		return err
	}
	time.Sleep(50 * time.Millisecond)
	if err := syscall.Kill(-pid, syscall.SIGKILL); err != nil && !errors.Is(err, syscall.ESRCH) {
		return err
	}
	return nil
}

type limitedBuffer struct {
	mu        sync.Mutex
	buffer    bytes.Buffer
	limit     int
	truncated bool
}

func (b *limitedBuffer) Write(data []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	written := len(data)
	remaining := b.limit - b.buffer.Len()
	if remaining <= 0 {
		b.truncated = true
		return written, nil
	}
	if len(data) > remaining {
		_, _ = b.buffer.Write(data[:remaining])
		b.truncated = true
		return written, nil
	}
	_, _ = b.buffer.Write(data)
	return written, nil
}

func (b *limitedBuffer) Result() (string, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buffer.String(), b.truncated
}

func previewCommand(command string, limit int) string {
	value := strings.Join(strings.Fields(command), " ")
	if len(value) > limit {
		return value[:limit] + "..."
	}
	return value
}
