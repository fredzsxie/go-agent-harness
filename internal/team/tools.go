package team

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"go-agent-harness/internal/hooks"
	"go-agent-harness/internal/logger"
	"go-agent-harness/internal/permission"
	"go-agent-harness/internal/task"
	"go-agent-harness/internal/tool"
	"go-agent-harness/internal/tool/builtin"
	"go-agent-harness/internal/workspace"
)

func (r *Runtime) registryFor(peer *teammate) (*tool.Registry, error) {
	registry, err := r.baseTools.Select(teammateBaseTools...)
	if err != nil {
		return nil, err
	}
	tools := builtin.NewDynamic(func() (*workspace.Resolver, error) {
		return r.currentResolver(peer)
	})
	bindings := map[string]tool.Handler{
		"bash": tools.RunBash, "read_file": tools.RunReadFile,
		"write_file": tools.RunWriteFile, "edit_file": tools.RunEditFile,
		"glob": tools.RunGlob,
	}
	for name, handler := range bindings {
		if err := registry.Rebind(name, handler); err != nil {
			return nil, err
		}
	}
	registerTeammateTools(registry, peer)
	return registry, nil
}

func registerTeammateTools(registry *tool.Registry, peer *teammate) {
	taskID := map[string]any{"type": "string", "pattern": `^task_[0-9a-f]{8}$`}
	registry.Register(tool.Spec{Name: "send_message", Description: "Send an intermediate message to lead or an active teammate.",
		Required: []string{"to", "content"}, Properties: map[string]any{
			"to": map[string]any{"type": "string"}, "content": map[string]any{"type": "string", "minLength": 1},
		}}, peer.runSendMessage)
	registry.Register(tool.Spec{Name: "submit_plan", Description: "Submit a work plan for Lead approval.",
		Required: []string{"plan"}, Properties: map[string]any{"plan": map[string]any{"type": "string", "minLength": 1}}}, peer.runSubmitPlan)
	registry.Register(tool.Spec{Name: "list_tasks", Description: "List shared tasks."}, func(_ context.Context, _ any) (string, error) {
		items, err := peer.runtime.tasks.List()
		if err != nil {
			return "", err
		}
		return task.FormatList(items), nil
	})
	registry.Register(tool.Spec{Name: "claim_task", Description: "Claim a ready task.",
		Required: []string{"task_id"}, Properties: map[string]any{"task_id": taskID}}, peer.runClaim)
	registry.Register(tool.Spec{Name: "complete_task", Description: "Complete a task owned by this teammate.",
		Required: []string{"task_id"}, Properties: map[string]any{"task_id": taskID}}, peer.runComplete)
}

func (r *Runtime) hooksFor(peer *teammate) *hooks.Manager {
	manager := hooks.NewManager()
	// 权限与文件工具使用同一个 assignment 目录，不能回退到进程默认工作区。
	authorizer := permission.New(func(path string) (string, error) {
		resolver, err := r.currentResolver(peer)
		if err != nil {
			return "", err
		}
		return resolver.Resolve(path)
	}, nil)
	manager.BeforeTool(func(_ context.Context, call hooks.ToolCall) string {
		if isMutatingTool(call.Name) {
			r.mu.Lock()
			gate := peer.gate
			r.mu.Unlock()
			if gate.blocksMutation() {
				logger.Warn("[TeamRuntime] %s blocked %s while plan is %s", peer.name, call.Name, gate)
				return fmt.Sprintf("Blocked: plan status is %s. Submit or revise the plan and wait for approval before changing the workspace.", gate)
			}
		}
		if err := authorizer.Authorize(call.Name, call.Input, false); err != nil {
			logger.Warn("[TeamRuntime] %s permission denied for %s: %v", peer.name, call.Name, err)
			return err.Error()
		}
		logger.Info("[TeamRuntime] %s tool %s", peer.name, call.Name)
		return ""
	})
	return manager
}

func isMutatingTool(name string) bool {
	return name == "bash" || name == "write_file" || name == "edit_file"
}

