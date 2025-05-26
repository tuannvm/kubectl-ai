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
	"time"

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

// MCPStatus represents the overall status of MCP servers and tools
type MCPStatus struct {
	// Individual server status details
	ServerInfoList []ServerConnectionInfo
	// Total number of configured servers
	TotalServers int
	// Number of successfully connected servers
	ConnectedCount int
	// Number of servers that failed to connect
	FailedCount int
	// Total number of tools available across all servers
	TotalTools int
	// Whether MCP client mode is enabled
	ClientEnabled bool
}

// MCPManager defines the interface needed by GetServerStatus to interact with MCP
type MCPManager interface {
	ListClients() []*Client
	ListAvailableTools(ctx context.Context) (map[string][]Tool, error)
}

// GetServerStatus returns status information for all MCP servers
func GetServerStatus(ctx context.Context, mcpClientEnabled bool, mcpManager MCPManager) (*MCPStatus, error) {
	status := &MCPStatus{
		ClientEnabled: mcpClientEnabled,
	}

	// Try to get MCP config path
	mcpConfigPath, err := DefaultConfigPath()
	if err != nil {
		klog.V(2).Infof("Failed to get MCP config path: %v", err)
		return status, nil // Don't fail, just return empty status
	}

	// Load the config using the mcp package
	mcpConfig, err := LoadConfig(mcpConfigPath)
	if err != nil {
		return status, nil // Don't fail, just return empty status
	}

	status.TotalServers = len(mcpConfig.Servers) + len(mcpConfig.MCPServers)

	if status.TotalServers == 0 {
		return status, nil
	}

	// Get connection status and tools from MCP manager - only when client mode is enabled
	var serverTools map[string][]Tool
	var connectedClients []*Client

	if mcpClientEnabled && mcpManager != nil {
		// Get list of successfully connected clients
		connectedClients = mcpManager.ListClients()
		status.ConnectedCount = len(connectedClients)
		status.FailedCount = status.TotalServers - status.ConnectedCount

		// Try to get available tools with a short timeout
		toolsCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()

		serverTools, err = mcpManager.ListAvailableTools(toolsCtx)
		if err != nil {
			klog.V(2).InfoS("Failed to get tools from MCP manager", "error", err)
			serverTools = make(map[string][]Tool) // Empty map to avoid nil panics
		}

		// Count total discovered tools
		for _, toolList := range serverTools {
			status.TotalTools += len(toolList)
		}
	} else {
		serverTools = make(map[string][]Tool) // Empty map
	}

	// Create a map of connected server names for quick lookup
	connectedServerNames := make(map[string]bool)
	if mcpClientEnabled {
		for _, client := range connectedClients {
			connectedServerNames[client.Name] = true
		}
	}

	// Collect information for each server
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

		status.ServerInfoList = append(status.ServerInfoList, serverInfo)
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

		status.ServerInfoList = append(status.ServerInfoList, serverInfo)
	}

	return status, nil
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
		klog.V(2).Infof("Loaded %d MCP %s from %s", totalServers, serverWord, mcpConfigPath)

		// Log servers from the new format
		for _, server := range mcpConfig.Servers {
			klog.V(2).Infof("  - %s: %s", server.Name, server.Command)
		}

		// Log servers from the legacy format
		for name, server := range mcpConfig.MCPServers {
			serverName := name
			if server.Name != "" {
				serverName = server.Name
			}
			klog.V(2).Infof("  - %s: %s (legacy)", serverName, server.Command)
		}
	}

	return nil
}
