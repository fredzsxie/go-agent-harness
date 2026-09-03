package team

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"go-agent-harness/internal/agent"
	"go-agent-harness/internal/logger"
	"go-agent-harness/internal/protocol"
	"go-agent-harness/internal/task"
	"go-agent-harness/internal/workspace"
	"go-agent-harness/internal/worktree"
)

const defaultIdleInterval = 2 * time.Second

var teammateBaseTools = []string{"bash", "read_file", "write_file", "edit_file", "glob"}

// TeammateStatus 表示持久化 Teammate 当前所处的生命周期阶段。
type TeammateStatus string

const (
	TeammateWorking         TeammateStatus = "working"
	TeammateWaitingApproval TeammateStatus = "waiting_approval"
	TeammateIdle            TeammateStatus = "idle"
	TeammateStopping        TeammateStatus = "stopping"
)

// TeammateInfo 是供 Lead 查看状态的只读快照。
type TeammateInfo struct {
	Name        string
	Role        string
	Status      TeammateStatus
	Plan        PlanStatus
	TaskID      string
	Worktree    string
	WorkVersion uint64
}

// RuntimeConfig 注入 Team Runtime 依赖；BaseTools 应只包含同步基础工具。
type RuntimeConfig struct {
	Model        agent.Model
	BaseTools    *agent.Registry
	Tasks        *task.Manager
	Worktrees    *worktree.Manager
	Bus          *Bus
	Requests     *Requests
	IdleInterval time.Duration
}

// Runtime 管理多个拥有独立消息历史的持久化 Teammate。
type Runtime struct {
	model        agent.Model
	baseTools    *agent.Registry
	tasks        *task.Manager
	worktrees    *worktree.Manager
	bus          *Bus
	requests     *Requests
	idleInterval time.Duration
	ctx          context.Context
	cancel       context.CancelFunc
	mu           sync.Mutex
	peers        map[string]*teammate
	wg           sync.WaitGroup
	closed       bool
}

type assignment struct {
	taskID string
	root   string
}

type teammate struct {
	name          string
	role          string
	status        TeammateStatus
	gate          PlanStatus
	planRequestID string
	workVersion   uint64
	assignment    *assignment
	messages      []protocol.Message
	worker        *agent.Worker
	runtime       *Runtime
}

// NewRuntime 创建独立于 Lead 单次会话 Context 的 Team 生命周期。
func NewRuntime(cfg RuntimeConfig) *Runtime {
	interval := cfg.IdleInterval
	if interval <= 0 {
		interval = defaultIdleInterval
	}
	ctx, cancel := context.WithCancel(context.Background())
	return &Runtime{
		model: cfg.Model, baseTools: cfg.BaseTools, tasks: cfg.Tasks,
		worktrees: cfg.Worktrees, bus: cfg.Bus, requests: cfg.Requests,
		idleInterval: interval, ctx: ctx, cancel: cancel,
		peers: make(map[string]*teammate),
	}
}

// Spawn 先占用名称并认领可选初始 Task，再启动持久化 goroutine。
func (r *Runtime) Spawn(name, role, initialPrompt, taskID string, requirePlan bool) error {
	name = strings.TrimSpace(name)
	role = strings.TrimSpace(role)
	initialPrompt = strings.TrimSpace(initialPrompt)
	if !validAgentName(name) || strings.EqualFold(name, "lead") || strings.EqualFold(name, task.AgentOwner) {
		return fmt.Errorf("invalid or reserved teammate name: %s", name)
	}
	if role == "" || initialPrompt == "" {
		return errors.New("teammate role and prompt are required")
	}
	if err := r.ready(); err != nil {
		return err
	}

	peer := &teammate{
		name: name, role: role, status: TeammateWorking,
		gate: PlanNotRequired, runtime: r,
	}
	if requirePlan {
		peer.gate = PlanRequired
	}
	registry, err := r.registryFor(peer)
	if err != nil {
		return err
	}
	peer.worker = agent.NewWorker(r.model, registry, r.hooksFor(peer))

	r.mu.Lock()
	if r.closed {
		r.mu.Unlock()
		return errors.New("team runtime is closed")
	}
	for existing := range r.peers {
		if strings.EqualFold(existing, name) {
			r.mu.Unlock()
			return fmt.Errorf("teammate already exists: %s", name)
		}
	}
	r.peers[name] = peer
	// 在释放锁前登记 WaitGroup，避免 Close 与 Spawn 并发时漏等尚未启动的 goroutine。
	r.wg.Add(1)
	r.mu.Unlock()

	if taskID != "" {
		if _, err := r.claim(peer, taskID); err != nil {
			r.mu.Lock()
			delete(r.peers, name)
			r.mu.Unlock()
			r.wg.Done()
			return fmt.Errorf("cannot assign %s to %s: %w", taskID, name, err)
		}
	}
	peer.messages = []protocol.Message{{Role: protocol.RoleUser, Content: r.initialMessage(peer, initialPrompt, requirePlan)}}

	go peer.run()
	logger.Info("[TeamRuntime] Spawned %s as %s", name, role)
	return nil
}

