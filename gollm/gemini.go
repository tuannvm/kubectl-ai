// Copyright 2024 Google LLC
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

package gollm

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"iter"
	"net"
	"net/http"
	"os"
	"os/exec"
	"strings"

	"google.golang.org/genai"

	"k8s.io/klog/v2"
)

func init() {
	if err := RegisterProvider("gemini", geminiFactory); err != nil {
		klog.Fatalf("Failed to register gemini provider: %v", err)
	}
	if err := RegisterProvider("vertexai", vertexaiViaGeminiFactory); err != nil {
		klog.Fatalf("Failed to register vertexai provider: %v", err)
	}
}

// geminiFactory is the provider factory function for Gemini.
// Supports ClientOptions for consistency, but skipVerifySSL is not used.
func geminiFactory(ctx context.Context, opts ClientOptions) (Client, error) {
	opt := GeminiAPIClientOptions{}
	return NewGeminiAPIClient(ctx, opt)
}

// GeminiAPIClientOptions are the options for the Gemini API client.
type GeminiAPIClientOptions struct {
	// API Key for GenAI. Required for BackendGeminiAPI.
	APIKey string
}

// NewGeminiAPIClient builds a client for the Gemini API.
func NewGeminiAPIClient(ctx context.Context, opt GeminiAPIClientOptions) (*GoogleAIClient, error) {
	apiKey := opt.APIKey
	if apiKey == "" {
		apiKey = os.Getenv("GEMINI_API_KEY")
	}
	if apiKey == "" {
		return nil, fmt.Errorf("GEMINI_API_KEY environment variable not set")
	}
	cc := &genai.ClientConfig{
		APIKey:  apiKey,
		Backend: genai.BackendGeminiAPI,
	}

	client, err := genai.NewClient(ctx, cc)
	if err != nil {
		return nil, fmt.Errorf("building gemini client: %w", err)
	}

	return &GoogleAIClient{
		client: client,
	}, nil
}

// VertexAIClientOptions are the options for using the VertexAPI.
type VertexAIClientOptions struct {
	// GCP Project ID for Vertex AI. Required for BackendVertexAI.
	Project string
	// GCP Location/Region for Vertex AI. Required for BackendVertexAI. See https://cloud.google.com/vertex-ai/docs/general/locations
	Location string
}

// vertexaiViaGeminiFactory is the provider factory function for VertexAI via Gemini.
// Supports ClientOptions for consistency, but skipVerifySSL is not used.
func vertexaiViaGeminiFactory(ctx context.Context, opts ClientOptions) (Client, error) {
	opt := VertexAIClientOptions{}
	return NewVertexAIClient(ctx, opt)
}

// findDefaultGCPProject gets the default GCP project ID from gcloud
func findDefaultGCPProject(ctx context.Context) (string, error) {
	log := klog.FromContext(ctx)

	// First check env vars
	// GOOGLE_CLOUD_PROJECT is the default for the genai library and a GCP convention
	projectID := ""
	for _, env := range []string{"GOOGLE_CLOUD_PROJECT"} {
		if v := os.Getenv(env); v != "" {
			projectID = v
			log.Info("got project for vertex client from env var", "project", projectID, "env", env)
			return projectID, nil
		}
	}

	// Now check default project in gcloud
	{
		cmd := exec.CommandContext(ctx, "gcloud", "config", "get", "project")
		var stdout bytes.Buffer
		cmd.Stdout = &stdout
		if err := cmd.Run(); err != nil {
			return "", fmt.Errorf("cannot get project (using gcloud config get project): %w", err)
		}
		projectID = strings.TrimSpace(stdout.String())
		if projectID != "" {
			log.Info("got project from gcloud config", "project", projectID)
			return projectID, nil
		}
	}

	return "", fmt.Errorf("project was not set in gcloud config (or GOOGLE_CLOUD_PROJECT env var)")
}

