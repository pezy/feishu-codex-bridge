package codex

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestRunnerRetriesTransientFailure(t *testing.T) {
	dir := t.TempDir()
	counterFile := filepath.Join(dir, "attempt.txt")
	script := filepath.Join(dir, "fake-codex.sh")

	scriptBody := `#!/bin/sh
count=0
if [ -f "` + counterFile + `" ]; then
  count=$(cat "` + counterFile + `")
fi
count=$((count + 1))
echo "$count" > "` + counterFile + `"
out=""
prev=""
for arg in "$@"; do
  if [ "$prev" = "--output-last-message" ]; then
    out="$arg"
  fi
  prev="$arg"
done
cat >/dev/null
if [ "$count" -eq 1 ]; then
  echo "OpenAI Codex v0.98.0"
  echo "session id: test"
  echo "Error: transient upstream failure"
  exit 1
fi
echo "ok" > "$out"
echo "done"
`
	if err := os.WriteFile(script, []byte(scriptBody), 0o755); err != nil {
		t.Fatalf("WriteFile script: %v", err)
	}

	runner := NewRunner(script, dir, 5*time.Second)
	result, err := runner.Run(context.Background(), "hello")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if strings.TrimSpace(result.Output) != "ok" {
		t.Fatalf("unexpected output: %q", result.Output)
	}
	data, err := os.ReadFile(counterFile)
	if err != nil {
		t.Fatalf("ReadFile counter: %v", err)
	}
	if strings.TrimSpace(string(data)) != "2" {
		t.Fatalf("expected 2 attempts, got %q", string(data))
	}
}

func TestFallbackErrorMessageSummarizesRawLogs(t *testing.T) {
	rawLogs := strings.Join([]string{
		"OpenAI Codex v0.98.0",
		"--------",
		"workdir: /tmp",
		"session id: abc",
		"mcp: linear starting",
		"Error: upstream request failed",
	}, "\n")

	got := fallbackErrorMessage(rawLogs, context.DeadlineExceeded)
	if !strings.Contains(got, "Codex 执行失败，请稍后重试。") {
		t.Fatalf("unexpected summary: %q", got)
	}
	if !strings.Contains(got, "Error: upstream request failed") {
		t.Fatalf("missing error detail: %q", got)
	}
	if strings.Contains(got, "session id:") {
		t.Fatalf("should not leak banner metadata: %q", got)
	}
}