// List 返回名称排序后的 Teammate 状态快照。
func (r *Runtime) List() []TeammateInfo {
	r.mu.Lock()
	defer r.mu.Unlock()
	items := make([]TeammateInfo, 0, len(r.peers))
	for _, peer := range r.peers {
		info := TeammateInfo{
			Name: peer.name, Role: peer.role, Status: peer.status,
			Plan: peer.gate, WorkVersion: peer.workVersion,
		}
		if peer.assignment != nil {
			info.TaskID = peer.assignment.taskID
			if item, err := r.tasks.Get(peer.assignment.taskID); err == nil && item.Worktree != nil {
				info.Worktree = *item.Worktree
			}
		}
		items = append(items, info)
	}
	sort.Slice(items, func(i, j int) bool { return items[i].Name < items[j].Name })
	return items
}

// Send 从 Lead 向仍活跃的 Teammate 投递普通协作消息。
func (r *Runtime) Send(to, content string) error {
	if _, err := r.active(to); err != nil {
		return err
	}
	_, err := r.bus.Send("lead", to, content, MessageText, Metadata{})
	return err
}

// RequestPlan 激活 Plan Gate，并通知 Teammate 在修改 Workspace 前提交计划。
func (r *Runtime) RequestPlan(name, instruction string) error {
	peer, err := r.active(name)
	if err != nil {
		return err
	}
	r.mu.Lock()
	if peer.gate == PlanPending {
		r.mu.Unlock()
		return errors.New("a plan is already waiting for review")
	}
	previous := peer.gate
	peer.gate = PlanRequired
	r.mu.Unlock()
	if _, err := r.bus.Send("lead", name, instruction, MessagePlanRequest, Metadata{}); err != nil {
		r.mu.Lock()
		peer.gate = previous
		r.mu.Unlock()
		return err
	}
	logger.Info("[TeamRuntime] Plan requested from %s", name)
	return nil
}

// ReviewPlan 只审批 Teammate 当前 assignment 对应的最新计划。
func (r *Runtime) ReviewPlan(requestID string, approve bool, feedback string) error {
	request, ok := r.requests.Get(requestID)
	if !ok || request.Type != RequestPlanApproval {
		return fmt.Errorf("plan request not found: %s", requestID)
	}
	peer, err := r.active(request.Sender)
	if err != nil {
		return err
	}
	r.mu.Lock()
	identity := r.identityLocked(peer)
	current := peer.planRequestID == requestID && sameIdentity(request.WorkIdentity, identity)
	r.mu.Unlock()
	if !current {
		return fmt.Errorf("plan request belongs to an earlier assignment: %s", requestID)
	}
	content := strings.TrimSpace(feedback)
	if content == "" {
		if approve {
			content = "Plan approved."
		} else {
			content = "Revise the plan and submit it again."
		}
	}
	response := Message{
		From: "lead", To: peer.name, Content: content,
		Type:      MessagePlanApprovalResponse,
		Timestamp: float64(time.Now().UnixNano()) / float64(time.Second),
		Metadata:  Metadata{RequestID: requestID, Approve: &approve, Feedback: feedback},
	}
	matched, err := r.requests.Match(response)
	if err != nil {
		return err
	}
	if _, err := r.bus.SendMessage(response); err != nil {
		return err
	}
	logger.Info("[TeamRuntime] Plan %s for %s (%s)", matched.Status, peer.name, requestID)
	return nil
}

