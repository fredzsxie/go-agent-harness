package workflow

import (
	"bytes"
	cryptorand "crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"

	"golang.org/x/sys/unix"

	"go-agent-harness/internal/logger"
	"go-agent-harness/internal/workspace"
)

const defaultStoreDir = ".workflows"

var runIDPattern = regexp.MustCompile(`^wf_[A-Za-z0-9][A-Za-z0-9._-]{0,63}_[0-9a-f]{16}$`)

type StoreConfig struct {
	WorkDir string
	Dir     string
}

type Snapshot struct {
	RunID        string         `json:"runId"`
	WorkflowName string         `json:"workflowName"`
	Args         map[string]any `json:"args"`
	Task         Task           `json:"task"`
}

// Store 管理 Workflow artifact，并确保同一个 run 不能被并发执行或恢复。
type Store struct {
	dir        string
	displayDir string
	initErr    error
	mu         sync.Mutex
	active     map[string]bool
}

func NewStore(config StoreConfig) *Store {
	workDir := strings.TrimSpace(config.WorkDir)
	if workDir == "" {
		workDir = "."
	}
	dirName := strings.TrimSpace(config.Dir)
	if dirName == "" {
		dirName = defaultStoreDir
	}
	resolver, err := workspace.New(workDir)
	if err != nil {
		return &Store{initErr: err, active: make(map[string]bool)}
	}
	dir, err := resolver.Resolve(dirName)
	displayDir := dirName
	if err == nil {
		if relative, relativeErr := filepath.Rel(resolver.Root(), dir); relativeErr == nil {
			displayDir = relative
		}
	}
	return &Store{dir: dir, displayDir: displayDir, initErr: err, active: make(map[string]bool)}
}

// ReserveRun 使用独占创建先占用 snapshot 文件，避免并发生成相同 run ID。
func (s *Store) ReserveRun(workflowName string) (string, error) {
	if !workflowNamePattern.MatchString(workflowName) {
		return "", fmt.Errorf("invalid workflow name")
	}
	if err := s.ready(); err != nil {
		return "", err
	}
	for range 16 {
		suffix := make([]byte, 8)
		if _, err := cryptorand.Read(suffix); err != nil {
			return "", err
		}
		runID := "wf_" + workflowName + "_" + hex.EncodeToString(suffix)
		file, err := os.OpenFile(s.snapshotPath(runID), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		if os.IsExist(err) {
			continue
		}
		if err != nil {
			return "", err
		}
		if err := file.Close(); err != nil {
			return "", err
		}
		logger.Info("[Workflow] reserved run %s", runID)
		return runID, nil
	}
	return "", fmt.Errorf("could not allocate a unique workflow run ID")
}

// LockRun 在完整生命周期内持有非阻塞锁，重复执行会立即失败而不是等待。
func (s *Store) LockRun(runID string) (func(), error) {
	if err := validateRunID(runID); err != nil {
		return nil, err
	}
	if err := s.ready(); err != nil {
		return nil, err
	}

	s.mu.Lock()
	if s.active[runID] {
		s.mu.Unlock()
		return nil, fmt.Errorf("workflow run %s is already active", runID)
	}
	s.active[runID] = true
	s.mu.Unlock()

	lock, err := os.OpenFile(s.lockPath(runID), os.O_CREATE|os.O_RDWR, 0o600)
	if err == nil {
		err = unix.Flock(int(lock.Fd()), unix.LOCK_EX|unix.LOCK_NB)
	}
	if err != nil {
		if lock != nil {
			_ = lock.Close()
		}
		s.releaseActive(runID)
		return nil, fmt.Errorf("workflow run %s is already active: %w", runID, err)
	}

	var once sync.Once
	return func() {
		once.Do(func() {
			if err := unix.Flock(int(lock.Fd()), unix.LOCK_UN); err != nil {
				logger.Error("[Workflow] unlock run %s: %v", runID, err)
			}
			if err := lock.Close(); err != nil {
				logger.Error("[Workflow] close run lock %s: %v", runID, err)
			}
			s.releaseActive(runID)
		})
	}, nil
}

func (s *Store) WriteSnapshot(snapshot Snapshot) error {
	if err := validateRunID(snapshot.RunID); err != nil {
		return err
	}
	if !workflowNamePattern.MatchString(snapshot.WorkflowName) {
		return fmt.Errorf("invalid snapshot workflow name")
	}
	if snapshot.Args == nil {
		snapshot.Args = map[string]any{}
	}
	return s.writeJSON(s.snapshotPath(snapshot.RunID), snapshot)
}

func (s *Store) ReadSnapshot(runID string) (Snapshot, error) {
	if err := validateRunID(runID); err != nil {
		return Snapshot{}, err
	}
	if err := s.ready(); err != nil {
		return Snapshot{}, err
	}
	data, err := os.ReadFile(s.snapshotPath(runID))
	if err != nil {
		return Snapshot{}, fmt.Errorf("read workflow snapshot %s: %w", runID, err)
	}
	var snapshot Snapshot
	if err := decodeStrictJSON(data, &snapshot); err != nil {
		return Snapshot{}, fmt.Errorf("invalid workflow snapshot %s: %w", runID, err)
	}
	if snapshot.RunID != runID || !workflowNamePattern.MatchString(snapshot.WorkflowName) || snapshot.Args == nil {
		return Snapshot{}, fmt.Errorf("invalid workflow snapshot %s", runID)
	}
	return snapshot, nil
}

func (s *Store) WriteOutput(runID string, value any) (string, error) {
	if err := validateRunID(runID); err != nil {
		return "", err
	}
	if err := s.writeJSON(s.outputPath(runID), value); err != nil {
		return "", err
	}
	return filepath.Join(s.displayDir, runID+".output.json"), nil
}

func (s *Store) ready() error {
	if s == nil {
		return fmt.Errorf("workflow store is nil")
	}
	if s.initErr != nil {
		return s.initErr
	}
	return os.MkdirAll(s.dir, 0o700)
}

// writeJSON 先同步临时文件再原子替换，避免恢复时读取半条 snapshot 或 output。
func (s *Store) writeJSON(path string, value any) error {
	if err := s.ready(); err != nil {
		return err
	}
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	temporary, err := os.CreateTemp(s.dir, ".workflow-*.tmp")
	if err != nil {
		return err
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if err = temporary.Chmod(0o600); err == nil {
		_, err = temporary.Write(data)
	}
	if err == nil {
		err = temporary.Sync()
	}
	if closeErr := temporary.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	return os.Rename(temporaryPath, path)
}

func (s *Store) releaseActive(runID string) {
	s.mu.Lock()
	delete(s.active, runID)
	s.mu.Unlock()
}

func (s *Store) snapshotPath(runID string) string { return filepath.Join(s.dir, runID+".json") }
func (s *Store) outputPath(runID string) string   { return filepath.Join(s.dir, runID+".output.json") }
func (s *Store) lockPath(runID string) string     { return filepath.Join(s.dir, runID+".lock") }

func validateRunID(runID string) error {
	if !runIDPattern.MatchString(runID) {
		return fmt.Errorf("invalid workflow run ID")
	}
	return nil
}

func decodeStrictJSON(data []byte, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		if err == nil {
			return fmt.Errorf("unexpected trailing JSON value")
		}
		return err
	}
	return nil
}
