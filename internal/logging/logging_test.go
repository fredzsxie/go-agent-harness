package logging

import (
	"bytes"
	"regexp"
	"testing"
)

func TestLoggerIncludesDateTimeAndMessage(t *testing.T) {
	var output bytes.Buffer
	SetOutput(&output)
	Printf("[test] hello")
	if !regexp.MustCompile(`^\d{4}/\d{2}/\d{2} \d{2}:\d{2}:\d{2} \[test\] hello\n$`).MatchString(output.String()) {
		t.Fatalf("unexpected log output: %q", output.String())
	}
}