// NewVertexAIClient builds a client for the vertexai API.
func NewVertexAIClient(ctx context.Context, opt VertexAIClientOptions) (*GoogleAIClient, error) {
	log := klog.FromContext(ctx)

	cc := &genai.ClientConfig{
		// Project ID is loaded from the GOOGLE_CLOUD_PROJECT environment variable
		// Location/Region is loaded from either GOOGLE_CLOUD_LOCATION or GOOGLE_CLOUD_REGION environment variable
		Backend:  genai.BackendVertexAI,
		Project:  opt.Project,
		Location: opt.Location,
	}

	// ProjectID is required
	if cc.Project == "" {
		projectID, err := findDefaultGCPProject(ctx)
		if err != nil {
			return nil, fmt.Errorf("finding default GCP project ID: %w", err)
		}
		cc.Project = projectID
	}

	// Location is also required
	if cc.Location == "" {
		location := ""

		// Check well-known env vars
		for _, env := range []string{"GOOGLE_CLOUD_LOCATION", "GOOGLE_CLOUD_REGION"} {
			if v := os.Getenv(env); v != "" {
				location = v
				log.Info("got location for vertex client from env var", "location", location, "env", env)
				break
			}
		}

		// Fallback to us-central1
		if location == "" {
			location = "us-central1"
			log.Info("defaulted location for vertex client", "location", opt.Location)
		}

		cc.Location = location
	}

	client, err := genai.NewClient(ctx, cc)
	if err != nil {
		return nil, fmt.Errorf("building gemini client: %w", err)
	}

	return &GoogleAIClient{
		client: client,
	}, nil
}

// GoogleAIClient is a client for the google AI APIs.
// It implements the Client interface.
type GoogleAIClient struct {
	client *genai.Client

	// responseSchema will constrain the output to match the given schema
	responseSchema *genai.Schema
}

var _ Client = &GoogleAIClient{}

// ListModels lists the models available in the Gemini API.
func (c *GoogleAIClient) ListModels(ctx context.Context) (modelNames []string, err error) {
	for model, err := range c.client.Models.All(ctx) {
		if err != nil {
			return nil, fmt.Errorf("error listing models: %w", err)
		}
		modelNames = append(modelNames, strings.TrimPrefix(model.Name, "models/"))
	}
	return modelNames, nil
}

// Close frees the resources used by the client.
func (c *GoogleAIClient) Close() error {
	return nil
}

// SetResponseSchema constrains LLM responses to match the provided schema.
// Calling with nil will clear the current schema.
func (c *GoogleAIClient) SetResponseSchema(responseSchema *Schema) error {
	if responseSchema == nil {
		c.responseSchema = nil
		return nil
	}

	geminiSchema, err := toGeminiSchema(responseSchema)
	if err != nil {
		return err
	}

	c.responseSchema = geminiSchema
	return nil
}

func (c *GoogleAIClient) GenerateCompletion(ctx context.Context, request *CompletionRequest) (CompletionResponse, error) {
	log := klog.FromContext(ctx)

	var config *genai.GenerateContentConfig

	if c.responseSchema != nil {
		config = &genai.GenerateContentConfig{
			ResponseSchema:   c.responseSchema,
			ResponseMIMEType: "application/json",
		}
	}

	content := []*genai.Content{
		{Role: "user", Parts: []*genai.Part{{Text: request.Prompt}}},
	}

	log.Info("sending GenerateContent request to gemini", "content", content)
	result, err := c.client.Models.GenerateContent(ctx, request.Model, content, config)
	if err != nil {
		return nil, err
	}

	return &GeminiCompletionResponse{geminiResponse: result, text: result.Text()}, nil
}

// StartChat starts a new chat with the model.
func (c *GoogleAIClient) StartChat(systemPrompt string, model string) Chat {
	// Some values that are recommended by aistudio
	temperature := float32(1.0)
	topK := float32(40)
	topP := float32(0.95)
	maxOutputTokens := int32(8192)

	chat := &GeminiChat{
		model:  model,
		client: c.client,
		genConfig: &genai.GenerateContentConfig{
			SystemInstruction: &genai.Content{
				Parts: []*genai.Part{
					{Text: systemPrompt},
				},
			},
			Temperature:      &temperature,
			TopK:             &topK,
			TopP:             &topP,
			MaxOutputTokens:  maxOutputTokens,
			ResponseMIMEType: "text/plain",
		},
		history: []*genai.Content{},
	}

	if chat.model == "gemma-3-27b-it" {
		// Note: gemma-3-27b-it does not allow system prompt
		// xref: https://discuss.ai.google.dev/t/gemma-3-missing-features-despite-announcement/71692
		// TODO: remove this hack once gemma-3-27b-it supports system prompt
		chat.genConfig.SystemInstruction = nil
		chat.history = []*genai.Content{
			{Role: "user", Parts: []*genai.Part{{Text: systemPrompt}}},
		}
	}

	if c.responseSchema != nil {
		chat.genConfig.ResponseSchema = c.responseSchema
		chat.genConfig.ResponseMIMEType = "application/json"
	}
	return chat
}

// GeminiChat is a chat with the model.
// It implements the Chat interface.
type GeminiChat struct {
	model     string
	client    *genai.Client
	history   []*genai.Content
	genConfig *genai.GenerateContentConfig
}

