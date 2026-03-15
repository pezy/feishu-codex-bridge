package prompt

import (
	"fmt"
	"strings"
	"time"

	"github.com/pezy/feishu-codex-bridge/internal/store"
)

const maxHistoryEntries = 12
const maxEntryChars = 600

func Build(workDir string, history []store.ConversationEntry, userText string) string {
	var builder strings.Builder
	workDir = sanitize(workDir)
	userText = sanitize(userText)

	builder.WriteString("You are Codex replying through a Feishu bridge on macOS.\n")
	builder.WriteString("Rules:\n")
	builder.WriteString("- Reply in Chinese unless the user explicitly asks for another language.\n")
	builder.WriteString("- Keep the answer concise, direct, and useful.\n")
	builder.WriteString("- Assume the working directory is ")
	builder.WriteString(workDir)
	builder.WriteString(".\n")
	builder.WriteString("- Return plain message text only unless you need to send images.\n")
	builder.WriteString("- To send an image, put one marker per line using the exact format [[image:/absolute/path/to/file.png]].\n")
	builder.WriteString("- Do not mention internal bridge implementation, background services, secrets, or tokens.\n")
	builder.WriteString("\nRecent conversation:\n")

	if len(history) == 0 {
		builder.WriteString("(none)\n")
	} else {
		start := 0
		if len(history) > maxHistoryEntries {
			start = len(history) - maxHistoryEntries
		}
		for _, entry := range history[start:] {
			builder.WriteString(formatEntry(normalizeEntry(entry)))
			builder.WriteByte('\n')
		}
	}

	builder.WriteString("\nCurrent user message:\n")
	builder.WriteString(userText)
	builder.WriteString("\n")
	builder.WriteString("\nWrite the final reply now.")

	return strings.ToValidUTF8(builder.String(), "�")
}

func normalizeEntry(entry store.ConversationEntry) store.ConversationEntry {
	if entry.ContentType == "image" {
		entry.FilePath = sanitize(entry.FilePath)
		return entry
	}

	content := sanitize(entry.Content)
	if strings.HasPrefix(content, "Codex 执行失败：") {
		entry.Content = "上一轮执行失败（详细日志已省略）。"
		return entry
	}
	if len(content) > maxEntryChars {
		content = content[:maxEntryChars] + " …[truncated]"
	}
	entry.Content = content
	return entry
}

func formatEntry(entry store.ConversationEntry) string {
	ts := entry.CreatedAt.Format(time.RFC3339)
	if entry.ContentType == "image" {
		return fmt.Sprintf("[%s] %s: [image] %s", ts, entry.Source, entry.FilePath)
	}
	return fmt.Sprintf("[%s] %s: %s", ts, entry.Source, entry.Content)
}

func sanitize(input string) string {
	input = strings.ToValidUTF8(input, "�")
	input = strings.ReplaceAll(input, "\r\n", "\n")
	input = strings.ReplaceAll(input, "\n", " ")
	return strings.TrimSpace(input)
}
