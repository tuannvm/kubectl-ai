// Copyright 2025 Google LLC
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package mcp

import (
	"context"
	"fmt"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	mcp "github.com/mark3labs/mcp-go/mcp"
	"k8s.io/klog/v2"
)

// RetryConfig defines retry behavior for MCP operations
type RetryConfig struct {
	MaxRetries  int
	BaseDelay   time.Duration
	MaxDelay    time.Duration
	Multiplier  float64
	Description string
}

// DefaultRetryConfig returns a sensible default retry configuration
func DefaultRetryConfig(description string) RetryConfig {
	return RetryConfig{
		MaxRetries:  3,
		BaseDelay:   1 * time.Second,
		MaxDelay:    10 * time.Second,
		Multiplier:  2.0,
		Description: description,
	}
}

// RetryOperation executes an operation with exponential backoff retry
func RetryOperation(ctx context.Context, config RetryConfig, operation func() error) error {
	var lastErr error

	for attempt := 1; attempt <= config.MaxRetries; attempt++ {
		klog.V(LogLevelDebug).InfoS("Attempting operation",
			"operation", config.Description,
			"attempt", attempt,
			"maxRetries", config.MaxRetries)

		if err := operation(); err == nil {
			if attempt > 1 {
				klog.V(LogLevelInfo).InfoS("Operation succeeded after retry",
					"operation", config.Description,
					"attempt", attempt)
			}
			return nil
		} else {
			lastErr = err

			if attempt < config.MaxRetries {
				delay := calculateBackoffDelay(attempt, config)
				klog.V(LogLevelDebug).InfoS("Operation failed, retrying",
					"operation", config.Description,
					"attempt", attempt,
					"error", err,
					"nextRetryIn", delay)

				select {
				case <-ctx.Done():
					return fmt.Errorf("operation cancelled: %w", ctx.Err())
				case <-time.After(delay):
					// Continue to next attempt
				}
			}
		}
	}

	return fmt.Errorf("operation failed after %d attempts: %w", config.MaxRetries, lastErr)
}

// calculateBackoffDelay calculates exponential backoff delay with jitter
func calculateBackoffDelay(attempt int, config RetryConfig) time.Duration {
	delay := float64(config.BaseDelay) * math.Pow(config.Multiplier, float64(attempt-1))

	if time.Duration(delay) > config.MaxDelay {
		return config.MaxDelay
	}

	return time.Duration(delay)
}

// SanitizeServerName ensures server names are valid identifiers
func SanitizeServerName(name string) string {
	// Simple sanitization - replace invalid characters
	result := ""
	for _, char := range name {
		if (char >= 'a' && char <= 'z') ||
			(char >= 'A' && char <= 'Z') ||
			(char >= '0' && char <= '9') ||
			char == '-' || char == '_' {
			result += string(char)
		} else {
			result += "_"
		}
	}

	if result == "" {
		result = "unnamed"
	}

	return result
}

// GroupToolsByServer groups tools by their server name for easier display
func GroupToolsByServer(tools map[string][]Tool) map[string]int {
	summary := make(map[string]int)

	for serverName, serverTools := range tools {
		summary[serverName] = len(serverTools)
	}

	return summary
}

// mergeEnvironmentVariables merges process environment with custom environment variables
func mergeEnvironmentVariables(processEnv, customEnv []string) []string {
	envMap := make(map[string]string)

	// Parse process environment
	for _, e := range processEnv {
		if parts := strings.SplitN(e, "=", 2); len(parts) == 2 {
			envMap[parts[0]] = parts[1]
		}
	}

	// Override with custom environment variables
	for _, env := range customEnv {
		if parts := strings.SplitN(env, "=", 2); len(parts) == 2 {
			envMap[parts[0]] = parts[1]
		}
	}

	// Convert back to slice
	finalEnv := make([]string, 0, len(envMap))
	for k, v := range envMap {
		finalEnv = append(finalEnv, fmt.Sprintf("%s=%s", k, v))
	}

	return finalEnv
}

// expandPath expands the command path, handling ~ and environment variables
// If the path is just a binary name (no path separators), it looks in $PATH
func expandPath(path string) (string, error) {
	if path == "" {
		return "", fmt.Errorf("path cannot be empty")
	}

	// Expand environment variables first
	expanded := os.ExpandEnv(path)

	// If the command contains no path separators, look it up in $PATH first
	if !strings.Contains(expanded, string(filepath.Separator)) && !strings.HasPrefix(expanded, "~") {
		klog.V(LogLevelInfo).InfoS("Attempting PATH lookup for command", "command", expanded)
		// Try to find the command in $PATH
		if pathResolved, err := exec.LookPath(expanded); err == nil {
			klog.V(LogLevelInfo).InfoS("Found command in PATH", "command", expanded, "resolved", pathResolved)
			return pathResolved, nil
		} else {
			klog.V(LogLevelInfo).InfoS("Command not found in PATH", "command", expanded, "error", err)
		}
		// If not found in PATH, continue with the original logic below
		klog.V(LogLevelInfo).InfoS("Command not found in PATH, trying relative to current directory", "command", expanded)
	} else {
		klog.V(LogLevelInfo).InfoS("Skipping PATH lookup", "command", expanded, "hasPathSeparator", strings.Contains(expanded, string(filepath.Separator)), "hasTilde", strings.HasPrefix(expanded, "~"))
	}

	// Handle ~ for home directory
	if strings.HasPrefix(expanded, "~") {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("getting home directory: %w", err)
		}
		expanded = filepath.Join(home, expanded[1:])
	}

	// Clean the path to remove any . or .. elements
	expanded = filepath.Clean(expanded)

	// Make the path absolute if it's not already
	if !filepath.IsAbs(expanded) {
		cwd, err := os.Getwd()
		if err != nil {
			return "", fmt.Errorf("getting current working directory: %w", err)
		}
		expanded = filepath.Clean(filepath.Join(cwd, expanded))
	}

	// Verify the file exists and is executable
	info, err := os.Stat(expanded)
	if err != nil {
		return "", fmt.Errorf(ErrPathCheckFmt, expanded, err)
	}

	// Check if it's a regular file and executable
	if !info.Mode().IsRegular() {
		return "", fmt.Errorf("path %q is not a regular file", expanded)
	}

	// Check if the file is executable by the current user
	if info.Mode().Perm()&0111 == 0 {
		return "", fmt.Errorf("file %q is not executable", expanded)
	}

	return expanded, nil
}

// =============================================================================
// Helper Functions to Reduce Redundancy
// =============================================================================

// withTimeout creates a context with the specified timeout and returns the context and cancel function
func withTimeout(ctx context.Context, timeout time.Duration) (context.Context, context.CancelFunc) {
	return context.WithTimeout(ctx, timeout)
}

// ensureConnected checks if the client is connected and returns an error if not
func (c *Client) ensureConnected() error {
	if c.client == nil {
		return fmt.Errorf("not connected to MCP server")
	}
	return nil
}

// convertMCPToolsToTools converts MCP library tools to our Tool type
func convertMCPToolsToTools(mcpTools []mcp.Tool) []Tool {
	tools := make([]Tool, 0, len(mcpTools))
	for _, tool := range mcpTools {
		tools = append(tools, Tool{
			Name:        tool.Name,
			Description: tool.Description,
		})
	}
	return tools
}
