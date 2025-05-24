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

package tools

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/GoogleCloudPlatform/kubectl-ai/gollm"
	"github.com/GoogleCloudPlatform/kubectl-ai/pkg/mcp"
	"k8s.io/klog/v2"
)

var mcpManager *mcp.Manager

// init automatically discovers and registers MCP tools during package initialization
func init() {
	// Only auto-discover if MCP_AUTO_DISCOVER is not explicitly set to false
	if autodiscover := os.Getenv("MCP_AUTO_DISCOVER"); autodiscover == "false" {
		klog.V(2).Info("MCP auto-discovery disabled via MCP_AUTO_DISCOVER=false")
		return
	}

	// MCP client initialization will be handled via explicit --mcp-client flag
}

// MCPTool wraps an MCP server tool to implement the Tool interface
type MCPTool struct {
	serverName  string
	toolName    string
	description string
	schema      *gollm.FunctionDefinition
	manager     *mcp.Manager
}

// NewMCPTool creates a new MCP tool wrapper
func NewMCPTool(serverName, toolName, description string, schema *gollm.FunctionDefinition, manager *mcp.Manager) *MCPTool {
	return &MCPTool{
		serverName:  serverName,
		toolName:    toolName,
		description: description,
		schema:      schema,
		manager:     manager,
	}
}

// Name returns the tool name
func (t *MCPTool) Name() string {
	return t.toolName
}

// ServerName returns the MCP server name
func (t *MCPTool) ServerName() string {
	return t.serverName
}

// Description returns the tool description
func (t *MCPTool) Description() string {
	return t.description
}

// FunctionDefinition returns the tool's function definition
func (t *MCPTool) FunctionDefinition() *gollm.FunctionDefinition {
	return t.schema
}

// Run executes the MCP tool with enhanced logging and feedback
func (t *MCPTool) Run(ctx context.Context, args map[string]any) (any, error) {
	log := klog.FromContext(ctx)

	// Show MCP invocation message to user
	if err := ShowMCPInvocationMessage(ctx, t.toolName, t.serverName); err != nil {
		log.V(2).Info("Failed to show MCP invocation message", "error", err)
	}

	// Enhanced logging for debugging
	log.V(1).Info("🔧 [MCP] Starting tool execution",
		"tool", t.toolName,
		"server", t.serverName,
		"args", formatArgsForDisplay(args))

	// Get MCP client for the server
	client, exists := t.manager.GetClient(t.serverName)
	if !exists {
		err := fmt.Errorf("MCP server %q not connected", t.serverName)
		log.Error(err, "❌ [MCP] Server connection failed", "server", t.serverName)
		return nil, err
	}

	log.V(1).Info("✅ [MCP] Server connection verified", "server", t.serverName)

	// Execute tool on MCP server
	result, err := client.CallTool(ctx, t.toolName, args)
	if err != nil {
		log.Error(err, "❌ [MCP] Tool execution failed",
			"tool", t.toolName,
			"server", t.serverName,
			"args", formatArgsForDisplay(args))
		return nil, fmt.Errorf("calling MCP tool %q on server %q: %w", t.toolName, t.serverName, err)
	}

	log.V(1).Info("🎉 [MCP] Tool executed successfully",
		"tool", t.toolName,
		"server", t.serverName,
		"resultLength", len(fmt.Sprintf("%v", result)))

	// Return enhanced result with MCP context
	return &MCPToolResult{
		ServerName: t.serverName,
		ToolName:   t.toolName,
		Result:     result,
		Success:    true,
	}, nil
}

// MCPToolResult wraps MCP tool execution results with metadata
type MCPToolResult struct {
	ServerName string `json:"mcp_server"`
	ToolName   string `json:"mcp_tool"`
	Result     any    `json:"result"`
	Success    bool   `json:"success"`
}

