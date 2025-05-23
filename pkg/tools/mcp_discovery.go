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
	"os"
	"time"

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

// discoverAndRegisterMCPTools loads MCP configuration, connects to servers, and registers discovered tools
func discoverAndRegisterMCPTools() error {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	// Load MCP configuration
	config, err := mcp.LoadConfig("")
	if err != nil {
		return err
	}

	// If no servers configured, nothing to do
	totalServers := len(config.Servers) + len(config.MCPServers)
	if totalServers == 0 {
		klog.V(2).Info("No MCP servers configured for auto-discovery")
		return nil
	}

	// Create MCP manager
	mcpManager = mcp.NewManager(config)

	// Connect to all configured servers with retries
	klog.V(1).InfoS("Connecting to MCP servers", "totalServers", totalServers)
	if err := mcpManager.ConnectAll(ctx); err != nil {
		klog.V(2).Info("Failed to connect to some MCP servers during auto-discovery", "error", err)
		// Continue with partial connections
	}

	// Give servers a moment to stabilize before discovering tools
	time.Sleep(2 * time.Second)

	// Discover and register tools from connected servers
	return registerToolsFromConnectedServers(ctx)
}

// registerToolsFromConnectedServers discovers tools from all connected MCP servers and registers them
func registerToolsFromConnectedServers(ctx context.Context) error {
	if mcpManager == nil {
		return nil
	}

	// Try to get list of available tools with retry logic
	var serverTools map[string][]mcp.ToolInfo
	var err error

	maxRetries := 3
	for attempt := 1; attempt <= maxRetries; attempt++ {
		klog.V(2).InfoS("Attempting to discover tools from MCP servers", "attempt", attempt, "maxRetries", maxRetries)

		serverTools, err = mcpManager.ListAvailableTools(ctx)
		if err == nil {
			break
		}

		if attempt < maxRetries {
			klog.V(3).InfoS("Tool discovery failed, retrying", "attempt", attempt, "error", err)
			time.Sleep(time.Duration(attempt) * time.Second) // Progressive backoff
		}
	}

	if err != nil {
		klog.Warningf("Failed to discover tools after %d attempts: %v", maxRetries, err)
		return err
	}

	toolCount := 0
	for serverName, tools := range serverTools {
		klog.V(1).Info("Discovering tools from MCP server", "server", serverName, "toolCount", len(tools))

		for _, toolInfo := range tools {
			// Create schema for the tool
			schema, err := convertMCPSchemaToGollm(&mcp.Tool{
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
		klog.InfoS("Successfully discovered and registered MCP tools", "totalTools", toolCount)
	} else {
		klog.V(1).Info("No MCP tools were discovered from connected servers")
	}

	return nil
}

// InitializeMCPClient explicitly initializes MCP client functionality when --mcp-client flag is used
func InitializeMCPClient() error {
	klog.V(1).Info("Initializing MCP client functionality")

	// Initialize synchronously to ensure manager is ready before UI queries it
	if err := discoverAndRegisterMCPTools(); err != nil {
		klog.V(2).Info("MCP tool discovery failed (this is expected if no MCP config exists)", "error", err)
		return err
	}

	return nil
}

// GetMCPManager returns the global MCP manager instance (for UI integration)
func GetMCPManager() *mcp.Manager {
	return mcpManager
}

// RefreshMCPTools forces a refresh of MCP tools (for manual refresh scenarios)
func RefreshMCPTools(ctx context.Context) error {
	if mcpManager == nil {
		return discoverAndRegisterMCPTools()
	}

	return registerToolsFromConnectedServers(ctx)
}
