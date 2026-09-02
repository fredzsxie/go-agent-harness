package logger

import (
	"bytes"
	"regexp"
	"testing"
)

func TestLoggerIncludesDateTimeLevelAndMessage(t *testing.T) {
	var output bytes.Buffer
	SetOutput(&output)
	Info("[test] hello")
	Error("[test] failed: %s", "boom")
	pattern := `^\d{4}/\d{2}/\d{2} \d{2}:\d{2}:\d{2} \[INFO\] \[test\] hello\n` +
		`\d{4}/\d{2}/\d{2} \d{2}:\d{2}:\d{2} \[ERROR\] \[test\] failed: boom\n$`
	if !regexp.MustCompile(pattern).MatchString(output.String()) {
		t.Fatalf("unexpected log output: %q", output.String())
	}
}
