package workflow

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"go-agent-harness/internal/logger"
)

const maxJournalRecordSize = 4 << 20

type journalRecord struct {
	Key   string          `json:"key"`
	Value json.RawMessage `json:"value"`
}

// Journal 逐条持久化 agent 结果；cache 保存原始 JSON，读取时返回独立副本。
type Journal struct {
	mu    sync.Mutex
	file  *os.File
	cache map[string]json.RawMessage
}

func (s *Store) journalPath(runID string) string {
	return filepath.Join(s.dir, runID+".journal.jsonl")
}

func (s *Store) OpenJournal(runID string, resume bool) (*Journal, error) {
	if err := validateRunID(runID); err != nil {
		return nil, err
	}
	if err := s.ready(); err != nil {
		return nil, err
	}
	path := s.journalPath(runID)
	cache := make(map[string]json.RawMessage)
	if resume {
		if err := loadJournal(path, cache); err != nil {
			return nil, err
		}
	}
	flags := os.O_CREATE | os.O_APPEND | os.O_WRONLY
	if !resume {
		flags |= os.O_EXCL
	}
	file, err := os.OpenFile(path, flags, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open workflow journal %s: %w", runID, err)
	}
	logger.Debug("[Workflow] opened journal run=%s resume=%t cached=%d", runID, resume, len(cache))
	return &Journal{file: file, cache: cache}, nil
}

func loadJournal(path string, cache map[string]json.RawMessage) error {
	file, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("open resume journal: %w", err)
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 64*1024), maxJournalRecordSize)
	line := 0
	for scanner.Scan() {
		line++
		var record journalRecord
		if err := decodeStrictJSON(scanner.Bytes(), &record); err != nil || strings.TrimSpace(record.Key) == "" || record.Value == nil {
			if err == nil {
				err = fmt.Errorf("expected key and value")
			}
			return fmt.Errorf("invalid resume journal record at line %d: %w", line, err)
		}
		cache[record.Key] = append(json.RawMessage(nil), record.Value...)
	}
	if err := scanner.Err(); err != nil {
		return fmt.Errorf("read resume journal: %w", err)
	}
	return nil
}

func (j *Journal) Cached(key string) (any, bool, error) {
	j.mu.Lock()
	defer j.mu.Unlock()
	raw, ok := j.cache[key]
	if !ok {
		return nil, false, nil
	}
	var value any
	if err := json.Unmarshal(raw, &value); err != nil {
		return nil, false, fmt.Errorf("decode cached workflow result: %w", err)
	}
	logger.Debug("[Workflow] journal cache hit key=%s", key)
	return value, true, nil
}

// Record 在更新内存 cache 前同步 JSONL，确保已命中的结果一定已经落盘。
func (j *Journal) Record(key string, value any) error {
	if j == nil {
		return fmt.Errorf("workflow journal is closed")
	}
	if strings.TrimSpace(key) == "" {
		return fmt.Errorf("workflow journal key is required")
	}
	raw, err := json.Marshal(value)
	if err != nil {
		return err
	}
	record, err := json.Marshal(journalRecord{Key: key, Value: raw})
	if err != nil {
		return err
	}
	record = append(record, '\n')

	j.mu.Lock()
	defer j.mu.Unlock()
	if j.file == nil {
		return fmt.Errorf("workflow journal is closed")
	}
	if _, err := j.file.Write(record); err != nil {
		return err
	}
	if err := j.file.Sync(); err != nil {
		return err
	}
	j.cache[key] = append(json.RawMessage(nil), raw...)
	logger.Debug("[Workflow] journal checkpoint key=%s", key)
	return nil
}

func (j *Journal) Close() error {
	if j == nil {
		return nil
	}
	j.mu.Lock()
	defer j.mu.Unlock()
	if j.file == nil {
		return nil
	}
	err := j.file.Close()
	j.file = nil
	return err
}

// SemanticKey 仅依赖调用语义，不使用并发完成顺序，因此恢复时可以稳定命中。
func SemanticKey(kind, label, prompt string, schema map[string]any) (string, error) {
	canonicalSchema, err := json.Marshal(schema)
	if err != nil {
		return "", err
	}
	basis := strings.Join([]string{kind, label, prompt, string(canonicalSchema)}, "\x00")
	sum := sha256.Sum256([]byte(basis))
	return kind + "-" + hex.EncodeToString(sum[:8]), nil
}
