//go:build smoke

package logs

import (
	"encoding/json"
	"fmt"
	"testing"
)

// Run: go test -tags=smoke -v ./internal/logs/... -run TestRealLog
func TestRealLog(t *testing.T) {
	s, err := LoadSession("/Users/ayusman/.freeman/logs/2026-04-19/call-113823-f71745.log")
	if err != nil {
		t.Fatal(err)
	}
	b, _ := json.MarshalIndent(s, "", "  ")
	fmt.Println(string(b))
	t.Logf("turns=%d tools=%d wakes=%d", s.TurnCount, s.ToolCallCount, len(s.WakeEvents))
}
