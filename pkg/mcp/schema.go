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
	"encoding/json"
	"fmt"

	"github.com/GoogleCloudPlatform/kubectl-ai/gollm"
	mcplib "github.com/mark3labs/mcp-go/mcp"
)

// ConvertToolToGollm converts an MCP tool to gollm.FunctionDefinition
func ConvertToolToGollm(mcpTool *Tool) (*gollm.FunctionDefinition, error) {
	functionDef := &gollm.FunctionDefinition{
		Name:        mcpTool.Name,
		Description: mcpTool.Description,
	}

	// Since MCP Tool struct doesn't include schema information,
	// create a minimal valid schema that accepts any parameters
	functionDef.Parameters = &gollm.Schema{
		Type: gollm.TypeObject,
		Properties: map[string]*gollm.Schema{
			"arguments": {
				Type:        gollm.TypeObject,
				Description: "Arguments for the MCP tool",
				Properties:  map[string]*gollm.Schema{},
			},
		},
	}

	return functionDef, nil
}

// ConvertMCPSchemaToGollm converts an MCP ToolInputSchema to gollm.Schema
func ConvertMCPSchemaToGollm(mcpSchema *mcplib.ToolInputSchema) (*gollm.Schema, error) {
	if mcpSchema == nil {
		return &gollm.Schema{
			Type: gollm.TypeObject,
			Properties: map[string]*gollm.Schema{
				"input": {
					Type:        gollm.TypeString,
					Description: "Input for the tool",
				},
			},
		}, nil
	}

	// Convert the MCP schema (which is JSON Schema) to our gollm schema
	// Since MCP uses JSON Schema format, we can try to convert it

	// First, try to marshal and unmarshal to handle the conversion
	schemaBytes, err := json.Marshal(mcpSchema)
	if err != nil {
		return nil, fmt.Errorf("marshaling MCP schema: %w", err)
	}

	// Parse the JSON schema
	var jsonSchema map[string]interface{}
	if err := json.Unmarshal(schemaBytes, &jsonSchema); err != nil {
		return nil, fmt.Errorf("unmarshaling MCP schema: %w", err)
	}

	// Convert JSON schema to gollm schema
	gollmSchema, err := convertJSONSchemaToGollm(jsonSchema)
	if err != nil {
		return nil, fmt.Errorf("converting JSON schema to gollm: %w", err)
	}

	return gollmSchema, nil
}

// convertJSONSchemaToGollm converts a JSON schema map to gollm.Schema
func convertJSONSchemaToGollm(jsonSchema map[string]interface{}) (*gollm.Schema, error) {
	schema := &gollm.Schema{}

	// Handle type
	if typeVal, ok := jsonSchema["type"]; ok {
		switch typeVal {
		case "object":
			schema.Type = gollm.TypeObject
		case "string":
			schema.Type = gollm.TypeString
		case "number", "integer":
			schema.Type = gollm.TypeInteger
		case "boolean":
			schema.Type = gollm.TypeBoolean
		case "array":
			schema.Type = gollm.TypeArray
		default:
			schema.Type = gollm.TypeString // Default fallback
		}
	} else {
		schema.Type = gollm.TypeObject // Default to object if no type specified
	}

	// Handle description
	if desc, ok := jsonSchema["description"].(string); ok {
		schema.Description = desc
	}

	// Handle properties for object types
	if schema.Type == gollm.TypeObject {
		if props, ok := jsonSchema["properties"].(map[string]interface{}); ok {
			schema.Properties = make(map[string]*gollm.Schema)
			for propName, propSchema := range props {
				if propSchemaMap, ok := propSchema.(map[string]interface{}); ok {
					convertedProp, err := convertJSONSchemaToGollm(propSchemaMap)
					if err != nil {
						// Log error but continue with other properties
						continue
					}
					schema.Properties[propName] = convertedProp
				}
			}
		}

		// Handle required fields
		if required, ok := jsonSchema["required"].([]interface{}); ok {
			schema.Required = make([]string, len(required))
			for i, req := range required {
				if reqStr, ok := req.(string); ok {
					schema.Required[i] = reqStr
				}
			}
		}

		// Ensure we have at least one property for valid OpenAI schema
		if len(schema.Properties) == 0 {
			schema.Properties = map[string]*gollm.Schema{
				"input": {
					Type:        gollm.TypeString,
					Description: "Input for the tool",
				},
			}
		}
	}

	// Handle array items
	if schema.Type == gollm.TypeArray {
		if items, ok := jsonSchema["items"].(map[string]interface{}); ok {
			itemSchema, err := convertJSONSchemaToGollm(items)
			if err == nil {
				schema.Items = itemSchema
			}
		}
	}

	return schema, nil
}
