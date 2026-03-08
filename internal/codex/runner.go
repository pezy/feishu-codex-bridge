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
		return result, fmt.Errorf("run codex exec: %w", err)
	}

	if result.Output == "" {
		result.Output = "任务已执行，但没有生成可发送的最终文本。请查看本机日志。"
	}

	return result, nil
}

func fallbackErrorMessage(rawLogs string, err error) string {
	if rawLogs != "" {
		return trimForFeishu(rawLogs)
	}
	return trimForFeishu(err.Error())
}

func trimForFeishu(input string) string {
	input = strings.TrimSpace(input)
	if len(input) <= 1800 {
		return input
	}
	return input[:1800] + "\n\n[truncated]"
}