func (p *teammate) runSendMessage(_ context.Context, input any) (string, error) {
	args, err := toolObject(input)
	if err != nil {
		return "", err
	}
	target, err := toolString(args, "to")
	if err != nil {
		return "", err
	}
	content, err := toolString(args, "content")
	if err != nil {
		return "", err
	}
	if target != "lead" {
		if _, err := p.runtime.active(target); err != nil {
			return "", err
		}
	}
	if _, err := p.runtime.bus.Send(p.name, target, content, MessageText, Metadata{}); err != nil {
		return "", err
	}
	return "Sent to " + target, nil
}

func (p *teammate) runSubmitPlan(_ context.Context, input any) (string, error) {
	args, err := toolObject(input)
	if err != nil {
		return "", err
	}
	plan, err := toolString(args, "plan")
	if err != nil {
		return "", err
	}
	p.runtime.mu.Lock()
	if p.gate == PlanPending {
		p.runtime.mu.Unlock()
		return "", errors.New("a plan is already waiting for review")
	}
	previousGate := p.gate
	previousStatus := p.status
	previousRequestID := p.planRequestID
	identity := p.runtime.identityLocked(p)
	p.runtime.mu.Unlock()
	request, err := p.runtime.requests.Open(RequestPlanApproval, p.name, "lead", plan, identity)
	if err != nil {
		return "", err
	}
	p.runtime.mu.Lock()
	p.gate = PlanPending
	p.status = TeammateWaitingApproval
	p.planRequestID = request.ID
	p.runtime.mu.Unlock()
	if _, err := p.runtime.bus.SendMessage(request.Message()); err != nil {
		p.runtime.requests.Cancel(request.ID)
		p.runtime.mu.Lock()
		if p.planRequestID == request.ID {
			p.gate = previousGate
			p.status = previousStatus
			p.planRequestID = previousRequestID
		}
		p.runtime.mu.Unlock()
		return "", err
	}
	logger.Info("[TeamRuntime] %s submitted plan %s", p.name, request.ID)
	return fmt.Sprintf("Plan submitted (%s). Wait for Lead's decision.", request.ID), nil
}

func (p *teammate) runClaim(_ context.Context, input any) (string, error) {
	id, err := toolTaskID(input)
	if err != nil {
		return "", err
	}
	claimed, err := p.runtime.claim(p, id)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("Claimed %s (%s)", claimed.ID, claimed.Subject), nil
}

func (p *teammate) runComplete(_ context.Context, input any) (string, error) {
	id, err := toolTaskID(input)
	if err != nil {
		return "", err
	}
	p.runtime.mu.Lock()
	gate := p.gate
	assigned := p.assignment != nil && p.assignment.taskID == id
	p.runtime.mu.Unlock()
	if gate.blocksMutation() {
		return "", fmt.Errorf("task %s cannot complete while plan status is %s", id, gate)
	}
	if !assigned {
		return "", fmt.Errorf("task is not the current assignment: %s", id)
	}
	completed, unblocked, err := p.runtime.tasks.Complete(id, p.name)
	if err != nil {
		return "", err
	}
	result := fmt.Sprintf("Completed %s (%s)", completed.ID, completed.Subject)
	if len(unblocked) > 0 {
		subjects := make([]string, len(unblocked))
		for i := range unblocked {
			subjects[i] = unblocked[i].Subject
		}
		result += "\nUnblocked: " + strings.Join(subjects, ", ")
	}
	return result, nil
}

func toolTaskID(input any) (string, error) {
	args, err := toolObject(input)
	if err != nil {
		return "", err
	}
	return toolString(args, "task_id")
}

func toolObject(input any) (map[string]any, error) {
	args, ok := input.(map[string]any)
	if !ok {
		return nil, errors.New("tool input must be an object")
	}
	return args, nil
}

func toolString(args map[string]any, key string) (string, error) {
	value, _ := args[key].(string)
	value = strings.TrimSpace(value)
	if value == "" {
		return "", fmt.Errorf("%s is required", key)
	}
	return value, nil
}
