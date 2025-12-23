package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/zarazaex69/mo/internal/config"
	"github.com/zarazaex69/mo/internal/domain"
	"github.com/zarazaex69/mo/internal/pkg/logger"
	"github.com/zarazaex69/mo/internal/pkg/utils"
	"github.com/zarazaex69/mo/internal/pkg/validator"
	"github.com/zarazaex69/mo/internal/provider/zlm"
)

// Define AIClienter interface here for now, or use zlm.Client directly
// Using zlm.Client directly for simplification as per instruction to port zlm logic.
// If multi-provider needed, we would use an interface.
type AIClienter interface {
	SendChatRequest(req *domain.ChatRequest, chatID string) (*http.Response, error)
}

// ChatCompletions orchestrates the request lifecycle for chat interactions, validating inputs,
// dispatching to the AI provider, and managing the response stream or synchronization.
// It serves as the primary entry point for the /v1/chat/completions endpoint.
func ChatCompletions(cfg *config.Config, aiClient AIClienter, tokenizer utils.Tokener) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// Deserialize the incoming JSON payload into the domain model.
		var req domain.ChatRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, "Invalid JSON request")
			return
		}

		// Enforce strict validation rules (required fields, range checks) to fail fast on invalid inputs.
		if err := validator.Validate(&req); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}

		// Apply configuration defaults to ensure predictable behavior when optional fields are omitted.
		if req.Model == "" {
			req.Model = cfg.Model.Default
		}

		// optimized tracking ID for distributed tracing across services.
		chatID := utils.GenerateRequestID()

		logger.Info().
			Str("model", req.Model).
			Bool("stream", req.Stream).
			Int("messages", len(req.Messages)).
			Msg("Processing chat completion request")

		// Delegate the heavy lifting to the AI provider client, abstracting the upstream communication.
		resp, err := aiClient.SendChatRequest(&req, chatID)
		if err != nil {
			logger.Error().Err(err).Msg("Failed to send chat request")
			writeError(w, http.StatusInternalServerError, "Failed to process request")
			return
		}

		// Branch execution based on whether the client requested a streaming response or a single atomic response.
		if req.Stream {
			handleStreamingResponse(w, resp, &req, cfg, tokenizer)
		} else {
			handleNonStreamingResponse(w, resp, &req, cfg, tokenizer)
		}
	}
}

func handleStreamingResponse(w http.ResponseWriter, resp *http.Response, req *domain.ChatRequest, cfg *config.Config, tokenizer utils.Tokener) {
	// Verify that the ResponseWriter supports flushing, which is essential for SSE to work correctly.
	// Without this, the client would not receive events in real-time.
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeError(w, http.StatusInternalServerError, "Streaming not supported")
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	// Accumulate content parts to calculate total token usage at the end of the stream if requested.
	var contentParts []string
	includeUsage := req.StreamOpts != nil && req.StreamOpts.IncludeUsage

	// Pre-calculate prompt tokens to avoid recalculating on every chunk.
	promptTokens := 0
	if includeUsage {
		promptTokens = zlm.CountTokens(req.Messages, tokenizer)
	}

	// Iterate over the parsed SSE stream from the provider.
	for zaiResp := range zlm.ParseSSEStream(resp) {
		delta := zlm.FormatResponse(zaiResp, cfg)
		if delta == nil {
			continue
		}

		// Aggregate content for usage reporting.
		if includeUsage {
			if content, ok := delta["content"].(string); ok {
				contentParts = append(contentParts, content)
			}
			if reasoningContent, ok := delta["reasoning_content"].(string); ok {
				contentParts = append(contentParts, reasoningContent)
			}
		}

		// Construct the chunk payload adhering to the OpenAI delta format.
		deltaResponse := &domain.ResponseMessage{
			Role:             getStringFromMap(delta, "role"),
			Content:          getStringFromMap(delta, "content"),
			ReasoningContent: getStringFromMap(delta, "reasoning_content"),
			ToolCall:         getStringFromMap(delta, "tool_call"),
		}

		streamChunk := domain.ChatResponse{
			ID:      utils.GenerateChatCompletionID(),
			Object:  "chat.completion.chunk",
			Created: time.Now().Unix(),
			Model:   req.Model,
			Choices: []domain.Choice{
				{
					Index: 0,
					Delta: deltaResponse,
				},
			},
		}

		chunkJSON, _ := json.Marshal(streamChunk)
		fmt.Fprintf(w, "data: %s\n\n", chunkJSON)
		flusher.Flush()
	}

	// Emit the final 'stop' chunk to signal standard completion to the client.
	finishChunk := domain.ChatResponse{
		ID:      utils.GenerateChatCompletionID(),
		Object:  "chat.completion.chunk",
		Created: time.Now().Unix(),
		Model:   req.Model,
		Choices: []domain.Choice{
			{
				Index:        0,
				Delta:        &domain.ResponseMessage{Role: "assistant"},
				FinishReason: stringPtr("stop"),
			},
		},
	}
	finishJSON, _ := json.Marshal(finishChunk)
	fmt.Fprintf(w, "data: %s\n\n", finishJSON)
	flusher.Flush()

	// Append usage statistics as a final event if the client opted in.
	// This mirrors the behavior of newer OpenAI API versions.
	if includeUsage {
		completionText := strings.Join(contentParts, "")
		completionTokens := tokenizer.Count(completionText)

		usageChunk := domain.ChatResponse{
			ID:      utils.GenerateChatCompletionID(),
			Object:  "chat.completion.chunk",
			Created: time.Now().Unix(),
			Model:   req.Model,
			Choices: []domain.Choice{},
			Usage: &domain.Usage{
				PromptTokens:     promptTokens,
				CompletionTokens: completionTokens,
				TotalTokens:      promptTokens + completionTokens,
			},
		}
		usageJSON, _ := json.Marshal(usageChunk)
		fmt.Fprintf(w, "data: %s\n\n", usageJSON)
		flusher.Flush()
	}

	// Terminate the SSE stream with the standard [DONE] marker.
	fmt.Fprintf(w, "data: [DONE]\n\n")
	flusher.Flush()
}

