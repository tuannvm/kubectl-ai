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
	ctx := context.Background()

	// Get MCPManager instance if client mode is enabled
	var status *mcp.MCPStatus
	var err error

	if mcpClientEnabled {
		// Get the existing manager
		mcpManager := tools.GetMCPManager()
		if mcpManager != nil {
			status, err = mcpManager.GetStatus(ctx, mcpClientEnabled)
			if err != nil {
				klog.Errorf("Failed to get MCP server status: %v", err)
				return nil, err
			}
		} else {
			// Manager should be available but isn't, create empty status
			status = &mcp.MCPStatus{ClientEnabled: mcpClientEnabled}
		}
	} else {
		// In non-client mode, create a temporary manager just for the status
		tmpManager := &mcp.Manager{}
		status, err = tmpManager.GetStatus(ctx, mcpClientEnabled)
		if err != nil {
			klog.Errorf("Failed to get MCP server status: %v", err)
			return nil, err
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
	if err != nil {
		klog.Warningf("Failed to get MCP config path: %v", err)
		return
	}

	// Create a temporary Manager instance to call LogConfig
	manager := &mcp.Manager{}
	if err := manager.LogConfig(mcpConfigPath); err != nil {
		klog.Warningf("Failed to load or log MCP config: %v", err)
	}
}
