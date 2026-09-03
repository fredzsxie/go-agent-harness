package team

// PlanStatus 表示 Teammate 当前任务的执行审批状态。
type PlanStatus string

const (
	PlanNotRequired PlanStatus = "not_required"
	PlanRequired    PlanStatus = "required"
	PlanPending     PlanStatus = "pending"
	PlanApproved    PlanStatus = "approved"
	PlanRejected    PlanStatus = "rejected"
)

func (s PlanStatus) blocksMutation() bool {
	return s != PlanNotRequired && s != PlanApproved
}
