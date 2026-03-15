package prompt

import (
	"strings"
	"testing"
	"time"

	"github.com/pezy/feishu-codex-bridge/internal/store"
)

func TestBuildIncludesHistoryAndUserText(t *testing.T) {
	history := []store.ConversationEntry{
		{
			Source:    "user",
			Content:   "hello\nworld",
			CreatedAt: time.Unix(1700000000, 0).UTC(),
		},
		{
			Source:    "assistant",
			Content:   "done",
			CreatedAt: time.Unix(1700000300, 0).UTC(),
		},
		{
			Source:      "user",
			Content:     "[image]",
			ContentType: "image",
			FilePath:    "/tmp/input.png",
			CreatedAt:   time.Unix(1700000600, 0).UTC(),
		},
	}

	output := Build("/tmp/work", history, "please help")

	for _, expected := range []string{
		"/tmp/work",
		"user: hello world",
		"assistant: done",
		"user: [image] /tmp/input.png",
		"Current user message:\nplease help",
	} {
		if !strings.Contains(output, expected) {
			t.Fatalf("output missing %q\n%s", expected, output)
		}
	}
}

func TestBuildCondensesFailureLogsInHistory(t *testing.T) {
	history := []store.ConversationEntry{
		{
			Source:    "assistant",
			Content:   "Codex 执行失败：\nOpenAI Codex v0.98.0\nsession id: abc\nError: boom",
			CreatedAt: time.Unix(1700000000, 0).UTC(),
		},
	}

	output := Build("/tmp/work", history, "继续")
	if !strings.Contains(output, "上一轮执行失败（详细日志已省略）。") {
		t.Fatalf("output missing condensed failure text:\n%s", output)
	}
	if strings.Contains(output, "OpenAI Codex v0.98.0") || strings.Contains(output, "session id: abc") {
		t.Fatalf("output should not include raw failure logs:\n%s", output)
	}
}
