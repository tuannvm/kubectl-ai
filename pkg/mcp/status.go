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
	"strings"
	"time"

	"github.com/GoogleCloudPlatform/kubectl-ai/pkg/ui"
	"k8s.io/klog/v2"
)

// ServerConnectionInfo holds connection status information for a server
type ServerConnectionInfo struct {
	Name           string
	Command        string
	IsLegacy       bool
	IsConnected    bool
	AvailableTools []Tool
}

// FormatServerStatus formats server status as a text string
func FormatServerStatus(info ServerConnectionInfo, mcpClientEnabled bool) string {
	serverText := fmt.Sprintf("    • %s (%s)", info.Name, info.Command)
	if info.IsLegacy {
		serverText += " (legacy)"
	}

	if mcpClientEnabled {
		if info.IsConnected {
			serverText += " - Connected"
			if len(info.AvailableTools) > 0 {
				toolNames := make([]string, len(info.AvailableTools))
				for i, tool := range info.AvailableTools {
					toolNames[i] = tool.Name
				}
				serverText += fmt.Sprintf(", Tools: %s", strings.Join(toolNames, ", "))
			} else {
				serverText += ", No tools discovered"
			}
		} else {
			serverText += " - Connection failed"
		}
	} else {
		serverText += " - Not connected (--mcp-client disabled)"
	}

	return serverText
}

// GetServerStatusBlocks returns UI blocks with server status information
func GetServerStatusBlocks(ctx context.Context, mcpClientEnabled bool, mcpManager MCPManager) ([]ui.Block, error) {
	var blocks []ui.Block

	// Try to get MCP config path
	mcpConfigPath, err := DefaultConfigPath()
	if err != nil {
		klog.Warningf("[DEBUG] Failed to get MCP config path: %v", err)
		return blocks, nil // Don't fail, just return empty blocks
	}

	// Load the config using the mcp package
	mcpConfig, err := LoadConfig(mcpConfigPath)
	if err != nil {
		return blocks, nil // Don't fail, just return empty blocks
	}

	totalServers := len(mcpConfig.Servers) + len(mcpConfig.MCPServers)

	if totalServers == 0 {
		blocks = append(blocks, ui.NewAgentTextBlock().WithText("No MCP servers configured."))
		return blocks, nil
	}

	// Get connection status and tools from MCP manager - only when client mode is enabled
	var serverTools map[string][]Tool
	var connectedClients []*Client

	if mcpClientEnabled && mcpManager != nil {
		// Get list of successfully connected clients
		connectedClients = mcpManager.ListClients()

		// Try to get available tools with a short timeout
		toolsCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()

		serverTools, err = mcpManager.ListAvailableTools(toolsCtx)
		if err != nil {
			klog.V(2).InfoS("Failed to get tools from MCP manager", "error", err)
			serverTools = make(map[string][]Tool) // Empty map to avoid nil panics
		}
	} else {
		serverTools = make(map[string][]Tool) // Empty map
	}

	// Build connection status summary
	if mcpClientEnabled {
		blocks = append(blocks, buildConnectionSummaryBlock(connectedClients, totalServers, serverTools))
	} else {
		blocks = append(blocks, buildConfiguredServersBlock(mcpConfig, totalServers))
	}

	// Create a map of connected server names for quick lookup
	connectedServerNames := make(map[string]bool)
	if mcpClientEnabled {
		for _, client := range connectedClients {
			connectedServerNames[client.Name] = true
		}
	}

	// Show details for each server with their connection status and tools
	for _, server := range mcpConfig.Servers {
		serverInfo := ServerConnectionInfo{
			Name:        server.Name,
			Command:     server.Command,
			IsLegacy:    false,
			IsConnected: connectedServerNames[server.Name],
		}

		if tools, exists := serverTools[server.Name]; exists {
			serverInfo.AvailableTools = tools
		}

		serverBlock := ui.NewAgentTextBlock()
		serverBlock.SetText(FormatServerStatus(serverInfo, mcpClientEnabled))
		blocks = append(blocks, serverBlock)
	}

	for name, server := range mcpConfig.MCPServers {
		serverName := name
		if server.Name != "" {
			serverName = server.Name
		}

		serverInfo := ServerConnectionInfo{
			Name:        serverName,
			Command:     server.Command,
			IsLegacy:    true,
			IsConnected: connectedServerNames[serverName],
		}

		if tools, exists := serverTools[serverName]; exists {
			serverInfo.AvailableTools = tools
		}

		serverBlock := ui.NewAgentTextBlock()
		serverBlock.SetText(FormatServerStatus(serverInfo, mcpClientEnabled))
		blocks = append(blocks, serverBlock)
	}

	return blocks, nil
}