// String provides a user-friendly string representation
func (r *MCPToolResult) String() string {
	status := "✅ SUCCESS"
	if !r.Success {
		status = "❌ FAILED"
	}

	resultStr := fmt.Sprintf("%v", r.Result)
	if len(resultStr) > 200 {
		resultStr = resultStr[:197] + "..."
	}

	return fmt.Sprintf("🔧 [MCP:%s] %s executed %s\nResult: %s",
		r.ServerName, r.ToolName, status, resultStr)
}

// formatArgsForDisplay creates a clean display format for arguments
func formatArgsForDisplay(args map[string]any) string {
	if len(args) == 0 {
		return "{}"
	}

	var parts []string
	for k, v := range args {
		valueStr := fmt.Sprintf("%v", v)
		if len(valueStr) > 50 {
			valueStr = valueStr[:47] + "..."
		}
		parts = append(parts, fmt.Sprintf("%s=%q", k, valueStr))
	}

	return "{" + strings.Join(parts, ", ") + "}"
}

// ShowMCPInvocationMessage displays a message to the user when an MCP tool is invoked
func ShowMCPInvocationMessage(ctx context.Context, toolName, serverName string) error {
	msg := fmt.Sprintf("🔧 [MCP:%s] Invoking %s", serverName, toolName)

	// Display message to user via stdout (visible in terminal)
	fmt.Printf("  %s\n", msg)

	// Enhanced logging with emojis for better visibility
	klog.V(1).Info(msg)
	return nil
}

// =============================================================================
// MCP Integration Functions
// =============================================================================

// registerToolsFromConnectedServers discovers tools from all connected MCP servers and registers them
func registerToolsFromConnectedServers(ctx context.Context) error {
	if mcpManager == nil {
		return nil
	}

	// Discover tools from connected servers
	serverTools, err := mcpManager.RefreshToolDiscovery(ctx)
	if err != nil {
		return err
	}

	toolCount := 0
	for serverName, tools := range serverTools {
		klog.V(1).Info("Registering tools from MCP server", "server", serverName, "toolCount", len(tools))

		for _, toolInfo := range tools {
			// Create schema for the tool using the new pkg/mcp functions
			schema, err := mcp.ConvertToolToGollm(&mcp.Tool{
				Name:        toolInfo.Name,
				Description: toolInfo.Description,
			})
			if err != nil {
				klog.Warningf("Failed to convert schema for tool %s from server %s: %v", toolInfo.Name, serverName, err)
				continue
			}

			// Create MCP tool wrapper
			mcpTool := NewMCPTool(serverName, toolInfo.Name, toolInfo.Description, schema, mcpManager)

			// Register with the tools system
			RegisterTool(mcpTool)

			klog.V(1).Info("Registered MCP tool", "tool", toolInfo.Name, "server", serverName)
			toolCount++
		}
	}

	if toolCount > 0 {
		klog.InfoS("Successfully registered MCP tools", "totalTools", toolCount)
	} else {
		klog.V(1).Info("No MCP tools were registered")
	}

	return nil
}

// InitializeMCPClient explicitly initializes MCP client functionality when --mcp-client flag is used
func InitializeMCPClient() error {
	// Initialize the MCP manager using the new pkg/mcp functions
	manager, err := mcp.InitializeManager()
	if err != nil {
		return err
	}

	mcpManager = manager

	// Start connection and tool discovery asynchronously to avoid blocking UI
	go func() {
		ctx := context.Background()

		// Connect to servers
		if err := mcpManager.DiscoverAndConnectServers(ctx); err != nil {
			klog.V(2).Info("MCP server connection failed", "error", err)
			return
		}

		// Register discovered tools
		if err := registerToolsFromConnectedServers(ctx); err != nil {
			klog.V(2).Info("MCP tool registration failed", "error", err)
		}
	}()

	return nil
}

// GetMCPManager returns the global MCP manager instance (for UI integration)
func GetMCPManager() *mcp.Manager {
	return mcpManager
}

// RefreshMCPTools forces a refresh of MCP tools (for manual refresh scenarios)
func RefreshMCPTools(ctx context.Context) error {
	if mcpManager == nil {
		return fmt.Errorf("MCP manager not initialized")
	}

	return registerToolsFromConnectedServers(ctx)
}
