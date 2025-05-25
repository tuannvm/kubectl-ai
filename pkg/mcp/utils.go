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
	"math"
	"time"

	"k8s.io/klog/v2"
)

// RetryConfig defines retry behavior for MCP operations
type RetryConfig struct {
	MaxRetries  int
	BaseDelay   time.Duration
	MaxDelay    time.Duration
	Multiplier  float64
	Description string
}

// DefaultRetryConfig returns a sensible default retry configuration
func DefaultRetryConfig(description string) RetryConfig {
	return RetryConfig{
		MaxRetries:  3,
		BaseDelay:   1 * time.Second,
		MaxDelay:    10 * time.Second,
		Multiplier:  2.0,
		Description: description,
	}
}

// RetryOperation executes an operation with exponential backoff retry
func RetryOperation(ctx context.Context, config RetryConfig, operation func() error) error {
	var lastErr error

	for attempt := 1; attempt <= config.MaxRetries; attempt++ {
		klog.V(3).InfoS("Attempting operation",
			"operation", config.Description,
			"attempt", attempt,
			"maxRetries", config.MaxRetries)

		if err := operation(); err == nil {
			if attempt > 1 {
				klog.V(2).InfoS("Operation succeeded after retry",
					"operation", config.Description,
					"attempt", attempt)
			}
			return nil
		} else {
			lastErr = err

			if attempt < config.MaxRetries {
				delay := calculateBackoffDelay(attempt, config)
				klog.V(3).InfoS("Operation failed, retrying",
					"operation", config.Description,
					"attempt", attempt,
					"error", err,
					"nextRetryIn", delay)

				select {
				case <-ctx.Done():
					return fmt.Errorf("operation cancelled: %w", ctx.Err())
				case <-time.After(delay):
					// Continue to next attempt
				}
			}
		}
	}

	return fmt.Errorf("operation failed after %d attempts: %w", config.MaxRetries, lastErr)
}

// calculateBackoffDelay calculates exponential backoff delay with jitter
func calculateBackoffDelay(attempt int, config RetryConfig) time.Duration {
	delay := float64(config.BaseDelay) * math.Pow(config.Multiplier, float64(attempt-1))

	if time.Duration(delay) > config.MaxDelay {
		return config.MaxDelay
	}

	return time.Duration(delay)
}

// SanitizeServerName ensures server names are valid identifiers
func SanitizeServerName(name string) string {
	// Simple sanitization - replace invalid characters
	result := ""
	for _, char := range name {
		if (char >= 'a' && char <= 'z') ||
			(char >= 'A' && char <= 'Z') ||
			(char >= '0' && char <= '9') ||
			char == '-' || char == '_' {
			result += string(char)
		} else {
			result += "_"
		}
	}

	if result == "" {
		result = "unnamed"
	}

	return result
}

// GroupToolsByServer groups tools by their server name for easier display
func GroupToolsByServer(tools map[string][]ToolInfo) map[string]int {
	summary := make(map[string]int)

	for serverName, serverTools := range tools {
		summary[serverName] = len(serverTools)
	}

	return summary
}
