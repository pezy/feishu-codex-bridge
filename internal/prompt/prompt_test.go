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
