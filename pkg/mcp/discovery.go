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
	"time"

	"k8s.io/klog/v2"
)

// InitializeManager creates and initializes the MCP manager
func InitializeManager() (*Manager, error) {
	klog.V(1).Info("Initializing MCP client functionality")

	config, err := LoadConfig("")
	if err != nil {
		klog.V(2).Info("Failed to load MCP config", "error", err)
		return nil, err
	}

	// Create MCP manager
	manager := NewManager(config)
	return manager, nil
}

// DiscoverAndConnectServers connects to all configured MCP servers and discovers their tools
func (m *Manager) DiscoverAndConnectServers(ctx context.Context) error {
	// Connect to all configured servers with retries
	klog.V(1).Info("Connecting to MCP servers")
	connectCtx, connectCancel := context.WithTimeout(ctx, 30*time.Second)
	defer connectCancel()

	if err := m.ConnectAll(connectCtx); err != nil {
		klog.V(2).Info("Failed to connect to some MCP servers during auto-discovery", "error", err)
		// Continue with partial connections
	}

	// Give servers a moment to stabilize before discovering tools
	time.Sleep(2 * time.Second)

	return nil
}

// RefreshToolDiscovery forces a refresh of tool discovery from connected servers
func (m *Manager) RefreshToolDiscovery(ctx context.Context) (map[string][]ToolInfo, error) {
	var serverTools map[string][]ToolInfo

	// Use the retry utility for tool discovery
	retryConfig := DefaultRetryConfig("tool discovery from MCP servers")
	err := RetryOperation(ctx, retryConfig, func() error {
		var err error
		serverTools, err = m.ListAvailableTools(ctx)
		return err
	})

	if err != nil {
		klog.Warningf("Failed to discover tools after retries: %v", err)
		return nil, err
	}

	// Log discovery results
	toolCount := 0
	for serverName, tools := range serverTools {
		klog.V(1).Info("Discovered tools from MCP server", "server", serverName, "toolCount", len(tools))
		toolCount += len(tools)
	}

	if toolCount > 0 {
		klog.InfoS("Successfully discovered MCP tools", "totalTools", toolCount)
	} else {
		klog.V(1).Info("No MCP tools were discovered from connected servers")
	}

	return serverTools, nil
}