func handleNonStreamingResponse(w http.ResponseWriter, resp *http.Response, req *domain.ChatRequest, cfg *config.Config, tokenizer utils.Tokener) {
	var contentParts []string
	var reasoningParts []string

	// Consume the entire stream to aggregate the full response object.
	// Even for non-streaming requests, we parse the upstream SSE to construct the final JSON.
	for zaiResp := range zlm.ParseSSEStream(resp) {
		if zaiResp.Data != nil && zaiResp.Data.Done {
			break
		}

		delta := zlm.FormatResponse(zaiResp, cfg)
		if delta == nil {
			continue
		}

		if content, ok := delta["content"].(string); ok {
			contentParts = append(contentParts, content)
		}
		if reasoningContent, ok := delta["reasoning_content"].(string); ok {
			reasoningParts = append(reasoningParts, reasoningContent)
		}
	}

	// Reconstruct the final message from aggregated parts.
	finalMessage := &domain.ResponseMessage{
		Role: "assistant",
	}

	completionText := ""
	if len(reasoningParts) > 0 {
		reasoning := strings.Join(reasoningParts, "")
		finalMessage.ReasoningContent = reasoning
		completionText += reasoning
	}
	if len(contentParts) > 0 {
		content := strings.Join(contentParts, "")
		finalMessage.Content = content
		completionText += content
	}

	// Build response
	response := domain.ChatResponse{
		ID:      utils.GenerateChatCompletionID(),
		Object:  "chat.completion",
		Created: time.Now().Unix(),
		Model:   req.Model,
		Choices: []domain.Choice{
			{
				Index:        0,
				Message:      finalMessage,
				FinishReason: stringPtr("stop"),
			},
		},
	}

	// Add usage
	promptTokens := zlm.CountTokens(req.Messages, tokenizer)
	completionTokens := tokenizer.Count(completionText)
	response.Usage = &domain.Usage{
		PromptTokens:     promptTokens,
		CompletionTokens: completionTokens,
		TotalTokens:      promptTokens + completionTokens,
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}

func getStringFromMap(m map[string]interface{}, key string) string {
	if v, ok := m[key]; ok {
		if s, ok := v.(string); ok {
			return s
		}
	}
	return ""
}

func writeError(w http.ResponseWriter, code int, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(domain.NewUpstreamError(code, message))
}

func stringPtr(s string) *string {
	return &s
}
