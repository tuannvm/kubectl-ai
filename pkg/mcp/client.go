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

	// Step 1: Prepare environment and command
	expandedCmd, finalEnv, err := c.prepareEnvironment()
	if err != nil {
		return fmt.Errorf("preparing environment: %w", err)
	}

	// Step 2: Create the MCP client
	if err := c.createMCPClient(expandedCmd, finalEnv); err != nil {
		return fmt.Errorf("creating MCP client: %w", err)
	}

	// Step 3: Initialize the connection
	if err := c.initializeConnection(ctx); err != nil {
		c.cleanup()
		return fmt.Errorf("initializing connection: %w", err)
	}

	// Step 4: Verify the connection
	if err := c.verifyConnection(ctx); err != nil {
		c.cleanup()
		return fmt.Errorf("verifying connection: %w", err)
	}

	klog.V(2).Info("Successfully connected to MCP server", "name", c.Name)
	return nil
}

// prepareEnvironment expands the command path and merges environment variables
func (c *Client) prepareEnvironment() (string, []string, error) {
	// Expand the command path to handle ~ and environment variables
	expandedCmd, err := expandPath(c.Command)
	if err != nil {
		return "", nil, fmt.Errorf("expanding command path: %w", err)
	}

	// Build proper environment slice by merging process environment with custom env
	finalEnv := mergeEnvironmentVariables(os.Environ(), c.Env)

	return expandedCmd, finalEnv, nil
}

// createMCPClient creates the underlying MCP client
func (c *Client) createMCPClient(command string, env []string) error {
	client, err := mcpclient.NewStdioMCPClient(command, env, c.Args...)
	if err != nil {
		return fmt.Errorf("creating stdio MCP client: %w", err)
	}
	c.client = client
	return nil
}

// initializeConnection initializes the MCP connection with proper handshake
func (c *Client) initializeConnection(ctx context.Context) error {
	initCtx, initCancel := context.WithTimeout(ctx, 30*time.Second)
	defer initCancel()

	initReq := c.buildInitializeRequest()
	_, err := c.client.Initialize(initCtx, initReq)
	if err != nil {
		return fmt.Errorf("initializing MCP client: %w", err)
	}

	return nil
}

// verifyConnection verifies the connection works by testing tool listing with retry
func (c *Client) verifyConnection(ctx context.Context) error {
	verifyCtx, verifyCancel := context.WithTimeout(ctx, 10*time.Second)
	defer verifyCancel()

	_, err := c.ListTools(verifyCtx)
	if err != nil {
		klog.V(2).InfoS("First ListTools attempt failed, trying ping and retry", "server", c.Name, "error", err)
		return c.retryConnectionWithPing(ctx)
	}

	return nil
}

// retryConnectionWithPing attempts to ping the server and retry ListTools
func (c *Client) retryConnectionWithPing(ctx context.Context) error {
	// Try ping to check if server is responsive
	pingCtx, pingCancel := context.WithTimeout(ctx, 5*time.Second)
	defer pingCancel()

	if err := c.client.Ping(pingCtx); err != nil {
		klog.V(2).InfoS("Ping also failed", "server", c.Name, "error", err)
		return fmt.Errorf("server ping failed: %w", err)
	}

	klog.V(2).InfoS("Ping succeeded, retrying ListTools", "server", c.Name)

	// Retry ListTools after successful ping
	retryCtx, retryCancel := context.WithTimeout(ctx, 10*time.Second)
	defer retryCancel()

	_, err := c.ListTools(retryCtx)
	if err != nil {
		return fmt.Errorf("ListTools retry failed: %w", err)
	}

	return nil
}

// buildInitializeRequest creates the MCP initialize request
func (c *Client) buildInitializeRequest() mcp.InitializeRequest {
	return mcp.InitializeRequest{
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
}

// cleanup closes the client connection and resets the client state
func (c *Client) cleanup() {
	if c.client != nil {
		_ = c.client.Close()
		c.client = nil
	}
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