func buildConnectionSummaryBlock(connectedClients []*Client, totalServers int, serverTools map[string][]Tool) ui.Block {
	connectedCount := len(connectedClients)
	failedCount := totalServers - connectedCount

	// Count total discovered tools
	totalTools := 0
	for _, toolList := range serverTools {
		totalTools += len(toolList)
	}

	var summary string
	if connectedCount == 0 {
		summary = fmt.Sprintf("Failed to connect to all %d MCP server(s)", totalServers)
	} else if failedCount == 0 {
		summary = fmt.Sprintf("Successfully connected to %d MCP server(s)", connectedCount)
		if totalTools > 0 {
			summary += fmt.Sprintf(" (%d tools discovered)", totalTools)
		}
	} else {
		summary = fmt.Sprintf("Connected to %d/%d MCP server(s) (%d failed)", connectedCount, totalServers, failedCount)
		if totalTools > 0 {
			summary += fmt.Sprintf(" (%d tools discovered)", totalTools)
		}
	}

	return ui.NewAgentTextBlock().WithText(summary)
}

func buildConfiguredServersBlock(mcpConfig *Config, totalServers int) ui.Block {
	serverNames := []string{}
	for _, server := range mcpConfig.Servers {
		serverNames = append(serverNames, server.Name)
	}
	for name, server := range mcpConfig.MCPServers {
		if server.Name != "" {
			serverNames = append(serverNames, server.Name)
		} else {
			serverNames = append(serverNames, name)
		}
	}

	summary := fmt.Sprintf("Found %d configured MCP server(s): %s (MCP client mode disabled - use --mcp-client to enable)",
		totalServers, strings.Join(serverNames, ", "))
	return ui.NewAgentTextBlock().WithText(summary)
}

// MCPManager defines the interface needed by GetServerStatusBlocks to interact with MCP
type MCPManager interface {
	ListClients() []*Client
	ListAvailableTools(ctx context.Context) (map[string][]Tool, error)
}

// LogConfig logs the MCP configuration summary to klog
func LogConfig(mcpConfigPath string) error {
	mcpConfig, err := LoadConfig(mcpConfigPath)
	if err != nil {
		return fmt.Errorf("failed to load MCP config from %s: %w", mcpConfigPath, err)
	}

	serverCount := len(mcpConfig.Servers)
	legacyServerCount := len(mcpConfig.MCPServers)
	totalServers := serverCount + legacyServerCount

	if totalServers > 0 {
		serverWord := "server"
		if totalServers > 1 {
			serverWord = "servers"
		}
		klog.Infof("Loaded %d MCP %s from %s", totalServers, serverWord, mcpConfigPath)

		// Log servers from the new format
		for _, server := range mcpConfig.Servers {
			klog.Infof("  - %s: %s", server.Name, server.Command)
		}

		// Log servers from the legacy format
		for name, server := range mcpConfig.MCPServers {
			serverName := name
			if server.Name != "" {
				serverName = server.Name
			}
			klog.Infof("  - %s: %s (legacy)", serverName, server.Command)
		}
	}

	return nil
}
