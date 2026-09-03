package team

import (
	"context"
	"fmt"
	"strings"
	"time"
)

// WaitLeadEvents 由 App 作为 Lead 邮箱的唯一消费者，并先推进控制协议状态。
func (r *Runtime) WaitLeadEvents(ctx context.Context, timeout time.Duration) ([]Message, error) {
	messages, err := r.bus.Wait(ctx, "lead", timeout)
	if err != nil {
		return nil, err
	}
	for _, message := range messages {
		if message.Type == MessageShutdownResponse {
			_, _ = r.requests.Match(message)
		}
	}
	return messages, nil
}

func (r *Runtime) RunSpawn(_ context.Context, input any) (string, error) {
	args, err := toolObject(input)
	if err != nil {
		return "", err
	}
	name, err := toolString(args, "name")
	if err != nil {
		return "", err
	}
	role, err := toolString(args, "role")
	if err != nil {
		return "", err
	}
	prompt, err := toolString(args, "prompt")
	if err != nil {
		return "", err
	}
	taskID, _ := args["task_id"].(string)
	requirePlan, _ := args["require_plan"].(bool)
	if err := r.Spawn(name, role, prompt, strings.TrimSpace(taskID), requirePlan); err != nil {
		return "", err
	}
	assigned := " without an initial Task"
	if strings.TrimSpace(taskID) != "" {
		assigned = " for " + strings.TrimSpace(taskID)
	}
	return fmt.Sprintf("Teammate %q spawned as %s%s. End this turn; the runtime will deliver its events.", name, role, assigned), nil
}

func (r *Runtime) RunList(_ context.Context, _ any) (string, error) {
	items := r.List()
	if len(items) == 0 {
		return "No active teammates.", nil
	}
	lines := make([]string, len(items))
	for i, item := range items {
		lines[i] = fmt.Sprintf("%s: %s", item.Name, item.Status)
		if item.TaskID != "" {
			lines[i] += " [" + item.TaskID + "]"
		}
		if item.Plan != PlanNotRequired {
			lines[i] += " (plan: " + string(item.Plan) + ")"
		}
	}
	return strings.Join(lines, "\n"), nil
}

func (r *Runtime) RunSend(_ context.Context, input any) (string, error) {
	args, err := toolObject(input)
	if err != nil {
		return "", err
	}
	to, err := toolString(args, "to")
	if err != nil {
		return "", err
	}
	content, err := toolString(args, "content")
	if err != nil {
		return "", err
	}
	if err := r.Send(to, content); err != nil {
		return "", err
	}
	return "Sent to " + to, nil
}

func (r *Runtime) RunRequestShutdown(_ context.Context, input any) (string, error) {
	args, err := toolObject(input)
	if err != nil {
		return "", err
	}
	name, err := toolString(args, "teammate")
	if err != nil {
		return "", err
	}
	requestID, err := r.RequestShutdown(name)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("Shutdown requested from %s (%s)", name, requestID), nil
}

func (r *Runtime) RunRequestPlan(_ context.Context, input any) (string, error) {
	args, err := toolObject(input)
	if err != nil {
		return "", err
	}
	name, err := toolString(args, "teammate")
	if err != nil {
		return "", err
	}
	instruction, err := toolString(args, "task")
	if err != nil {
		return "", err
	}
	if err := r.RequestPlan(name, instruction); err != nil {
		return "", err
	}
	return "Plan requested from " + name, nil
}

func (r *Runtime) RunReviewPlan(_ context.Context, input any) (string, error) {
	args, err := toolObject(input)
	if err != nil {
		return "", err
	}
	requestID, err := toolString(args, "request_id")
	if err != nil {
		return "", err
	}
	approve, ok := args["approve"].(bool)
	if !ok {
		return "", fmt.Errorf("approve is required")
	}
	feedback, _ := args["feedback"].(string)
	if err := r.ReviewPlan(requestID, approve, feedback); err != nil {
		return "", err
	}
	status := "rejected"
	if approve {
		status = "approved"
	}
	return fmt.Sprintf("Plan %s (%s)", status, requestID), nil
}
