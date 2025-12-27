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

type AIClient interface {
	SendChatRequest(req *domain.ChatRequest, chatID string) (*http.Response, error)
}

func ChatCompletions(cfg *config.Config, client AIClient, tokenizer utils.Tokener) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req domain.ChatRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeErr(w, http.StatusBadRequest, "invalid json")
			return
		}

		if err := validator.Validate(&req); err != nil {
			writeErr(w, http.StatusBadRequest, err.Error())
			return
		}

		if req.Model == "" {
			req.Model = cfg.Model.Default
		}

		chatID := utils.GenerateRequestID()

		logger.Info().
			Str("model", req.Model).
			Bool("stream", req.Stream).
			Int("messages", len(req.Messages)).
			Msg("chat request")

		resp, err := client.SendChatRequest(&req, chatID)
		if err != nil {
			logger.Error().Err(err).Msg("request failed")
			writeErr(w, http.StatusInternalServerError, "failed to process request")
			return
		}

		if req.Stream {
			streamResponse(w, resp, &req, cfg, tokenizer)
		} else {
			nonStreamResponse(w, resp, &req, cfg, tokenizer)
		}
	}
}

func streamResponse(w http.ResponseWriter, resp *http.Response, req *domain.ChatRequest, cfg *config.Config, tokenizer utils.Tokener) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeErr(w, http.StatusInternalServerError, "streaming not supported")
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	var parts []string
	includeUsage := req.StreamOpts != nil && req.StreamOpts.IncludeUsage

	promptTokens := 0
	if includeUsage {
		promptTokens = zlm.CountTokens(req.Messages, tokenizer)
	}

	for zaiResp := range zlm.ParseSSEStream(resp) {
		delta := zlm.FormatResponse(zaiResp, cfg)
		if delta == nil {
			continue
		}

		if includeUsage {
			if c, ok := delta["content"].(string); ok {
				parts = append(parts, c)
			}
			if r, ok := delta["reasoning_content"].(string); ok {
				parts = append(parts, r)
			}
		}

		msg := &domain.ResponseMessage{
			Role:             getStr(delta, "role"),
			Content:          getStr(delta, "content"),
			ReasoningContent: getStr(delta, "reasoning_content"),
			ToolCall:         getStr(delta, "tool_call"),
		}

		chunk := domain.ChatResponse{
			ID:      utils.GenerateChatCompletionID(),
			Object:  "chat.completion.chunk",
			Created: time.Now().Unix(),
			Model:   req.Model,
			Choices: []domain.Choice{{Index: 0, Delta: msg}},
		}

		data, _ := json.Marshal(chunk)
		fmt.Fprintf(w, "data: %s\n\n", data)
		flusher.Flush()
	}

	// stop chunk
	stop := domain.ChatResponse{
		ID:      utils.GenerateChatCompletionID(),
		Object:  "chat.completion.chunk",
		Created: time.Now().Unix(),
		Model:   req.Model,
		Choices: []domain.Choice{{
			Index:        0,
			Delta:        &domain.ResponseMessage{Role: "assistant"},
			FinishReason: strPtr("stop"),
		}},
	}
	data, _ := json.Marshal(stop)
	fmt.Fprintf(w, "data: %s\n\n", data)
	flusher.Flush()

	// usage chunk
	if includeUsage {
		text := strings.Join(parts, "")
		completionTokens := tokenizer.Count(text)

		usage := domain.ChatResponse{
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
		data, _ := json.Marshal(usage)
		fmt.Fprintf(w, "data: %s\n\n", data)
		flusher.Flush()
	}

	fmt.Fprintf(w, "data: [DONE]\n\n")
	flusher.Flush()
}

func nonStreamResponse(w http.ResponseWriter, resp *http.Response, req *domain.ChatRequest, cfg *config.Config, tokenizer utils.Tokener) {
	var contentParts []string
	var reasoningParts []string

	for zaiResp := range zlm.ParseSSEStream(resp) {
		delta := zlm.FormatResponse(zaiResp, cfg)
		if delta == nil {
			continue
		}

		if c, ok := delta["content"].(string); ok {
			contentParts = append(contentParts, c)
		}
		if r, ok := delta["reasoning_content"].(string); ok {
			reasoningParts = append(reasoningParts, r)
		}

		if zaiResp.Data != nil && zaiResp.Data.Done {
			break
		}
	}

	msg := &domain.ResponseMessage{Role: "assistant"}

	completionText := ""
	if len(reasoningParts) > 0 {
		reasoning := strings.Join(reasoningParts, "")
		msg.ReasoningContent = reasoning
		completionText += reasoning
	}
	if len(contentParts) > 0 {
		content := strings.Join(contentParts, "")
		msg.Content = content
		completionText += content
	}

	response := domain.ChatResponse{
		ID:      utils.GenerateChatCompletionID(),
		Object:  "chat.completion",
		Created: time.Now().Unix(),
		Model:   req.Model,
		Choices: []domain.Choice{{
			Index:        0,
			Message:      msg,
			FinishReason: strPtr("stop"),
		}},
	}

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

func ListModels(cfg *config.Config) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		url := fmt.Sprintf("%s//%s/api/models", cfg.Upstream.Protocol, cfg.Upstream.Host)

		req, err := http.NewRequest("GET", url, nil)
		if err != nil {
			writeErr(w, http.StatusInternalServerError, "failed to create request")
			return
		}

		for k, v := range cfg.GetUpstreamHeaders() {
			req.Header.Set(k, v)
		}
		req.Header.Set("Authorization", "Bearer "+cfg.Upstream.Token)

		client := &http.Client{Timeout: 10 * time.Second}
		resp, err := client.Do(req)
		if err != nil {
			logger.Error().Err(err).Msg("models request failed")
			writeErr(w, http.StatusBadGateway, "upstream unavailable")
			return
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			writeErr(w, resp.StatusCode, "upstream error")
			return
		}

		var upstream struct {
			Data []struct {
				ID   string `json:"id"`
				Name string `json:"name"`
			} `json:"data"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&upstream); err != nil {
			writeErr(w, http.StatusInternalServerError, "failed to parse models")
			return
		}

		models := make([]map[string]interface{}, 0, len(upstream.Data))
		for _, m := range upstream.Data {
			models = append(models, map[string]interface{}{
				"id":       m.ID,
				"object":   "model",
				"created":  time.Now().Unix(),
				"owned_by": "zhipu",
			})
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"object": "list",
			"data":   models,
		})
	}
}

func getStr(m map[string]interface{}, key string) string {
	if v, ok := m[key]; ok {
		if s, ok := v.(string); ok {
			return s
		}
	}
	return ""
}

func writeErr(w http.ResponseWriter, code int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(domain.NewUpstreamError(code, msg))
}

func strPtr(s string) *string {
	return &s
}