// SetFunctionDefinitions sets the function definitions for the chat.
// This allows the LLM to call user-defined functions.
func (c *GeminiChat) SetFunctionDefinitions(functionDefinitions []*FunctionDefinition) error {
	var genaiFunctionDeclarations []*genai.FunctionDeclaration
	for _, functionDefinition := range functionDefinitions {
		parameters, err := toGeminiSchema(functionDefinition.Parameters)
		if err != nil {
			return err
		}
		genaiFunctionDeclarations = append(genaiFunctionDeclarations, &genai.FunctionDeclaration{
			Name:        functionDefinition.Name,
			Description: functionDefinition.Description,
			Parameters:  parameters,
		})
	}
	c.genConfig.Tools = []*genai.Tool{
		{
			FunctionDeclarations: genaiFunctionDeclarations,
		},
	}
	return nil
}

// toGeminiSchema converts our generic Schema to a genai.Schema
func toGeminiSchema(schema *Schema) (*genai.Schema, error) {
	ret := &genai.Schema{
		Description: schema.Description,
		Required:    schema.Required,
	}

	switch schema.Type {
	case TypeObject:
		ret.Type = genai.TypeObject
	case TypeString:
		ret.Type = genai.TypeString
	case TypeBoolean:
		ret.Type = genai.TypeBoolean
	case TypeInteger:
		ret.Type = genai.TypeInteger
	case TypeArray:
		ret.Type = genai.TypeArray
	default:
		return nil, fmt.Errorf("type %q not handled by genai.Schema", schema.Type)
	}
	if schema.Properties != nil {
		ret.Properties = make(map[string]*genai.Schema)
		for k, v := range schema.Properties {
			geminiValue, err := toGeminiSchema(v)
			if err != nil {
				return nil, err
			}
			ret.Properties[k] = geminiValue
		}
	}
	if schema.Items != nil {
		geminiValue, err := toGeminiSchema(schema.Items)
		if err != nil {
			return nil, err
		}
		ret.Items = geminiValue
	}
	return ret, nil
}

func (c *GeminiChat) partsToGemini(contents ...any) ([]*genai.Part, error) {
	var parts []*genai.Part

	for _, content := range contents {
		switch v := content.(type) {
		case string:
			parts = append(parts, genai.NewPartFromText(v))
		case FunctionCallResult:
			parts = append(parts, &genai.Part{
				FunctionResponse: &genai.FunctionResponse{
					ID:       v.ID,
					Name:     v.Name,
					Response: v.Result,
				},
			})
		default:
			return nil, fmt.Errorf("unexpected type of content: %T", content)
		}
	}
	return parts, nil
}

// Send sends a message to the model.
// It returns a ChatResponse object containing the response from the model.
func (c *GeminiChat) Send(ctx context.Context, contents ...any) (ChatResponse, error) {
	log := klog.FromContext(ctx)
	log.V(1).Info("sending LLM request", "user", contents)

	parts, err := c.partsToGemini(contents...)
	if err != nil {
		return nil, err
	}

	genaiContent := &genai.Content{
		Role:  "user",
		Parts: parts,
	}

	c.history = append(c.history, genaiContent)
	result, err := c.client.Models.GenerateContent(ctx, c.model, c.history, c.genConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to generate content: %w", err)
	}
	if result == nil || len(result.Candidates) == 0 {
		return nil, fmt.Errorf("no response from Gemini")
	}
	c.history = append(c.history, result.Candidates[0].Content)
	geminiResponse := result
	log.V(1).Info("got LLM response", "response", geminiResponse)
	return &GeminiChatResponse{geminiResponse: geminiResponse}, nil
}

func (c *GeminiChat) SendStreaming(ctx context.Context, contents ...any) (ChatResponseIterator, error) {
	log := klog.FromContext(ctx)
	log.V(1).Info("sending LLM streaming request", "user", contents)

	parts, err := c.partsToGemini(contents...)
	if err != nil {
		return nil, err
	}

	genaiContent := &genai.Content{
		Role:  "user",
		Parts: parts,
	}

	c.history = append(c.history, genaiContent)
	stream := c.client.Models.GenerateContentStream(ctx, c.model, c.history, c.genConfig)

	return func(yield func(ChatResponse, error) bool) {
		next, stop := iter.Pull2(stream)
		defer stop()
		for {
			geminiResponse, err, ok := next()
			if !ok {
				return
			}

			var response *GeminiChatResponse
			if geminiResponse != nil {
				response = &GeminiChatResponse{geminiResponse: geminiResponse}

				if len(geminiResponse.Candidates) > 0 {
					// TODO: Should we try to coalesce parts when we have a streaming response?
					c.history = append(c.history, geminiResponse.Candidates[0].Content)
				}
			}

			if !yield(response, err) {
				return
			}
		}
	}, nil
}

