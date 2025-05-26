// Copyright 2025 Google LLC
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//      http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package main

import (
	"context"
	"fmt"
	"strings"

	"github.com/GoogleCloudPlatform/kubectl-ai/pkg/mcp"
	"github.com/GoogleCloudPlatform/kubectl-ai/pkg/tools"
	"github.com/GoogleCloudPlatform/kubectl-ai/pkg/ui"
	"k8s.io/klog/v2"
)

// GetMCPServerStatus returns a slice of text blocks with the current MCP server status
// This function is called to display MCP server information in the UI
func GetMCPServerStatus() ([]ui.Block, error) {
	return GetMCPServerStatusWithClientMode(false)
}

// GetMCPServerStatusWithClientMode returns a slice of text blocks with the current MCP server status
// mcpClientEnabled indicates whether MCP client mode is active
func GetMCPServerStatusWithClientMode(mcpClientEnabled bool) ([]ui.Block, error) {
	// Simply delegate to the mcp package function
	ctx := context.Background()
	var mcpManager mcp.MCPManager

	if mcpClientEnabled {
		mcpManager = tools.GetMCPManager()
	}

	status, err := mcp.GetServerStatus(ctx, mcpClientEnabled, mcpManager)
	if err != nil {
		klog.Errorf("Failed to get MCP server status: %v", err)
		return nil, err
	}

	// Log MCP server status details
	if status != nil {
		klog.Infof("MCP Status: Total Servers=%d, Connected=%d, Failed=%d, Total Tools=%d, Client Enabled=%v",
			status.TotalServers, status.ConnectedCount, status.FailedCount, status.TotalTools, status.ClientEnabled)

		// Log individual server details
		for _, server := range status.ServerInfoList {
			toolCount := len(server.AvailableTools)
			klog.V(2).Infof("MCP Server: %s, Command=%s, Legacy=%v, Connected=%v, Tools=%d",
				server.Name, server.Command, server.IsLegacy, server.IsConnected, toolCount)
		}
	}

	// Convert the status into UI blocks and return
	return GenerateMCPStatusBlocks(status), nil
}

// StartMCPServer starts the MCP server with the given configuration
func StartMCPServer(ctx context.Context, opt Options) error {
	return startMCPServer(ctx, opt)
}

// LoadMCPConfig loads and logs the MCP configuration
func LoadMCPConfig() {
	mcpConfigPath, err := mcp.DefaultConfigPath()
	if err == nil {
		if err := mcp.LogConfig(mcpConfigPath); err != nil {
			klog.Warningf("Failed to load or log MCP config: %v", err)
		}
	} else {
		klog.Warningf("Failed to get MCP config path: %v", err)
	}
}

// GenerateMCPStatusBlocks converts MCP server status into UI blocks
// This is the main entry point for generating UI blocks from MCP status data
func GenerateMCPStatusBlocks(status *mcp.MCPStatus) []ui.Block {
	var blocks []ui.Block

	if status == nil || status.TotalServers == 0 {
		blocks = append(blocks, ui.NewAgentTextBlock().WithText("No MCP servers configured."))
		return blocks
	}

	// Add summary block based on connection status
	if status.ClientEnabled {
		blocks = append(blocks, buildConnectionSummaryBlock(status))
	} else {
		blocks = append(blocks, buildConfiguredServersBlock(status))
	}

	// Add details for each server
	for _, serverInfo := range status.ServerInfoList {
		serverBlock := ui.NewAgentTextBlock()
		serverText := formatServerStatus(serverInfo, status.ClientEnabled)
		serverBlock.SetText(serverText)
		blocks = append(blocks, serverBlock)
	}

	return blocks
}

// formatServerStatus formats a single server's status as a text string
func formatServerStatus(info mcp.ServerConnectionInfo, mcpClientEnabled bool) string {
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

// buildConnectionSummaryBlock creates a summary block for connected servers
func buildConnectionSummaryBlock(status *mcp.MCPStatus) ui.Block {
	var summary string
	if status.ConnectedCount == 0 {
		summary = fmt.Sprintf("Failed to connect to all %d MCP server(s)", status.TotalServers)
	} else if status.FailedCount == 0 {
		summary = fmt.Sprintf("Successfully connected to %d MCP server(s)", status.ConnectedCount)
		if status.TotalTools > 0 {
			summary += fmt.Sprintf(" (%d tools discovered)", status.TotalTools)
		}
	} else {
		summary = fmt.Sprintf("Connected to %d/%d MCP server(s) (%d failed)",
			status.ConnectedCount, status.TotalServers, status.FailedCount)
		if status.TotalTools > 0 {
			summary += fmt.Sprintf(" (%d tools discovered)", status.TotalTools)
		}
	}

	return ui.NewAgentTextBlock().WithText(summary)
}

// buildConfiguredServersBlock creates a summary block for configured servers when client mode is disabled
func buildConfiguredServersBlock(status *mcp.MCPStatus) ui.Block {
	// Extract server names for display
	serverNames := make([]string, 0, status.TotalServers)
	for _, serverInfo := range status.ServerInfoList {
		serverNames = append(serverNames, serverInfo.Name)
	}

	summary := fmt.Sprintf("Found %d configured MCP server(s): %s (MCP client mode disabled - use --mcp-client to enable)",
		status.TotalServers, strings.Join(serverNames, ", "))
	return ui.NewAgentTextBlock().WithText(summary)
}
