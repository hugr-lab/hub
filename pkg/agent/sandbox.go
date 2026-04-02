package agent

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

const (
	defaultTimeout = 30 * time.Second
	maxOutputSize  = 100 * 1024 // 100KB
)

// allowedPaths restricts file operations to these directories.
var allowedPaths = []string{"/shared", "/.agent"}

// Sandbox provides sandboxed Python and bash execution + file tools.
type Sandbox struct {
	logger *slog.Logger
}

func NewSandbox(logger *slog.Logger) *Sandbox {
	return &Sandbox{logger: logger}
}

// PythonExec runs Python code in a subprocess with timeout.
func (s *Sandbox) PythonExec(ctx context.Context, code string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, defaultTimeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, "python3", "-c", code)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()
	output := stdout.String()
	if stderr.Len() > 0 {
		output += "\nSTDERR:\n" + stderr.String()
	}

	if len(output) > maxOutputSize {
		output = output[:maxOutputSize] + "\n... (truncated)"
	}

	if err != nil {
		return output, fmt.Errorf("python error: %w\n%s", err, output)
	}

	s.logger.Info("python exec", "code_len", len(code), "output_len", len(output))
	return output, nil
}

// BashExec runs a bash command in a subprocess with timeout.
func (s *Sandbox) BashExec(ctx context.Context, command string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, defaultTimeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, "bash", "-c", command)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()
	output := stdout.String()
	if stderr.Len() > 0 {
		output += "\nSTDERR:\n" + stderr.String()
	}

	if len(output) > maxOutputSize {
		output = output[:maxOutputSize] + "\n... (truncated)"
	}

	if err != nil {
		return output, fmt.Errorf("bash error: %w\n%s", err, output)
	}

	s.logger.Info("bash exec", "cmd_len", len(command), "output_len", len(output))
	return output, nil
}

// FileRead reads a file from allowed paths.
func (s *Sandbox) FileRead(path string) (string, error) {
	if err := checkAllowedPath(path); err != nil {
		return "", err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("read file: %w", err)
	}
	if len(data) > maxOutputSize {
		return string(data[:maxOutputSize]) + "\n... (truncated)", nil
	}
	return string(data), nil
}

// FileWrite writes content to a file in allowed paths.
func (s *Sandbox) FileWrite(path, content string) error {
	if err := checkAllowedPath(path); err != nil {
		return err
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("create directory: %w", err)
	}
	return os.WriteFile(path, []byte(content), 0644)
}

// FileList lists directory contents in allowed paths.
func (s *Sandbox) FileList(path string) ([]string, error) {
	if err := checkAllowedPath(path); err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(path)
	if err != nil {
		return nil, fmt.Errorf("list directory: %w", err)
	}
	var names []string
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() {
			name += "/"
		}
		names = append(names, name)
	}
	return names, nil
}

func checkAllowedPath(path string) error {
	abs, err := filepath.Abs(path)
	if err != nil {
		return fmt.Errorf("invalid path: %w", err)
	}
	for _, allowed := range allowedPaths {
		if strings.HasPrefix(abs, allowed) {
			return nil
		}
	}
	return fmt.Errorf("path %q not allowed (restricted to %v)", path, allowedPaths)
}