// GeminiChatResponse is a response from the Gemini API.
// It implements the ChatResponse interface.
type GeminiChatResponse struct {
	geminiResponse *genai.GenerateContentResponse
}

var _ ChatResponse = &GeminiChatResponse{}

func (r *GeminiChatResponse) MarshalJSON() ([]byte, error) {
	formatted := RecordChatResponse{
		Raw: r.geminiResponse,
	}
	return json.Marshal(&formatted)
}

// String returns a string representation of the response.
func (r *GeminiChatResponse) String() string {
	return r.geminiResponse.Text()
}

// UsageMetadata returns the usage metadata for the response.
func (r *GeminiChatResponse) UsageMetadata() any {
	return r.geminiResponse.UsageMetadata
}

// Candidates returns the candidates for the response.
func (r *GeminiChatResponse) Candidates() []Candidate {
	var candidates []Candidate
	for _, candidate := range r.geminiResponse.Candidates {
		candidates = append(candidates, &GeminiCandidate{candidate: candidate})
	}
	return candidates
}

// GeminiCandidate is a candidate for the response.
// It implements the Candidate interface.
type GeminiCandidate struct {
	candidate *genai.Candidate
}

// String returns a string representation of the response.
func (r *GeminiCandidate) String() string {
	var response strings.Builder
	response.WriteString("[")
	for i, parts := range r.Parts() {
		if i > 0 {
			response.WriteString(", ")
		}
		text, ok := parts.AsText()
		if ok {
			response.WriteString(text)
		}
		functionCalls, ok := parts.AsFunctionCalls()
		if ok {
			response.WriteString("functionCalls=[")
			for _, functionCall := range functionCalls {
				response.WriteString(fmt.Sprintf("%q(args=%v)", functionCall.Name, functionCall.Arguments))
			}
			response.WriteString("]}")
		}
	}
	response.WriteString("]}")
	return response.String()
}

// Parts returns the parts of the candidate.
func (r *GeminiCandidate) Parts() []Part {
	var parts []Part
	if r.candidate.Content != nil {
		for _, part := range r.candidate.Content.Parts {
			parts = append(parts, &GeminiPart{part: *part})
		}
	}
	return parts
}

// GeminiPart is a part of a candidate.
// It implements the Part interface.
type GeminiPart struct {
	part genai.Part
}

// AsText returns the text of the part.
func (p *GeminiPart) AsText() (string, bool) {
	if p.part.Text != "" {
		return p.part.Text, true
	}
	return "", false
}

// AsFunctionCalls returns the function calls of the part.
func (p *GeminiPart) AsFunctionCalls() ([]FunctionCall, bool) {
	if p.part.FunctionCall != nil {
		return []FunctionCall{
			{
				ID:        p.part.FunctionCall.ID,
				Name:      p.part.FunctionCall.Name,
				Arguments: p.part.FunctionCall.Args,
			},
		}, true
	}
	return nil, false
}

type GeminiCompletionResponse struct {
	geminiResponse *genai.GenerateContentResponse
	text           string
}

var _ CompletionResponse = &GeminiCompletionResponse{}

func (r *GeminiCompletionResponse) MarshalJSON() ([]byte, error) {
	formatted := RecordCompletionResponse{
		Text: r.text,
		Raw:  r.geminiResponse,
	}
	return json.Marshal(&formatted)
}

func (r *GeminiCompletionResponse) Response() string {
	return r.text
}

func (r *GeminiCompletionResponse) UsageMetadata() any {
	return r.geminiResponse.UsageMetadata
}

func (r *GeminiCompletionResponse) String() string {
	return fmt.Sprintf("{text=%q}", r.text)
}

func (c *GeminiChat) IsRetryableError(err error) bool {
	if err == nil {
		return false
	}

	var apiErr genai.APIError
	if errors.As(err, &apiErr) {
		switch apiErr.Code {
		case http.StatusConflict, http.StatusTooManyRequests,
			http.StatusInternalServerError, http.StatusBadGateway,
			http.StatusServiceUnavailable, http.StatusGatewayTimeout:
			return true
		default:
			return false
		}
	}

	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		return true
	}

	// Add other error checks specific to LLM clients if needed
	// e.g., if errors.Is(err, specificLLMRateLimitError) { return true }

	return false
}