// RequestShutdown 开启带 request_id 的优雅停止握手。
func (r *Runtime) RequestShutdown(name string) (string, error) {
	if _, err := r.active(name); err != nil {
		return "", err
	}
	request, err := r.requests.Open(RequestShutdown, "lead", name, "Finish the current step and shut down.", nil)
	if err != nil {
		return "", err
	}
	if _, err := r.bus.SendMessage(request.Message()); err != nil {
		return "", err
	}
	logger.Info("[TeamRuntime] Shutdown requested from %s (%s)", name, request.ID)
	return request.ID, nil
}

// InUse 判断路径是否仍被某个 Teammate 的当前模型回合租用。
func (r *Runtime) InUse(path string) bool {
	path = filepath.Clean(path)
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, peer := range r.peers {
		if peer.assignment != nil && filepath.Clean(peer.assignment.root) == path {
			return true
		}
	}
	return false
}

// Close 取消所有 Teammate，并等待其释放尚未完成的 Task。
func (r *Runtime) Close() {
	r.mu.Lock()
	if !r.closed {
		r.closed = true
		r.cancel()
	}
	r.mu.Unlock()
	r.wg.Wait()
}

func (r *Runtime) ready() error {
	switch {
	case r == nil:
		return errors.New("team runtime is nil")
	case r.model == nil:
		return errors.New("team model is required")
	case r.baseTools == nil:
		return errors.New("teammate base tools are required")
	case r.tasks == nil:
		return errors.New("task manager is required")
	case r.worktrees == nil:
		return errors.New("worktree manager is required")
	case r.bus == nil:
		return errors.New("team bus is required")
	case r.requests == nil:
		return errors.New("team protocol requests are required")
	default:
		return nil
	}
}

func (r *Runtime) active(name string) (*teammate, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	peer, ok := r.peers[name]
	if !ok || peer.status == TeammateStopping {
		return nil, fmt.Errorf("teammate is not active: %s", name)
	}
	return peer, nil
}

func (r *Runtime) initialMessage(peer *teammate, prompt string, requirePlan bool) string {
	if peer.assignment != nil {
		if item, err := r.tasks.Get(peer.assignment.taskID); err == nil {
			prompt += fmt.Sprintf("\n\n[Assigned task %s] %s\n%s\nWork directory: %s", item.ID, item.Subject, item.Description, peer.assignment.root)
		}
	}
	if requirePlan {
		prompt += "\n\n[Plan required] Submit a plan and wait for Lead approval before changing files or using bash."
	}
	return prompt
}

func (r *Runtime) systemPrompt(peer *teammate) string {
	return fmt.Sprintf(
		"You are %q, a %s. Use tools to complete the assigned Task, then call complete_task and report a concise result. "+
			"An [Assigned task] is already claimed. When a plan is required, call submit_plan and wait for Lead approval before bash or file changes. "+
			"File and shell tools use the Task working directory, which is not a sandbox. Use send_message only for intermediate coordination.",
		peer.name, peer.role,
	)
}

func sameIdentity(left, right *WorkIdentity) bool {
	if left == nil || right == nil {
		return left == right
	}
	return left.Version == right.Version && left.TaskID == right.TaskID
}

func (r *Runtime) identityLocked(peer *teammate) *WorkIdentity {
	identity := &WorkIdentity{Version: peer.workVersion}
	if peer.assignment != nil {
		identity.TaskID = peer.assignment.taskID
	}
	return identity
}

func (r *Runtime) currentResolver(peer *teammate) (*workspace.Resolver, error) {
	r.mu.Lock()
	if peer.assignment == nil {
		r.mu.Unlock()
		return nil, errors.New("claim a Task before using workspace tools")
	}
	assigned := *peer.assignment
	r.mu.Unlock()

	item, err := r.tasks.Get(assigned.taskID)
	if err != nil {
		return nil, err
	}
	if item.Owner == nil || *item.Owner != peer.name || (item.Status != task.InProgress && item.Status != task.Completed) {
		return nil, fmt.Errorf("task assignment is no longer active: %s", item.ID)
	}
	resolver, err := r.worktrees.Resolve(r.ctx, item)
	if err != nil {
		return nil, err
	}
	if resolver.Root() != assigned.root {
		return nil, fmt.Errorf("task workspace changed: %s", item.ID)
	}
	return resolver, nil
}

