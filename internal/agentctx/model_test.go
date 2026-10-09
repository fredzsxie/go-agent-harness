package agentctx

import "testing"

func TestExtractJSONArrayReturnsFirstValidArray(t *testing.T) {
	got := extractJSONArray(`prefix [invalid] text [{"name":"memory"}] suffix [1,2]`)
	if got != `[{"name":"memory"}]` {
		t.Fatalf("extractJSONArray() = %q", got)
	}
}
