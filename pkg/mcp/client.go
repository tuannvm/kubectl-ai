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
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	mcpclient "github.com/mark3labs/mcp-go/client"
	mcp "github.com/mark3labs/mcp-go/mcp"
	"k8s.io/klog/v2"
)

// Client represents an MCP client that can connect to MCP servers
type Client struct {
	// Name is a friendly name for this MCP server connection
	Name string
	// Command is the command to execute for stdio-based MCP servers
	Command string
	// Args are the arguments to pass to the command
	Args []string
	// Env are the environment variables to set for the command
	Env []string
	// client is the underlying MCP client
	client *mcpclient.Client
	// Note: cmd field removed since NewStdioMCPClient handles the server process automatically
}

// NewClient creates a new MCP client with the given configuration
func NewClient(name, command string, args []string, env map[string]string) *Client {
	// Convert env map to slice of KEY=value strings
	var envSlice []string
	for k, v := range env {
		envSlice = append(envSlice, fmt.Sprintf("%s=%s", k, v))
	}

	return &Client{
		Name:    name,
		Command: command,
		Args:    args,
		Env:     envSlice,
	}
}

// Connect establishes a connection to the MCP server
// It creates an MCP client that will start the server process automatically
func (c *Client) Connect(ctx context.Context) error {
	klog.V(2).InfoS("Connecting to MCP server", "name", c.Name, "command", c.Command, "args", c.Args)
	if c.client != nil {
		return nil // Already connected
	}

	// Expand the command path to handle ~ and environment variables
	expandedCmd, err := expandPath(c.Command)
	if err != nil {
		return fmt.Errorf("expanding command path: %w", err)
	}

	// Build proper environment slice by merging process environment with custom env
	processEnv := os.Environ()
	envMap := make(map[string]string)
	for _, e := range processEnv {
		parts := strings.SplitN(e, "=", 2)
		if len(parts) == 2 {
			envMap[parts[0]] = parts[1]
		}
	}
	// Override with custom environment variables
	for _, env := range c.Env {
		parts := strings.SplitN(env, "=", 2)
		if len(parts) == 2 {
			envMap[parts[0]] = parts[1]
		}
	}
	finalEnv := make([]string, 0, len(envMap))
	for k, v := range envMap {
		finalEnv = append(finalEnv, fmt.Sprintf("%s=%s", k, v))
	}

	// Create the MCP client with proper environment
	client, err := mcpclient.NewStdioMCPClient(expandedCmd, finalEnv, c.Args...)
	if err != nil {
		return fmt.Errorf("creating MCP client: %w", err)
	}

	c.client = client

	// Initialize the client with proper timeout - this is required before making any requests
	initCtx, initCancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer initCancel()

	initReq := mcp.InitializeRequest{
		Request: mcp.Request{
			Method: "initialize",
		},
		Params: struct {
			ProtocolVersion string                 `json:"protocolVersion"`
			Capabilities    mcp.ClientCapabilities `json:"capabilities"`
			ClientInfo      mcp.Implementation     `json:"clientInfo"`
		}{
			ProtocolVersion: mcp.LATEST_PROTOCOL_VERSION,
			Capabilities:    mcp.ClientCapabilities{},
			ClientInfo: mcp.Implementation{
				Name:    "kubectl-ai-mcp-client",
				Version: "1.0.0",
			},
		},
	}

	_, err = c.client.Initialize(initCtx, initReq)
	if err != nil {
		_ = c.client.Close() // Clean up on error
		c.client = nil
		return fmt.Errorf("initializing MCP client: %w", err)
	}

	// Verify the connection by listing tools with improved retry logic
	verifyCtx, verifyCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer verifyCancel()

	_, lastErr := c.ListTools(verifyCtx)
	if lastErr != nil {
		klog.V(2).InfoS("First ListTools attempt failed, trying ping and retry", "server", c.Name, "error", lastErr)

		// Try ping to check if server is responsive
		pingCtx, pingCancel := context.WithTimeout(context.Background(), 5*time.Second)
		if pingErr := c.client.Ping(pingCtx); pingErr != nil {
			pingCancel()
			klog.V(2).InfoS("Ping also failed", "server", c.Name, "error", pingErr)
		} else {
			pingCancel()
			klog.V(2).InfoS("Ping succeeded, retrying ListTools", "server", c.Name)

			// Retry ListTools after successful ping
			retryCtx, retryCancel := context.WithTimeout(context.Background(), 10*time.Second)
			_, lastErr = c.ListTools(retryCtx)
			retryCancel()
		}
	}

	if lastErr != nil {
		_ = c.Close() // Clean up on error
		return fmt.Errorf("verifying MCP connection with ping retry: %w", lastErr)
	}

	klog.V(2).Info("Successfully connected to MCP server", "name", c.Name)
	return nil
}

