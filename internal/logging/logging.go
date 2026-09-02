// Package logging 提供全项目共享的极简标准库 Logger。
package logging

import (
	"io"
	"log"
	"os"
	"sync"
)

var (
	mu     sync.RWMutex
	logger = log.New(os.Stdout, "", log.LstdFlags)
)

// SetOutput 设置日志输出位置；日期和时间由 log.LstdFlags 统一添加。
func SetOutput(writer io.Writer) {
	if writer == nil {
		writer = os.Stdout
	}
	mu.Lock()
	logger.SetOutput(writer)
	mu.Unlock()
}

func Printf(format string, args ...any) {
	mu.RLock()
	defer mu.RUnlock()
	logger.Printf(format, args...)
}

func Println(args ...any) {
	mu.RLock()
	defer mu.RUnlock()
	logger.Println(args...)
}
