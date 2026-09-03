package logger

import (
	"bytes"
	"regexp"
	"strings"
	"testing"
)

func TestLoggerIncludesDateTimeLevelAndMessage(t *testing.T) {
	var output bytes.Buffer
	SetOutput(&output)
	SetMode(ModeDebug)
	t.Cleanup(func() { SetMode(ModeInfo) })
	Debug("[test] details")
	Info("[test] hello")
	Warn("[test] warning")
	Error("[test] failed: %s", "boom")
	pattern := `^\d{4}/\d{2}/\d{2} \d{2}:\d{2}:\d{2} \[DEBUG\] \[test\] details\n` +
		`\d{4}/\d{2}/\d{2} \d{2}:\d{2}:\d{2} \[INFO\] \[test\] hello\n` +
		`\d{4}/\d{2}/\d{2} \d{2}:\d{2}:\d{2} \[WARN\] \[test\] warning\n` +
		`\d{4}/\d{2}/\d{2} \d{2}:\d{2}:\d{2} \[ERROR\] \[test\] failed: boom\n$`
	if !regexp.MustCompile(pattern).MatchString(output.String()) {
		t.Fatalf("unexpected log output: %q", output.String())
	}
}

func TestModePrintsSelectedLevelAndAbove(t *testing.T) {
	tests := []struct {
		name    string
		mode    Mode
		want    []string
		notWant []string
	}{
		{name: "debug", mode: ModeDebug, want: []string{"[DEBUG]", "[INFO]", "[WARN]", "[ERROR]"}},
		{name: "info", mode: ModeInfo, want: []string{"[INFO]", "[WARN]", "[ERROR]"}, notWant: []string{"[DEBUG]"}},
		{name: "warn", mode: ModeWarn, want: []string{"[WARN]", "[ERROR]"}, notWant: []string{"[DEBUG]", "[INFO]"}},
		{name: "error", mode: ModeError, want: []string{"[ERROR]"}, notWant: []string{"[DEBUG]", "[INFO]", "[WARN]"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var output bytes.Buffer
			SetOutput(&output)
			SetMode(test.mode)
			Debug("debug")
			Info("info")
			Warn("warn")
			Error("error")
			for _, value := range test.want {
				if !strings.Contains(output.String(), value) {
					t.Fatalf("expected %s in %q", value, output.String())
				}
			}
			for _, value := range test.notWant {
				if strings.Contains(output.String(), value) {
					t.Fatalf("did not expect %s in %q", value, output.String())
				}
			}
		})
	}
	SetMode(ModeInfo)
}

func TestParseMode(t *testing.T) {
	for value, want := range map[string]Mode{"": ModeInfo, "debug": ModeDebug, "INFO": ModeInfo, " warn ": ModeWarn, "error": ModeError} {
		got, err := ParseMode(value)
		if err != nil || got != want {
			t.Fatalf("ParseMode(%q) = %v, %v; want %v", value, got, err, want)
		}
	}
	if _, err := ParseMode("trace"); err == nil {
		t.Fatal("invalid Mode should return an error")
	}
}
