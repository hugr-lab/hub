// sandbox-mcp is a local MCP server providing sandboxed execution tools.
// It communicates via stdio (stdin/stdout) and is managed by hub-agent.
package main

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

const (
	defaultTimeout = 30 * time.Second
	maxOutputSize  = 100 * 1024 // 100KB
)

var allowedPaths = []string{"/shared", "/.agent"}

func main() {
	s := server.NewMCPServer("sandbox", "0.1.0",
		server.WithToolCapabilities(true),
	)

	s.AddTool(mcp.NewTool("python-exec",
		mcp.WithDescription("Execute Python 3 code in a sandboxed subprocess."),
		mcp.WithString("code", mcp.Required(), mcp.Description("Python code to execute")),
		mcp.WithNumber("timeout", mcp.Description("Timeout in seconds (default 30)")),
	), handlePythonExec)

	s.AddTool(mcp.NewTool("bash-exec",
		mcp.WithDescription("Execute a bash command in a sandboxed subprocess."),
		mcp.WithString("command", mcp.Required(), mcp.Description("Bash command to execute")),
		mcp.WithNumber("timeout", mcp.Description("Timeout in seconds (default 30)")),
	), handleBashExec)

	s.AddTool(mcp.NewTool("file-read",
		mcp.WithDescription("Read a file from allowed paths (/shared, /.agent)."),
		mcp.WithString("path", mcp.Required(), mcp.Description("File path to read")),
	), handleFileRead)

	s.AddTool(mcp.NewTool("file-write",
		mcp.WithDescription("Write content to a file in allowed paths (/shared, /.agent)."),
		mcp.WithString("path", mcp.Required(), mcp.Description("File path to write")),
		mcp.WithString("content", mcp.Required(), mcp.Description("Content to write")),
	), handleFileWrite)

	s.AddTool(mcp.NewTool("file-list",
		mcp.WithDescription("List directory contents in allowed paths (/shared, /.agent)."),
		mcp.WithString("path", mcp.Required(), mcp.Description("Directory path to list")),
	), handleFileList)

	if err := server.ServeStdio(s); err != nil {
		fmt.Fprintf(os.Stderr, "sandbox-mcp error: %v\n", err)
		os.Exit(1)
	}
}

func handlePythonExec(_ context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	args := request.GetArguments()
	code, _ := args["code"].(string)
	if code == "" {
		return mcp.NewToolResultError("code is required"), nil
	}

	timeout := defaultTimeout
	if t, ok := args["timeout"].(float64); ok && t > 0 {
		timeout = time.Duration(t) * time.Second
	}

	output, err := runCommand(timeout, "python3", "-c", code)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("python error: %v\n%s", err, output)), nil
	}
	return mcp.NewToolResultText(output), nil
}

func handleBashExec(_ context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	args := request.GetArguments()
	command, _ := args["command"].(string)
	if command == "" {
		return mcp.NewToolResultError("command is required"), nil
	}

	timeout := defaultTimeout
	if t, ok := args["timeout"].(float64); ok && t > 0 {
		timeout = time.Duration(t) * time.Second
	}

	output, err := runCommand(timeout, "bash", "-c", command)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("bash error: %v\n%s", err, output)), nil
	}
	return mcp.NewToolResultText(output), nil
}

func handleFileRead(_ context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	args := request.GetArguments()
	path, _ := args["path"].(string)
	if path == "" {
		return mcp.NewToolResultError("path is required"), nil
	}
	if err := checkAllowedPath(path); err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("read file: %v", err)), nil
	}
	output := string(data)
	if len(output) > maxOutputSize {
		output = output[:maxOutputSize] + "\n... (truncated)"
	}
	return mcp.NewToolResultText(output), nil
}

func handleFileWrite(_ context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	args := request.GetArguments()
	path, _ := args["path"].(string)
	content, _ := args["content"].(string)
	if path == "" {
		return mcp.NewToolResultError("path is required"), nil
	}
	if err := checkAllowedPath(path); err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("create directory: %v", err)), nil
	}
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("write file: %v", err)), nil
	}
	return mcp.NewToolResultText(fmt.Sprintf("wrote %d bytes to %s", len(content), path)), nil
}

func handleFileList(_ context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	args := request.GetArguments()
	path, _ := args["path"].(string)
	if path == "" {
		return mcp.NewToolResultError("path is required"), nil
	}
	if err := checkAllowedPath(path); err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	entries, err := os.ReadDir(path)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("list directory: %v", err)), nil
	}
	var names []string
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() {
			name += "/"
		}
		names = append(names, name)
	}
	return mcp.NewToolResultText(strings.Join(names, "\n")), nil
}

func runCommand(timeout time.Duration, name string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, name, args...)
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
	return output, err
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
