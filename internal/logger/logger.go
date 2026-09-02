// Package logger 提供全项目共享的极简标准库 Logger。
package logger

import (
	"io"
	"log"
	"os"
	"sync"
)

var (
	mu     sync.RWMutex
	logger = log.New(os.Stderr, "", log.LstdFlags)
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

// Info 记录正常运行状态。
func Info(format string, args ...any) {
	mu.RLock()
	defer mu.RUnlock()
	logger.Printf("[INFO] "+format, args...)
}

// Error 记录需要关注的失败信息。
func Error(format string, args ...any) {
	mu.RLock()
	defer mu.RUnlock()
	logger.Printf("[ERROR] "+format, args...)
}
