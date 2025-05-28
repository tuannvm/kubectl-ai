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
	"strconv"
	"strings"

	"github.com/GoogleCloudPlatform/kubectl-ai/gollm"
	"github.com/GoogleCloudPlatform/kubectl-ai/pkg/mcp"
	"k8s.io/klog/v2"
)

// =============================================================================
// Schema Conversion Functions (kubectl-ai specific)
// =============================================================================

// convertToolToGollm converts an MCP tool to gollm.FunctionDefinition with a simple schema
func convertToolToGollm(mcpTool *mcp.Tool) (*gollm.FunctionDefinition, error) {
	return &gollm.FunctionDefinition{
		Name:        mcpTool.Name,
		Description: mcpTool.Description,
		Parameters: &gollm.Schema{
			Type: gollm.TypeObject,
			Properties: map[string]*gollm.Schema{
				"arguments": {
					Type:        gollm.TypeObject,
					Description: "Arguments for the MCP tool",
					Properties:  map[string]*gollm.Schema{},
				},
			},
		},
	}, nil
}

// =============================================================================
// MCP Tool Implementation
// =============================================================================

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

// Run executes the MCP tool
func (t *MCPTool) Run(ctx context.Context, args map[string]any) (any, error) {
	// Get MCP client for the server
	client, exists := t.manager.GetClient(t.serverName)
	if !exists {
		return nil, fmt.Errorf("MCP server %q not connected", t.serverName)
	}

	// Convert arguments to proper types for MCP server
	convertedArgs := convertArgsForMCP(t.serverName, args)

	// Execute tool on MCP server
	result, err := client.CallTool(ctx, t.toolName, convertedArgs)
	if err != nil {
		return nil, fmt.Errorf("calling MCP tool %q on server %q: %w", t.toolName, t.serverName, err)
	}

	return result, nil
}

// convertArgsForMCP converts arguments using pure generic approach
func convertArgsForMCP(serverName string, args map[string]any) map[string]any {
	converted := make(map[string]any)

	for key, value := range args {
		// Convert parameter name using generic snake_case to camelCase
		convertedKey := snakeToCamelCase(key)

		// Convert value type using intelligent inference
		convertedValue := convertValueType(convertedKey, value)

		converted[convertedKey] = convertedValue
	}

	return converted
}

// snakeToCamelCase converts snake_case to camelCase generically
func snakeToCamelCase(s string) string {
	if !strings.Contains(s, "_") {
		return s // No conversion needed
	}

	parts := strings.Split(s, "_")
	if len(parts) <= 1 {
		return s
	}

	// First part stays lowercase, subsequent parts get capitalized
	result := parts[0]
	for _, part := range parts[1:] {
		if len(part) > 0 {
			result += strings.ToUpper(part[:1]) + part[1:]
		}
	}
	return result
}

// convertValueType converts value to appropriate type using intelligent inference
func convertValueType(paramName string, value any) any {
	// Generic type inference based on parameter name patterns
	lowerName := strings.ToLower(paramName)

	// Common number parameter patterns
	if strings.Contains(lowerName, "number") || strings.Contains(lowerName, "count") ||
		strings.Contains(lowerName, "total") || strings.Contains(lowerName, "max") ||
		strings.Contains(lowerName, "min") || strings.Contains(lowerName, "limit") {
		return convertToNumber(value)
	}

	// Common boolean parameter patterns
	if strings.HasPrefix(lowerName, "is") || strings.HasPrefix(lowerName, "has") ||
		strings.HasPrefix(lowerName, "needs") || strings.HasPrefix(lowerName, "enable") ||
		strings.Contains(lowerName, "required") || strings.Contains(lowerName, "enabled") {
		return convertToBoolean(value)
	}

	// Default: keep as-is
	return value
}

// convertToNumber attempts to convert value to a number
func convertToNumber(value any) any {
	switch v := value.(type) {
	case string:
		if num, err := strconv.Atoi(v); err == nil {
			return num
		}
		if num, err := strconv.ParseFloat(v, 64); err == nil {
			return num
		}
	case float64:
		return int(v) // Convert float to int if it's a whole number
	case int, int64, int32:
		return v
	}
	return value // Keep original if conversion fails
}

// convertToBoolean attempts to convert value to a boolean
func convertToBoolean(value any) any {
	switch v := value.(type) {
	case string:
		if b, err := strconv.ParseBool(v); err == nil {
			return b
		}
	case bool:
		return v
	case int:
		return v != 0
	}
	return value // Keep original if conversion fails
}

// =============================================================================
// MCP Integration Functions
// =============================================================================

// InitializeMCPClient explicitly initializes MCP client functionality when --mcp-client flag is used
func InitializeMCPClient() (*mcp.Manager, error) {
	// Initialize the MCP manager using the new pkg/mcp functions
	manager, err := mcp.InitializeManager()
	if err != nil {
		return nil, err
	}

	// Start connection and tool discovery synchronously to ensure tools are available before conversation starts
	ctx := context.Background()

	// Connect to servers
	if err := manager.DiscoverAndConnectServers(ctx); err != nil {
		klog.V(2).Info("MCP server connection failed", "error", err)
		return nil, fmt.Errorf("MCP server connection failed: %w", err)
	}

	// Register discovered tools using the RegisterTools method
	if err := manager.RegisterTools(ctx, func(serverName string, toolInfo mcp.Tool) error {
		// Create schema for the tool
		schema, err := convertToolToGollm(&mcp.Tool{
			Name:        toolInfo.Name,
			Description: toolInfo.Description,
		})
		if err != nil {
			return err
		}

		// Create MCP tool wrapper
		mcpTool := NewMCPTool(serverName, toolInfo.Name, toolInfo.Description, schema, manager)

		// Register with the tools system
		RegisterTool(mcpTool)
		return nil
	}); err != nil {
		klog.V(2).Info("MCP tool registration failed", "error", err)
		return nil, fmt.Errorf("MCP tool registration failed: %w", err)
	}

	return manager, nil
}

// RefreshMCPTools forces a refresh of MCP tools (for manual refresh scenarios)
func RefreshMCPTools(ctx context.Context, manager *mcp.Manager) error {
	if manager == nil {
		return fmt.Errorf("MCP manager not initialized")
	}

	return manager.RegisterTools(ctx, func(serverName string, toolInfo mcp.Tool) error {
		// Create schema for the tool
		schema, err := convertToolToGollm(&mcp.Tool{
			Name:        toolInfo.Name,
			Description: toolInfo.Description,
		})
		if err != nil {
			return err
		}

		// Create MCP tool wrapper
		mcpTool := NewMCPTool(serverName, toolInfo.Name, toolInfo.Description, schema, manager)

		// Register with the tools system
		RegisterTool(mcpTool)
		return nil
	})
}
