package memory

import (
	"context"
	"fmt"
	"strings"
)

// s09 Consolidation：先完整校验新集合，再替换旧记录；失败时通过快照恢复。
// 判断文件数是否超过阈值。若超过阈值，调用 LLM side query 进行合并
func (m *Manager) Consolidate(ctx context.Context, consolidate Consolidator) (int, int, error) {
	records, err := m.List()
	if err != nil {
		return 0, 0, err
	}
	if len(records) < m.cfg.ConsolidateThreshold {
		return len(records), len(records), nil
	}
	next, err := consolidate(ctx, records)
	if err != nil {
		return len(records), len(records), err
	}
	if len(next) == 0 {
		return len(records), len(records), fmt.Errorf("consolidation returned no valid records")
	}
	if len(next) > MaxConsolidatedRecords {
		return len(records), len(records), fmt.Errorf("consolidation returned %d records; max is %d", len(next), MaxConsolidatedRecords)
	}
	seenSlugs := make(map[string]bool, len(next))
	for _, record := range next {
		if strings.TrimSpace(record.Name) == "" || strings.TrimSpace(record.Description) == "" || strings.TrimSpace(record.Body) == "" || !isValidType(record.Type) {
			return len(records), len(records), fmt.Errorf("consolidation returned an invalid record")
		}
		slug := slugify(record.Name)
		if seenSlugs[slug] {
			return len(records), len(records), fmt.Errorf("consolidation returned duplicate memory %q", slug)
		}
		seenSlugs[slug] = true
	}

	snapshot, err := m.snapshotRecords(records)
	if err != nil {
		return len(records), len(records), err
	}

	if err := m.removeRecords(records); err != nil {
		if restoreErr := m.restoreSnapshot(snapshot); restoreErr != nil {
			return len(records), 0, fmt.Errorf("remove old memories: %v; restore snapshot: %w", err, restoreErr)
		}
		return len(records), len(records), err
	}
	count := 0
	for _, record := range next {
		if _, err := m.Write(record); err != nil {
			if restoreErr := m.restoreSnapshot(snapshot); restoreErr != nil {
				return len(records), count, fmt.Errorf("write consolidated memories: %v; restore snapshot: %w", err, restoreErr)
			}
			return len(records), len(records), err
		}
		count++
	}
	if err := m.RebuildIndex(); err != nil {
		if restoreErr := m.restoreSnapshot(snapshot); restoreErr != nil {
			return len(records), count, fmt.Errorf("rebuild consolidated index: %v; restore snapshot: %w", err, restoreErr)
		}
		return len(records), len(records), err
	}
	return len(records), count, nil
}
