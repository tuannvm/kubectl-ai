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
	"os"
	"path/filepath"
	"strings"

	"github.com/GoogleCloudPlatform/kubectl-ai/pkg/mcp"
	"github.com/GoogleCloudPlatform/kubectl-ai/pkg/tools"
	"github.com/GoogleCloudPlatform/kubectl-ai/pkg/ui"
	"k8s.io/klog/v2"
)

// GetMCPServerStatus returns a slice of text blocks with the current MCP server status
// This function is called to display MCP server information in the UI
func GetMCPServerStatus() ([]ui.Block, error) {

	var blocks []ui.Block

	// Try to get MCP config path
	mcpConfigPath, err := mcp.DefaultConfigPath()
	if err != nil {
		klog.Warningf("[DEBUG] Failed to get MCP config path: %v", err)
		return blocks, nil // Don't fail, just return empty blocks
	}

	// Load the config using the mcp package
	mcpConfig, err := mcp.LoadConfig(mcpConfigPath)
	if err != nil {

		return blocks, nil // Don't fail, just return empty blocks
	}

	totalServers := len(mcpConfig.Servers) + len(mcpConfig.MCPServers)

	if totalServers == 0 {
		blocks = append(blocks, ui.NewAgentTextBlock().WithText("No MCP servers configured."))
		return blocks, nil
	}

	// Build a user-friendly summary
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
	summary := fmt.Sprintf("Loaded %d MCP server(s): %s", totalServers, strings.Join(serverNames, ", "))
	blocks = append(blocks, ui.NewAgentTextBlock().WithText(summary))

	// Show details for each server
	for _, server := range mcpConfig.Servers {
		serverBlock := ui.NewAgentTextBlock()
		serverBlock.SetText(fmt.Sprintf("    • %s (%s)", server.Name, server.Command))
		blocks = append(blocks, serverBlock)
	}
	for name, server := range mcpConfig.MCPServers {
		serverName := name
		if server.Name != "" {
			serverName = server.Name
		}
		serverBlock := ui.NewAgentTextBlock()
		serverBlock.SetText(fmt.Sprintf("    • %s (%s) (legacy)", serverName, server.Command))
		blocks = append(blocks, serverBlock)
	}

	return blocks, nil
}

// StartMCPServer starts the MCP server with the given configuration
func StartMCPServer(ctx context.Context, opt Options) error {
	workDir := filepath.Join(os.TempDir(), "kubectl-ai-mcp")

	mcpServer, err := newKubectlMCPServer(ctx, opt.KubeConfigPath, tools.Default(), workDir)
	if err != nil {
		return fmt.Errorf("creating mcp server: %w", err)
	}
	return mcpServer.Serve(ctx)
}

// LoadMCPConfig loads and logs the MCP configuration
func LoadMCPConfig() {
	mcpConfigPath, err := mcp.DefaultConfigPath()
	if err == nil {
		if mcpConfig, err := mcp.LoadConfig(mcpConfigPath); err == nil {
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
		} else {
			klog.Warningf("Failed to load MCP config from %s: %v", mcpConfigPath, err)
		}
	} else {
		klog.Warningf("Failed to get MCP config path: %v", err)
	}
}
