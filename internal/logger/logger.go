// Package logger 提供全项目共享的极简标准库 Logger。
package logger

import (
	"fmt"
	"io"
	"log"
	"os"
	"strings"
	"sync"
)

// Mode 表示允许输出的最低日志等级。
type Mode uint8

const (
	ModeDebug Mode = iota
	ModeInfo
	ModeWarn
	ModeError
)

var (
	mu          sync.RWMutex
	currentMode = ModeInfo
	logger      = log.New(os.Stderr, "", log.LstdFlags)
)

// SetOutput 设置日志输出位置；日期和时间由 log.LstdFlags 统一添加。
func SetOutput(writer io.Writer) {
	if writer == nil {
		writer = os.Stderr
	}
	mu.Lock()
	logger.SetOutput(writer)
	mu.Unlock()
}

// ParseMode 将配置字符串转换为日志 Mode；空值默认使用 Info。
func ParseMode(value string) (Mode, error) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "", "info":
		return ModeInfo, nil
	case "debug":
		return ModeDebug, nil
	case "warn":
		return ModeWarn, nil
	case "error":
		return ModeError, nil
	default:
		return ModeInfo, fmt.Errorf("invalid log mode: %s", value)
	}
}

// SetMode 设置允许输出的最低日志等级。
func SetMode(mode Mode) {
	mu.Lock()
	currentMode = mode
	mu.Unlock()
}

// Debug 记录仅在 Debug Mode 下需要的诊断信息。
func Debug(format string, args ...any) {
	print(ModeDebug, "DEBUG", format, args...)
}

// Info 记录正常运行状态。
func Info(format string, args ...any) {
	print(ModeInfo, "INFO", format, args...)
}

// Warn 记录不影响继续运行但需要关注的信息。
func Warn(format string, args ...any) {
	print(ModeWarn, "WARN", format, args...)
}

// Error 记录需要关注的失败信息。
func Error(format string, args ...any) {
	print(ModeError, "ERROR", format, args...)
}

// print 根据当前 Mode 过滤低等级日志，并统一添加等级前缀。
func print(mode Mode, level, format string, args ...any) {
	mu.RLock()
	defer mu.RUnlock()
	if mode < currentMode {
		return
	}
	logger.Printf("["+level+"] "+format, args...)
}