// Close closes the connection to the MCP server
func (c *Client) Close() error {
	var err error

	// Close the client if it exists
	if c.client != nil {
		err = c.client.Close()
		c.client = nil
	}

	// Note: cmd is no longer managed manually since NewStdioMCPClient
	// handles the server process lifecycle automatically

	return err
}

// ListTools lists all available tools from the MCP server
func (c *Client) ListTools(ctx context.Context) ([]Tool, error) {
	if c.client == nil {
		return nil, fmt.Errorf("not connected to MCP server")
	}

	// Call the ListTools method on the MCP server
	result, err := c.client.ListTools(ctx, mcp.ListToolsRequest{})
	if err != nil {
		return nil, fmt.Errorf("listing tools: %w", err)
	}

	// Convert the result to our simplified Tool type
	var tools []Tool
	for _, tool := range result.Tools {
		tools = append(tools, Tool{
			Name:        tool.Name,
			Description: tool.Description,
		})
	}

	klog.V(2).InfoS("Listed tools from MCP server", "count", len(tools), "server", c.Name)
	return tools, nil
}

// CallTool calls a tool on the MCP server and returns the result as a string
// The arguments should be a map of parameter names to values that will be passed to the tool
func (c *Client) CallTool(ctx context.Context, toolName string, arguments map[string]interface{}) (string, error) {
	klog.V(2).InfoS("Calling MCP tool", "server", c.Name, "tool", toolName, "args", arguments)
	if c.client == nil {
		return "", fmt.Errorf("not connected to MCP server")
	}

	// Ensure we have a valid context
	if ctx == nil {
		ctx = context.Background()
	}

	// Convert arguments to the format expected by the MCP server
	args := make(map[string]interface{})
	for k, v := range arguments {
		args[k] = v
	}

	// Add the command as an argument if not already present
	if _, ok := args["command"]; !ok {
		args["command"] = toolName
	}

	// Call the tool on the MCP server
	result, err := c.client.CallTool(ctx, mcp.CallToolRequest{
		Params: struct {
			Name      string                 `json:"name"`
			Arguments map[string]interface{} `json:"arguments,omitempty"`
			Meta      *struct {
				ProgressToken mcp.ProgressToken `json:"progressToken,omitempty"`
			} `json:"_meta,omitempty"`
		}{
			Name:      toolName,
			Arguments: args,
		},
	})

	if err != nil {
		return "", fmt.Errorf("calling tool %q: %w", toolName, err)
	}

	// Handle error response
	if result.IsError {
		if len(result.Content) > 0 {
			if textContent, ok := result.Content[0].(mcp.TextContent); ok {
				return "", fmt.Errorf("tool error: %s", textContent.Text)
			}
		}
		return "", fmt.Errorf("tool returned an error")
	}

	// Convert the result to a string
	if len(result.Content) > 0 {
		if textContent, ok := result.Content[0].(mcp.TextContent); ok {
			return textContent.Text, nil
		}
	}

	// If we couldn't extract text content, return a generic message
	return "Tool executed successfully, but no text content was returned", nil
}

// Tool represents an MCP tool
type Tool struct {
	Name        string `json:"name"`
	Description string `json:"description"`
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
		klog.V(2).InfoS("Attempting PATH lookup for command", "command", expanded)
		// Try to find the command in $PATH
		if pathResolved, err := exec.LookPath(expanded); err == nil {
			klog.V(2).InfoS("Found command in PATH", "command", expanded, "resolved", pathResolved)
			return pathResolved, nil
		} else {
			klog.V(2).InfoS("Command not found in PATH", "command", expanded, "error", err)
		}
		// If not found in PATH, continue with the original logic below
		klog.V(2).InfoS("Command not found in PATH, trying relative to current directory", "command", expanded)
	} else {
		klog.V(2).InfoS("Skipping PATH lookup", "command", expanded, "hasPathSeparator", strings.Contains(expanded, string(filepath.Separator)), "hasTilde", strings.HasPrefix(expanded, "~"))
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
		return "", fmt.Errorf("checking path %q: %w", expanded, err)
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