// claim 在持久化 Task 成功认领后建立本轮 Workspace 租约和工作版本。
func (r *Runtime) claim(peer *teammate, taskID string) (task.Task, error) {
	r.mu.Lock()
	if peer.assignment != nil {
		r.mu.Unlock()
		return task.Task{}, fmt.Errorf("teammate already has task %s", peer.assignment.taskID)
	}
	r.mu.Unlock()
	claimed, err := r.tasks.Claim(taskID, peer.name)
	if err != nil {
		return task.Task{}, err
	}
	// Claim 返回锁内读取的最新 Task；据此解析 Workspace，避免认领前绑定变化造成错租目录。
	resolver, err := r.worktrees.Resolve(r.ctx, claimed)
	if err != nil {
		_, _ = r.tasks.ReleaseOwner(peer.name)
		return task.Task{}, err
	}
	r.mu.Lock()
	if peer.assignment != nil {
		r.mu.Unlock()
		_, _ = r.tasks.ReleaseOwner(peer.name)
		return task.Task{}, errors.New("teammate assignment changed while claiming task")
	}
	peer.assignment = &assignment{taskID: claimed.ID, root: resolver.Root()}
	peer.workVersion++
	r.mu.Unlock()
	logger.Info("[TeamRuntime] %s assigned %s at %s", peer.name, claimed.ID, resolver.Root())
	return claimed, nil
}

func (r *Runtime) claimNext(peer *teammate) (*task.Task, error) {
	r.mu.Lock()
	if peer.assignment != nil {
		r.mu.Unlock()
		return nil, nil
	}
	r.mu.Unlock()
	items, err := r.tasks.List()
	if err != nil {
		return nil, err
	}
	for _, candidate := range items {
		if candidate.Status != task.Pending || candidate.Owner != nil {
			continue
		}
		if _, err := r.worktrees.Resolve(r.ctx, candidate); err != nil {
			logger.Warn("[TeamRuntime] Skipped task %s with invalid workspace: %v", candidate.ID, err)
			continue
		}
		claimed, err := r.claim(peer, candidate.ID)
		if err != nil {
			continue
		}
		return &claimed, nil
	}
	return nil, nil
}

func (r *Runtime) releaseCompleted(peer *teammate) {
	r.mu.Lock()
	if peer.assignment == nil {
		r.mu.Unlock()
		return
	}
	taskID := peer.assignment.taskID
	r.mu.Unlock()
	item, err := r.tasks.Get(taskID)
	if err != nil || item.Status != task.Completed || item.Owner == nil || *item.Owner != peer.name {
		return
	}
	r.mu.Lock()
	if peer.assignment != nil && peer.assignment.taskID == taskID {
		peer.assignment = nil
		peer.workVersion++
		peer.gate = PlanNotRequired
		peer.planRequestID = ""
	}
	r.mu.Unlock()
	logger.Info("[TeamRuntime] %s released completed assignment %s", peer.name, taskID)
}

func (r *Runtime) cleanup(peer *teammate) {
	if released, err := r.tasks.ReleaseOwner(peer.name); err != nil {
		logger.Error("[TeamRuntime] Release assignment for %s: %v", peer.name, err)
	} else if released {
		logger.Warn("[TeamRuntime] Returned unfinished task owned by %s to pending", peer.name)
	}
	r.mu.Lock()
	delete(r.peers, peer.name)
	peer.assignment = nil
	peer.workVersion++
	r.mu.Unlock()
	logger.Info("[TeamRuntime] Teammate %s stopped", peer.name)
}

func (r *Runtime) setStatus(peer *teammate, status TeammateStatus) {
	r.mu.Lock()
	peer.status = status
	r.mu.Unlock()
	logger.Debug("[TeamRuntime] %s -> %s", peer.name, status)
}
