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

package main

import (
	"context"
	"fmt"

	"github.com/GoogleCloudPlatform/kubectl-ai/gollm"
	"github.com/GoogleCloudPlatform/kubectl-ai/pkg/mcp"
	"github.com/GoogleCloudPlatform/kubectl-ai/pkg/tools"
	mcpgo "github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"k8s.io/klog/v2"
)

type kubectlMCPServer struct {
	kubectlConfig string
	server        *server.MCPServer
	tools         tools.Tools
	workDir       string
	mcpManager    *mcp.Manager // Add MCP manager for external tool calls
}

func newKubectlMCPServer(ctx context.Context, kubectlConfig string, tools tools.Tools, workDir string, exposeExternalTools bool) (*kubectlMCPServer, error) {
	s := &kubectlMCPServer{
		kubectlConfig: kubectlConfig,
		workDir:       workDir,
		server: server.NewMCPServer(
			"kubectl-ai",
			"0.0.1",
			server.WithToolCapabilities(true),
		),
		tools: tools,
	}

	// Add built-in tools
	for _, tool := range s.tools.AllTools() {
		toolDefn := tool.FunctionDefinition()
		toolInputSchema, err := toolDefn.Parameters.ToRawSchema()
		if err != nil {
			return nil, fmt.Errorf("converting tool schema to json.RawMessage: %w", err)
		}
		s.server.AddTool(mcpgo.NewToolWithRawSchema(
			toolDefn.Name,
			toolDefn.Description,
			toolInputSchema,
		), s.handleToolCall)
	}

	// Only discover external MCP tools if explicitly enabled
	if exposeExternalTools {
		// Initialize MCP manager to get client tools
		manager, err := mcp.InitializeManager()
		if err != nil {
			klog.Warningf("Failed to initialize MCP manager: %v", err)
			return s, nil // Return server with just built-in tools
		}

		// Store the manager for later use in tool calls
		s.mcpManager = manager

		// Connect to MCP servers and get their tools
		if err := manager.DiscoverAndConnectServers(ctx); err != nil {
			klog.Warningf("Failed to connect to MCP servers: %v", err)
			return s, nil // Return server with just built-in tools
		}

		// Get tools from all connected MCP servers
		serverTools, err := manager.ListAvailableTools(ctx)
		if err != nil {
			klog.Warningf("Failed to list tools from MCP servers: %v", err)
			return s, nil // Return server with just built-in tools
		}

		// Add tools from MCP servers
		for _, tools := range serverTools {
			for _, tool := range tools {
				// Create a schema for the tool
				schema := &gollm.FunctionDefinition{
					Name:        tool.Name,
					Description: tool.Description,
					Parameters: &gollm.Schema{
						Type: gollm.TypeObject,
						Properties: map[string]*gollm.Schema{
							"args": {
								Type:        gollm.TypeObject,
								Description: "Tool arguments",
							},
						},
					},
				}

				toolInputSchema, err := schema.Parameters.ToRawSchema()
				if err != nil {
					klog.Warningf("Failed to convert tool schema for %s: %v", tool.Name, err)
					continue
				}

				// Add the tool to the server
				s.server.AddTool(mcpgo.NewToolWithRawSchema(
					tool.Name,
					tool.Description,
					toolInputSchema,
				), s.handleToolCall)
			}
		}

		klog.Infof("MCP server initialized with external tool discovery enabled")
	} else {
		klog.Infof("MCP server initialized with external tool discovery disabled")
	}

	return s, nil
}

func (s *kubectlMCPServer) Serve(ctx context.Context) error {
	// Ensure proper cleanup of MCP manager on shutdown
	if s.mcpManager != nil {
		defer func() {
			if err := s.mcpManager.Close(); err != nil {
				klog.Warningf("Failed to close MCP manager: %v", err)
			}
		}()
	}

	klog.Info("Starting kubectl-ai MCP server")
	return server.ServeStdio(s.server)
}

func (s *kubectlMCPServer) handleToolCall(ctx context.Context, request mcpgo.CallToolRequest) (*mcpgo.CallToolResult, error) {
	toolName := request.Params.Name

	// First, try to find the tool in our built-in tools collection
	builtinTool := s.tools.Lookup(toolName)
	if builtinTool != nil {
		return s.handleBuiltinToolCall(ctx, request, builtinTool)
	}

	// If not a built-in tool, try to handle as external MCP tool
	if s.mcpManager != nil {
		return s.handleExternalMCPToolCall(ctx, request)
	}

	// Tool not found
	return &mcpgo.CallToolResult{
		IsError: true,
		Content: []mcpgo.Content{
			mcpgo.TextContent{
				Type: "text",
				Text: fmt.Sprintf("tool %q not found", toolName),
			},
		},
	}, nil
}

