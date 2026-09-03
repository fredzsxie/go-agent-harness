package team

import (
	"fmt"
	"strings"

	"go-agent-harness/internal/agent"
	"go-agent-harness/internal/logger"
	"go-agent-harness/internal/protocol"
)

type loopState uint8

const (
	loopWork loopState = iota
	loopIdle
	loopStop
)

// run 在独立 goroutine 中保持 WORK/IDLE 循环，直到完成 shutdown 握手或 Runtime 关闭。
func (p *teammate) run() {
	defer p.runtime.wg.Done()
	defer p.runtime.cleanup(p)
	state := loopWork
	for state != loopStop {
		if state == loopIdle {
			if !p.waitForWork() {
				break
			}
		}
		state = p.work()
	}
}

// work 运行连续模型轮次；没有 tool_use 时本次工作回到 IDLE，而非结束 Teammate。
func (p *teammate) work() loopState {
	if inbox, err := p.runtime.bus.ReadInbox(p.name); err != nil {
		p.reportError(err)
		return loopStop
	} else if stop, _ := p.handleInbox(inbox); stop {
		return loopStop
	}
	p.runtime.setStatus(p, TeammateWorking)

	turn, err := p.worker.RunTurn(p.runtime.ctx, p.runtime.systemPrompt(p), p.messages, nil)
	if err != nil {
		if p.runtime.ctx.Err() == nil {
			p.reportError(err)
		}
		return loopStop
	}
	p.messages = append(p.messages, turn.Assistant)
	if turn.HasTools {
		if len(turn.Tools.Results) == 0 {
			p.reportError(fmt.Errorf("model requested tool_use without tool blocks"))
			return loopStop
		}
		p.messages = append(p.messages, protocol.Message{Role: protocol.RoleUser, Blocks: turn.Tools.Results})
		return loopWork
	}

	summary := strings.TrimSpace(agent.LatestAssistantText(p.messages))
	p.runtime.mu.Lock()
	gate := p.gate
	p.runtime.mu.Unlock()
	if gate != PlanPending && summary != "" {
		if _, err := p.runtime.bus.Send(p.name, "lead", summary, MessageResult, Metadata{}); err != nil {
			logger.Error("[TeamRuntime] Report result from %s: %v", p.name, err)
		}
	}
	if gate == PlanPending {
		p.runtime.setStatus(p, TeammateWaitingApproval)
		return loopIdle
	}

	// Task 完成后保留 Workspace 到本轮模型响应结束，随后才释放租约。
	p.runtime.releaseCompleted(p)
	p.runtime.setStatus(p, TeammateIdle)
	if _, err := p.runtime.bus.Send(p.name, "lead", "Waiting for more work.", MessageIdleNotification, Metadata{}); err != nil {
		logger.Error("[TeamRuntime] Report idle from %s: %v", p.name, err)
		return loopStop
	}
	return loopIdle
}

// waitForWork 优先处理邮箱；只有等待超时后才扫描并原子认领 ready Task。
func (p *teammate) waitForWork() bool {
	for {
		messages, err := p.runtime.bus.Wait(p.runtime.ctx, p.name, p.runtime.idleInterval)
		if err != nil {
			if p.runtime.ctx.Err() == nil {
				p.reportError(err)
			}
			return false
		}
		if len(messages) > 0 {
			stop, work := p.handleInbox(messages)
			if stop {
				return false
			}
			if work {
				return true
			}
			continue
		}

		claimed, err := p.runtime.claimNext(p)
		if err != nil {
			p.reportError(err)
			return false
		}
		if claimed == nil {
			continue
		}
		resolver, err := p.runtime.currentResolver(p)
		if err != nil {
			p.reportError(err)
			return false
		}
		p.messages = append(p.messages, protocol.Message{
			Role: protocol.RoleUser,
			Content: fmt.Sprintf("[Auto-claimed task %s] %s\n%s\nWork directory: %s",
				claimed.ID, claimed.Subject, claimed.Description, resolver.Root()),
		})
		logger.Info("[TeamRuntime] %s auto-claimed %s", p.name, claimed.ID)
		return true
	}
}

// handleInbox 校验控制消息，并把普通协作内容合并为一个新的 user 回合。
func (p *teammate) handleInbox(messages []Message) (stop, work bool) {
	if len(messages) == 0 {
		return false, false
	}
	contents := make([]string, 0, len(messages))
	for _, message := range messages {
		switch message.Type {
		case MessageShutdownRequest:
			if p.acceptShutdown(message) {
				return true, false
			}
			contents = append(contents, "[Ignored shutdown request: request mismatch]")
		case MessagePlanApprovalResponse:
			if notice, ok := p.applyPlanResponse(message); ok {
				contents = append(contents, notice)
			} else {
				contents = append(contents, "[Ignored plan response: request mismatch]")
			}
		case MessagePlanRequest:
			p.runtime.mu.Lock()
			p.gate = PlanRequired
			p.runtime.mu.Unlock()
			contents = append(contents, "[Plan required] "+message.Content)
		default:
			contents = append(contents, fmt.Sprintf("[Message from %s] %s", message.From, message.Content))
		}
	}
	if len(contents) > 0 {
		p.messages = append(p.messages, protocol.Message{Role: protocol.RoleUser, Content: strings.Join(contents, "\n")})
		return false, true
	}
	return false, false
}

func (p *teammate) acceptShutdown(message Message) bool {
	request, ok := p.runtime.requests.Get(message.Metadata.RequestID)
	valid := ok && message.From == "lead" && message.To == p.name &&
		request.Type == RequestShutdown && request.Sender == "lead" && request.Target == p.name &&
		request.Status == RequestPending
	if !valid {
		logger.Warn("[TeamRuntime] %s ignored mismatched shutdown request", p.name)
		return false
	}
	p.runtime.setStatus(p, TeammateStopping)
	approve := true
	_, err := p.runtime.bus.Send(p.name, "lead", "Shutdown acknowledged.", MessageShutdownResponse, Metadata{
		RequestID: request.ID, Approve: &approve,
	})
	if err != nil {
		logger.Error("[TeamRuntime] %s shutdown acknowledgement: %v", p.name, err)
	}
	return true
}

func (p *teammate) applyPlanResponse(message Message) (string, bool) {
	request, ok := p.runtime.requests.Get(message.Metadata.RequestID)
	p.runtime.mu.Lock()
	defer p.runtime.mu.Unlock()
	identity := p.runtime.identityLocked(p)
	valid := ok && message.From == "lead" && message.To == p.name &&
		p.planRequestID == message.Metadata.RequestID && request.Type == RequestPlanApproval &&
		request.Sender == p.name && request.Target == "lead" && sameIdentity(request.WorkIdentity, identity) &&
		(request.Status == RequestApproved || request.Status == RequestRejected) && message.Metadata.Approve != nil &&
		(*message.Metadata.Approve == (request.Status == RequestApproved))
	if !valid {
		logger.Warn("[TeamRuntime] %s ignored mismatched plan response", p.name)
		return "", false
	}
	if request.Status == RequestApproved {
		p.gate = PlanApproved
	} else {
		p.gate = PlanRejected
	}
	p.status = TeammateWorking
	p.planRequestID = ""
	return fmt.Sprintf("[Plan %s] %s", request.Status, message.Content), true
}

func (p *teammate) reportError(err error) {
	logger.Error("[TeamRuntime] %s: %v", p.name, err)
	if _, sendErr := p.runtime.bus.Send(p.name, "lead", err.Error(), MessageError, Metadata{}); sendErr != nil {
		logger.Error("[TeamRuntime] Report error from %s: %v", p.name, sendErr)
	}
}
