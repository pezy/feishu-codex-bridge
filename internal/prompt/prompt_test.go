package prompt

import (
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/pezy/feishu-codex-bridge/internal/store"
)

func TestBuildIncludesHistoryAndUserText(t *testing.T) {
	history := []store.ConversationEntry{
		{
			Source:    "user",
			OpenID:    "ou_123",
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
			OpenID:      "ou_123",
			Content:     "[image]",
			ContentType: "image",
			FilePath:    "/tmp/input.png",
			CreatedAt:   time.Unix(1700000600, 0).UTC(),
		},
	}

	output := Build("/tmp/work", history, "please help")

	for _, expected := range []string{
		"/tmp/work",
		"user(ou_123): hello world",
		"assistant: done",
		"user(ou_123): [image] /tmp/input.png",
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

func TestBuildSanitizesInvalidUTF8(t *testing.T) {
	history := []store.ConversationEntry{
		{
			Source:    "user",
			Content:   "bad" + string([]byte{0xff}) + "history",
			CreatedAt: time.Unix(1700000000, 0).UTC(),
		},
	}

	output := Build("/tmp/"+string([]byte{0xff})+"work", history, "hi"+string([]byte{0xff}))
	if strings.Contains(output, string([]byte{0xff})) {
		t.Fatalf("output contains invalid utf8 byte: %q", output)
	}
	if !utf8.ValidString(output) {
		t.Fatalf("output should be valid utf8: %q", output)
	}
}