// handleBuiltinToolCall handles calls to built-in kubectl-ai tools
func (s *kubectlMCPServer) handleBuiltinToolCall(ctx context.Context, request mcpgo.CallToolRequest, tool tools.Tool) (*mcpgo.CallToolResult, error) {
	// Set up context for built-in tools
	ctx = context.WithValue(ctx, tools.KubeconfigKey, s.kubectlConfig)
	ctx = context.WithValue(ctx, tools.WorkDirKey, s.workDir)

	// Convert arguments to the expected type
	args, ok := request.Params.Arguments.(map[string]any)
	if !ok {
		return &mcpgo.CallToolResult{
			IsError: true,
			Content: []mcpgo.Content{
				mcpgo.TextContent{
					Type: "text",
					Text: fmt.Sprintf("invalid arguments type: expected map[string]any, got %T", request.Params.Arguments),
				},
			},
		}, nil
	}

	// Execute the built-in tool
	result, err := tool.Run(ctx, args)
	if err != nil {
		return &mcpgo.CallToolResult{
			IsError: true,
			Content: []mcpgo.Content{
				mcpgo.TextContent{
					Type: "text",
					Text: err.Error(),
				},
			},
		}, nil
	}

	// Convert result to string
	var resultStr string
	switch v := result.(type) {
	case string:
		resultStr = v
	default:
		resultStr = fmt.Sprintf("%v", v)
	}

	return &mcpgo.CallToolResult{
		Content: []mcpgo.Content{
			mcpgo.TextContent{
				Type: "text",
				Text: resultStr,
			},
		},
	}, nil
}

// handleExternalMCPToolCall handles calls to external MCP tools
func (s *kubectlMCPServer) handleExternalMCPToolCall(ctx context.Context, request mcpgo.CallToolRequest) (*mcpgo.CallToolResult, error) {
	toolName := request.Params.Name

	// Find which server provides this tool
	serverTools, err := s.mcpManager.ListAvailableTools(ctx)
	if err != nil {
		return &mcpgo.CallToolResult{
			IsError: true,
			Content: []mcpgo.Content{
				mcpgo.TextContent{
					Type: "text",
					Text: fmt.Sprintf("failed to list available tools: %v", err),
				},
			},
		}, nil
	}

	var targetServerName string
	for serverName, tools := range serverTools {
		for _, tool := range tools {
			if tool.Name == toolName {
				targetServerName = serverName
				break
			}
		}
		if targetServerName != "" {
			break
		}
	}

	if targetServerName == "" {
		return &mcpgo.CallToolResult{
			IsError: true,
			Content: []mcpgo.Content{
				mcpgo.TextContent{
					Type: "text",
					Text: fmt.Sprintf("external MCP tool %q not found", toolName),
				},
			},
		}, nil
	}

	// Get the client for the target server
	client, exists := s.mcpManager.GetClient(targetServerName)
	if !exists {
		return &mcpgo.CallToolResult{
			IsError: true,
			Content: []mcpgo.Content{
				mcpgo.TextContent{
					Type: "text",
					Text: fmt.Sprintf("MCP client for server %q not found", targetServerName),
				},
			},
		}, nil
	}

	// Extract arguments - handle the args wrapper for external tools
	var toolArgs map[string]any
	if args, ok := request.Params.Arguments.(map[string]any); ok {
		if argsValue, hasArgs := args["args"]; hasArgs {
			if argsMap, ok := argsValue.(map[string]any); ok {
				toolArgs = argsMap
			} else {
				toolArgs = args // Fallback to using args directly
			}
		} else {
			toolArgs = args // Use arguments directly if no "args" wrapper
		}
	} else {
		return &mcpgo.CallToolResult{
			IsError: true,
			Content: []mcpgo.Content{
				mcpgo.TextContent{
					Type: "text",
					Text: fmt.Sprintf("invalid arguments type: expected map[string]any, got %T", request.Params.Arguments),
				},
			},
		}, nil
	}

	// Call the external MCP tool
	result, err := client.CallTool(ctx, toolName, toolArgs)
	if err != nil {
		return &mcpgo.CallToolResult{
			IsError: true,
			Content: []mcpgo.Content{
				mcpgo.TextContent{
					Type: "text",
					Text: fmt.Sprintf("error calling external MCP tool %q: %v", toolName, err),
				},
			},
		}, nil
	}

	// Return successful result
	return &mcpgo.CallToolResult{
		Content: []mcpgo.Content{
			mcpgo.TextContent{
				Type: "text",
				Text: result,
			},
		},
	}, nil
}
