package workflow

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
)

var findingsSchema = map[string]any{
	"type": "object", "required": []string{"findings"},
	"properties": map[string]any{
		"findings": map[string]any{"type": "array", "items": map[string]any{
			"type": "object", "required": []string{"title", "severity"},
			"properties": map[string]any{
				"title":    map[string]any{"type": "string"},
				"severity": map[string]any{"type": "string", "enum": []string{"high", "medium", "low"}},
			},
		}},
	},
}

var verdictSchema = map[string]any{
	"type": "object", "required": []string{"isReal", "reason"},
	"properties": map[string]any{
		"isReal": map[string]any{"type": "boolean"},
		"reason": map[string]any{"type": "string"},
	},
}

var reviewDimensions = []any{"correctness", "security", "performance", "style"}

func RegisterDefaults(registry *Registry) error {
	if registry == nil {
		return fmt.Errorf("workflow registry is required")
	}
	return registry.Register(Definition{
		Metadata: Metadata{
			Name: "review-changes", Description: "Review changed code across dimensions and verify each finding",
			Phases: []string{"Review", "Verify"},
		},
		Script: reviewChanges,
	})
}

// reviewChanges 将固定的 audit → verify 编排保存在 Host 代码中，而不是会话历史中。
func reviewChanges(ctx context.Context, execution ExecutionContext, args map[string]any) (any, error) {
	changes, ok := args["changes"].(string)
	if !ok {
		return nil, fmt.Errorf("review-changes requires string args.changes")
	}
	execution.Phase("Review")
	results, err := execution.Pipeline(ctx, reviewDimensions,
		func(ctx context.Context, _ any, item any, _ int) (any, error) {
			dimension := item.(string)
			result, err := execution.Agent(ctx,
				fmt.Sprintf("Review this change context for %s issues. Report only issues supported by the supplied text.\n\n%s", dimension, changes),
				AgentOptions{Schema: findingsSchema, Label: "audit:" + dimension, Phase: "Review"},
			)
			if err != nil {
				return nil, err
			}
			return map[string]any{"dimension": dimension, "findings": result.(map[string]any)["findings"]}, nil
		},
		func(ctx context.Context, value any, item any, _ int) (any, error) {
			execution.Phase("Verify")
			dimension := item.(string)
			audited := value.(map[string]any)
			findings := audited["findings"].([]any)
			steps := make([]Step, len(findings))
			for index, finding := range findings {
				finding := finding
				steps[index] = func(ctx context.Context) (any, error) {
					rawFinding, err := json.Marshal(finding)
					if err != nil {
						return nil, err
					}
					title, _ := finding.(map[string]any)["title"].(string)
					return execution.Agent(ctx,
						fmt.Sprintf("Adversarially verify this %s finding against the supplied change context.\n\nChange context:\n%s\n\nFinding:\n%s", dimension, changes, rawFinding),
						AgentOptions{Schema: verdictSchema, Label: "verify:" + dimension + ":" + title, Phase: "Verify"},
					)
				}
			}
			verdicts, err := execution.Parallel(ctx, steps)
			if err != nil {
				return nil, err
			}
			confirmed := make([]any, 0, len(findings))
			for index, verdict := range verdicts {
				if verdict.(map[string]any)["isReal"] == true {
					confirmed = append(confirmed, findings[index])
				}
			}
			return map[string]any{"dimension": dimension, "confirmed": confirmed}, nil
		},
	)
	if err != nil {
		return nil, err
	}

	confirmed := make([]map[string]any, 0)
	for _, result := range results {
		item := result.(map[string]any)
		for _, rawFinding := range item["confirmed"].([]any) {
			finding := rawFinding.(map[string]any)
			confirmed = append(confirmed, map[string]any{
				"dimension": item["dimension"], "title": finding["title"], "severity": finding["severity"],
			})
		}
	}
	severityOrder := map[string]int{"high": 0, "medium": 1, "low": 2}
	sort.SliceStable(confirmed, func(i, j int) bool {
		return severityOrder[confirmed[i]["severity"].(string)] < severityOrder[confirmed[j]["severity"].(string)]
	})
	execution.Log(fmt.Sprintf("confirmed %d real finding(s)", len(confirmed)))
	return map[string]any{"confirmed": confirmed}, nil
}
