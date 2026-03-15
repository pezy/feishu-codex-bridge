package codex

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

const (
	maxFailureMessageLen = 1800
	maxRunAttempts       = 2
)

type Runner struct {
	codexPath string
	workDir   string
	timeout   time.Duration
}

type Result struct {
	Output   string
	RawLogs  string
	ExitCode int
}

func NewRunner(codexPath string, workDir string, timeout time.Duration) *Runner {
	return &Runner{
		codexPath: codexPath,
		workDir:   workDir,
		timeout:   timeout,
	}
}

func (r *Runner) Run(ctx context.Context, prompt string) (Result, error) {
	var lastResult Result
	var lastErr error

	for attempt := 1; attempt <= maxRunAttempts; attempt++ {
		result, err := r.runOnce(ctx, prompt)
		if err == nil {
			if result.Output == "" {
				result.Output = "任务已执行，但没有生成可发送的最终文本。请查看本机日志。"
			}
			return result, nil
		}

		lastResult = result
		lastErr = err
		if !shouldRetry(result, err) || attempt == maxRunAttempts {
			break
		}
		time.Sleep(time.Duration(attempt) * time.Second)
	}

	if lastResult.Output == "" {
		lastResult.Output = fallbackErrorMessage(lastResult.RawLogs, lastErr)
	}
	return lastResult, fmt.Errorf("run codex exec: %w", lastErr)
}

func fallbackErrorMessage(rawLogs string, err error) string {
	if summary := summarizeRawLogs(rawLogs); summary != "" {
		return trimForFeishu(summary)
	}
	return trimForFeishu(err.Error())
}

func trimForFeishu(input string) string {
	input = strings.TrimSpace(input)
	if len(input) <= maxFailureMessageLen {
		return input
	}
	return input[:maxFailureMessageLen] + "\n\n[truncated]"
}

func (r *Runner) runOnce(ctx context.Context, prompt string) (Result, error) {
	runCtx, cancel := context.WithTimeout(ctx, r.timeout)
	defer cancel()

	tempDir, err := os.MkdirTemp("", "feishu-codex-bridge-*")
	if err != nil {
		return Result{}, fmt.Errorf("create temp dir: %w", err)
	}
	defer os.RemoveAll(tempDir)

	outputPath := filepath.Join(tempDir, "codex-last-message.txt")
	args := []string{
		"-a", "never",
		"exec",
		"-C", r.workDir,
		"--skip-git-repo-check",
		"-s", "danger-full-access",
		"--output-last-message", outputPath,
		"-",
	}

	cmd := exec.CommandContext(runCtx, r.codexPath, args...)
	cmd.Stdin = strings.NewReader(prompt)
	cmd.Dir = r.workDir
	combinedOutput, err := cmd.CombinedOutput()

	result := Result{
		RawLogs: strings.TrimSpace(string(combinedOutput)),
	}

	if output, readErr := os.ReadFile(outputPath); readErr == nil {
		result.Output = strings.TrimSpace(string(output))
	}

	if cmd.ProcessState != nil {
		result.ExitCode = cmd.ProcessState.ExitCode()
	}

	if err != nil {
		if result.Output == "" {
			result.Output = fallbackErrorMessage(result.RawLogs, err)
		}
		return result, err
	}

	return result, nil
}

func shouldRetry(result Result, err error) bool {
	if err == nil {
		return false
	}
	if result.RawLogs != "" && !looksLikeCodexBanner(result.RawLogs) {
		return false
	}
	return true
}

func summarizeRawLogs(rawLogs string) string {
	rawLogs = strings.TrimSpace(rawLogs)
	if rawLogs == "" {
		return ""
	}
	lines := strings.Split(rawLogs, "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		line := strings.TrimSpace(lines[i])
		if line == "" || looksLikeCodexBanner(line) || isCodexMetadataLine(line) {
			continue
		}
		if strings.HasPrefix(line, "user") || strings.HasPrefix(line, "assistant") || strings.HasPrefix(line, "thinking") || strings.HasPrefix(line, "exec") || strings.HasPrefix(line, "tokens used") {
			continue
		}
		return "Codex 执行失败，请稍后重试。\n原因：" + line
	}
	return "Codex 执行失败，请稍后重试。"
}

func looksLikeCodexBanner(input string) bool {
	input = strings.TrimSpace(input)
	return strings.HasPrefix(input, "OpenAI Codex v") || strings.HasPrefix(input, "Codex 执行失败：\nOpenAI Codex v")
}

func isCodexMetadataLine(line string) bool {
	if strings.HasPrefix(line, "--------") {
		return true
	}
	for _, prefix := range []string{
		"workdir:",
		"model:",
		"provider:",
		"approval:",
		"sandbox:",
		"reasoning effort:",
		"reasoning summaries:",
		"session id:",
		"mcp:",
		"mcp startup:",
	} {
		if strings.HasPrefix(line, prefix) {
			return true
		}
	}
	return false
}
