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
	"k8s.io/klog/v2"
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

	// Handle type with validation
	schemaType, err := parseSchemaType(jsonSchema)
	if err != nil {
		klog.V(3).InfoS("Schema type parsing failed, using default", "error", err)
		schema.Type = gollm.TypeObject // Safe default
	} else {
		schema.Type = schemaType
	}

	// Handle description
	if desc, ok := jsonSchema["description"].(string); ok {
		schema.Description = desc
	}

	// Handle properties for object types
	if schema.Type == gollm.TypeObject {
		properties, required, err := parseObjectProperties(jsonSchema)
		if err != nil {
			klog.V(3).InfoS("Failed to parse object properties", "error", err)
			// Use fallback properties
			schema.Properties = createFallbackProperties()
		} else {
			schema.Properties = properties
			schema.Required = required
		}

		// Ensure we have at least one property for valid OpenAI schema
		if len(schema.Properties) == 0 {
			schema.Properties = createFallbackProperties()
		}
	}

	// Handle array items
	if schema.Type == gollm.TypeArray {
		if items, err := parseArrayItems(jsonSchema); err != nil {
			klog.V(3).InfoS("Failed to parse array items", "error", err)
		} else {
			schema.Items = items
		}
	}

	return schema, nil
}

// parseSchemaType extracts and validates the schema type
func parseSchemaType(jsonSchema map[string]interface{}) (gollm.SchemaType, error) {
	typeVal, ok := jsonSchema["type"]
	if !ok {
		return gollm.TypeObject, nil // Default to object if no type specified
	}

	typeStr, ok := typeVal.(string)
	if !ok {
		return "", fmt.Errorf("type field is not a string: %T", typeVal)
	}

	switch typeStr {
	case "object":
		return gollm.TypeObject, nil
	case "string":
		return gollm.TypeString, nil
	case "number", "integer":
		return gollm.TypeInteger, nil
	case "boolean":
		return gollm.TypeBoolean, nil
	case "array":
		return gollm.TypeArray, nil
	default:
		return "", fmt.Errorf("unsupported schema type: %s", typeStr)
	}
}

// parseObjectProperties parses object properties and required fields
func parseObjectProperties(jsonSchema map[string]interface{}) (map[string]*gollm.Schema, []string, error) {
	properties := make(map[string]*gollm.Schema)
	var required []string

	// Parse properties
	if props, ok := jsonSchema["properties"].(map[string]interface{}); ok {
		for propName, propSchema := range props {
			if propSchemaMap, ok := propSchema.(map[string]interface{}); ok {
				convertedProp, err := convertJSONSchemaToGollm(propSchemaMap)
				if err != nil {
					klog.V(3).InfoS("Failed to convert property schema", "property", propName, "error", err)
					// Continue with other properties instead of failing completely
					continue
				}
				properties[propName] = convertedProp
			} else {
				klog.V(3).InfoS("Property schema is not an object", "property", propName, "type", fmt.Sprintf("%T", propSchema))
			}
		}
	}

	// Parse required fields
	if requiredArray, ok := jsonSchema["required"].([]interface{}); ok {
		required = make([]string, 0, len(requiredArray))
		for _, req := range requiredArray {
			if reqStr, ok := req.(string); ok {
				required = append(required, reqStr)
			} else {
				klog.V(3).InfoS("Required field is not a string", "field", req, "type", fmt.Sprintf("%T", req))
			}
		}
	}

	return properties, required, nil
}

// parseArrayItems parses array item schema
func parseArrayItems(jsonSchema map[string]interface{}) (*gollm.Schema, error) {
	items, ok := jsonSchema["items"].(map[string]interface{})
	if !ok {
		return nil, fmt.Errorf("array items schema is not an object: %T", jsonSchema["items"])
	}

	return convertJSONSchemaToGollm(items)
}

// createFallbackProperties creates safe fallback properties for object schemas
func createFallbackProperties() map[string]*gollm.Schema {
	return map[string]*gollm.Schema{
		"input": {
			Type:        gollm.TypeString,
			Description: "Input for the tool",
		},
	}
}
