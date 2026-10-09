package runtime

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// ValidateCron 校验五段 cron 表达式，支持 *、*/N、N、N-M 和逗号列表。
func ValidateCron(expression string) error {
	fields := strings.Fields(expression)
	if len(fields) != 5 {
		return fmt.Errorf("expected 5 fields, got %d", len(fields))
	}
	rules := []struct {
		name     string
		min, max int
	}{
		{"minute", 0, 59},
		{"hour", 0, 23},
		{"day-of-month", 1, 31},
		{"month", 1, 12},
		{"day-of-week", 0, 6},
	}
	for i, field := range fields {
		if err := validateCronField(field, rules[i].min, rules[i].max); err != nil {
			return fmt.Errorf("%s: %w", rules[i].name, err)
		}
	}
	return nil
}

func validateCronField(field string, min, max int) error {
	if field == "*" {
		return nil
	}
	if strings.HasPrefix(field, "*/") {
		step, err := strconv.Atoi(strings.TrimPrefix(field, "*/"))
		if err != nil || step <= 0 {
			return fmt.Errorf("invalid step: %s", field)
		}
		return nil
	}
	if strings.Contains(field, ",") {
		for _, part := range strings.Split(field, ",") {
			if err := validateCronField(strings.TrimSpace(part), min, max); err != nil {
				return err
			}
		}
		return nil
	}
	if strings.Contains(field, "-") {
		parts := strings.SplitN(field, "-", 2)
		start, startErr := strconv.Atoi(parts[0])
		end, endErr := strconv.Atoi(parts[1])
		if startErr != nil || endErr != nil {
			return fmt.Errorf("invalid range: %s", field)
		}
		if start > end {
			return fmt.Errorf("range start is greater than end: %s", field)
		}
		if start < min || end > max {
			return fmt.Errorf("range %s is outside [%d-%d]", field, min, max)
		}
		return nil
	}
	value, err := strconv.Atoi(field)
	if err != nil {
		return fmt.Errorf("invalid field: %s", field)
	}
	if value < min || value > max {
		return fmt.Errorf("value %d is outside [%d-%d]", value, min, max)
	}
	return nil
}

// CronMatches 使用本地时间判断表达式是否命中；日期与星期同时受限时采用 cron 的 OR 语义。
func CronMatches(expression string, moment time.Time) bool {
	if ValidateCron(expression) != nil {
		return false
	}
	fields := strings.Fields(expression)
	if !cronFieldMatches(fields[0], moment.Minute()) ||
		!cronFieldMatches(fields[1], moment.Hour()) ||
		!cronFieldMatches(fields[3], int(moment.Month())) {
		return false
	}
	dayMatches := cronFieldMatches(fields[2], moment.Day())
	weekdayMatches := cronFieldMatches(fields[4], int(moment.Weekday()))
	switch {
	case fields[2] == "*" && fields[4] == "*":
		return true
	case fields[2] == "*":
		return weekdayMatches
	case fields[4] == "*":
		return dayMatches
	default:
		return dayMatches || weekdayMatches
	}
}

func cronFieldMatches(field string, value int) bool {
	if field == "*" {
		return true
	}
	if strings.HasPrefix(field, "*/") {
		step, _ := strconv.Atoi(strings.TrimPrefix(field, "*/"))
		return value%step == 0
	}
	if strings.Contains(field, ",") {
		for _, part := range strings.Split(field, ",") {
			if cronFieldMatches(strings.TrimSpace(part), value) {
				return true
			}
		}
		return false
	}
	if strings.Contains(field, "-") {
		parts := strings.SplitN(field, "-", 2)
		start, _ := strconv.Atoi(parts[0])
		end, _ := strconv.Atoi(parts[1])
		return value >= start && value <= end
	}
	want, _ := strconv.Atoi(field)
	return value == want
}
