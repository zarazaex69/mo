package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/zarazaex69/mo/internal/config"
	"github.com/zarazaex69/mo/internal/domain"
	"github.com/zarazaex69/mo/internal/pkg/browser"
	"github.com/zarazaex69/mo/internal/pkg/crypto"
	"github.com/zarazaex69/mo/internal/pkg/logger"
	"github.com/zarazaex69/mo/internal/pkg/tempmail"
	"github.com/zarazaex69/mo/internal/pkg/tokenstore"
	"github.com/zarazaex69/mo/internal/pkg/utils"
	"github.com/zarazaex69/mo/internal/pkg/validator"
	"github.com/zarazaex69/mo/internal/provider"
	"github.com/zarazaex69/mo/internal/provider/qwen"
	"github.com/zarazaex69/mo/internal/provider/zlm"
)

func ChatCompletions(cfg *config.Config, providers []provider.Provider, tokenizer utils.Tokener) http.HandlerFunc {
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

		var p provider.Provider
		for _, prov := range providers {
			if prov.SupportsModel(req.Model) {
				p = prov
				break
			}
		}

		if p == nil {
			writeErr(w, http.StatusBadRequest, "unsupported model")
			return
		}

		chatID := utils.GenerateRequestID()

		logger.Info().
			Str("provider", p.Name()).
			Str("model", req.Model).
			Bool("stream", req.Stream).
			Int("messages", len(req.Messages)).
			Msg("chat request")

		resp, err := p.SendChatRequest(&req, chatID)
		if err != nil {
			logger.Error().Err(err).Msg("request failed")
			writeErr(w, http.StatusInternalServerError, "failed to process request")
			return
		}

		switch p.Name() {
		case "qwen":
			if req.Stream {
				qwenStreamResponse(w, resp, &req, tokenizer)
			} else {
				qwenNonStreamResponse(w, resp, &req, tokenizer)
			}
		default:
			if req.Stream {
				zlmStreamResponse(w, resp, &req, cfg, tokenizer)
			} else {
				zlmNonStreamResponse(w, resp, &req, cfg, tokenizer)
			}
		}
	}
}

func zlmStreamResponse(w http.ResponseWriter, resp *http.Response, req *domain.ChatRequest, cfg *config.Config, tokenizer utils.Tokener) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeErr(w, http.StatusInternalServerError, "streaming not supported")
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	var parts []string
	var toolCallBuffer string
	var pendingToolCall *domain.ToolCall
	includeUsage := req.StreamOpts != nil && req.StreamOpts.IncludeUsage

	promptTokens := 0
	if includeUsage {
		promptTokens = zlm.CountTokens(req.Messages, tokenizer)
	}

	fmtr := zlm.NewFormatter(cfg)
	for zaiResp := range zlm.ParseSSEStream(resp) {
		delta := fmtr.Format(zaiResp)
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

		if tc, ok := delta["tool_call"].(string); ok {
			toolCallBuffer += tc

			if parsed := zlm.ParseToolCall(toolCallBuffer); parsed != nil {
				pendingToolCall = parsed

				chunk := domain.ChatResponse{
					ID:      utils.GenerateChatCompletionID(),
					Object:  "chat.completion.chunk",
					Created: time.Now().Unix(),
					Model:   req.Model,
					Choices: []domain.Choice{{
						Index: 0,
						Delta: &domain.ResponseMessage{
							Role:      "assistant",
							ToolCalls: []domain.ToolCall{*parsed},
						},
					}},
				}
				data, _ := json.Marshal(chunk)
				fmt.Fprintf(w, "data: %s\n\n", data)
				flusher.Flush()

				toolCallBuffer = ""
			}
			continue
		}

		content := getStr(delta, "content")
		if content != "" {
			content = zlm.StripToolCallBlock(content)
		}

		msg := &domain.ResponseMessage{
			Role:             getStr(delta, "role"),
			Content:          content,
			ReasoningContent: getStr(delta, "reasoning_content"),
		}

		if msg.Content == "" && msg.ReasoningContent == "" && msg.Role == "" {
			continue
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

	finishReason := "stop"
	if pendingToolCall != nil {
		finishReason = "tool_calls"
	}

	stop := domain.ChatResponse{
		ID:      utils.GenerateChatCompletionID(),
		Object:  "chat.completion.chunk",
		Created: time.Now().Unix(),
		Model:   req.Model,
		Choices: []domain.Choice{{
			Index:        0,
			Delta:        &domain.ResponseMessage{Role: "assistant"},
			FinishReason: strPtr(finishReason),
		}},
	}
	data, _ := json.Marshal(stop)
	fmt.Fprintf(w, "data: %s\n\n", data)
	flusher.Flush()

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

func zlmNonStreamResponse(w http.ResponseWriter, resp *http.Response, req *domain.ChatRequest, cfg *config.Config, tokenizer utils.Tokener) {
	var contentParts []string
	var reasoningParts []string
	var toolCallBuffer string
	var toolCalls []domain.ToolCall

	fmtr := zlm.NewFormatter(cfg)
	for zaiResp := range zlm.ParseSSEStream(resp) {
		delta := fmtr.Format(zaiResp)
		if delta == nil {
			continue
		}

		if c, ok := delta["content"].(string); ok {
			c = zlm.StripToolCallBlock(c)
			if c != "" {
				contentParts = append(contentParts, c)
			}
		}
		if r, ok := delta["reasoning_content"].(string); ok {
			reasoningParts = append(reasoningParts, r)
		}
		if tc, ok := delta["tool_call"].(string); ok {
			toolCallBuffer += tc
		}

		if zaiResp.Data != nil && zaiResp.Data.Done {
			break
		}
	}

	if toolCallBuffer != "" {
		if parsed := zlm.ParseToolCall(toolCallBuffer); parsed != nil {
			toolCalls = append(toolCalls, *parsed)
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
	if len(toolCalls) > 0 {
		msg.ToolCalls = toolCalls
		msg.Content = ""
	}

	finishReason := "stop"
	if len(toolCalls) > 0 {
		finishReason = "tool_calls"
	}

	response := domain.ChatResponse{
		ID:      utils.GenerateChatCompletionID(),
		Object:  "chat.completion",
		Created: time.Now().Unix(),
		Model:   req.Model,
		Choices: []domain.Choice{{
			Index:        0,
			Message:      msg,
			FinishReason: strPtr(finishReason),
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

func qwenStreamResponse(w http.ResponseWriter, resp *http.Response, req *domain.ChatRequest, tokenizer utils.Tokener) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeErr(w, http.StatusInternalServerError, "streaming not supported")
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	var parts []string
	var lastFinishReason string
	includeUsage := req.StreamOpts != nil && req.StreamOpts.IncludeUsage

	for qwenResp := range qwen.ParseSSEStream(resp) {
		if len(qwenResp.Choices) == 0 {
			continue
		}

		choice := qwenResp.Choices[0]
		if choice.Delta == nil {
			if choice.FinishReason != nil {
				lastFinishReason = *choice.FinishReason
			}
			continue
		}

		if includeUsage && choice.Delta.Content != "" {
			parts = append(parts, choice.Delta.Content)
		}

		chunk := domain.ChatResponse{
			ID:      qwenResp.ID,
			Object:  "chat.completion.chunk",
			Created: qwenResp.Created,
			Model:   req.Model,
			Choices: []domain.Choice{{
				Index: 0,
				Delta: &domain.ResponseMessage{
					Role:      choice.Delta.Role,
					Content:   choice.Delta.Content,
					ToolCalls: choice.Delta.ToolCalls,
				},
			}},
		}

		if choice.FinishReason != nil {
			lastFinishReason = *choice.FinishReason
			chunk.Choices[0].FinishReason = choice.FinishReason
		}

		data, _ := json.Marshal(chunk)
		fmt.Fprintf(w, "data: %s\n\n", data)
		flusher.Flush()
	}

	if lastFinishReason == "" {
		lastFinishReason = "stop"
	}

	stop := domain.ChatResponse{
		ID:      utils.GenerateChatCompletionID(),
		Object:  "chat.completion.chunk",
		Created: time.Now().Unix(),
		Model:   req.Model,
		Choices: []domain.Choice{{
			Index:        0,
			Delta:        &domain.ResponseMessage{},
			FinishReason: &lastFinishReason,
		}},
	}
	data, _ := json.Marshal(stop)
	fmt.Fprintf(w, "data: %s\n\n", data)
	flusher.Flush()

	if includeUsage {
		text := strings.Join(parts, "")
		promptTokens := tokenizer.Count(zlm.ExtractTextFromMessages(req.Messages))
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

func qwenNonStreamResponse(w http.ResponseWriter, resp *http.Response, req *domain.ChatRequest, tokenizer utils.Tokener) {
	qwenResp, err := qwen.ParseNonStreamResponse(resp)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "failed to parse response")
		return
	}

	if len(qwenResp.Choices) == 0 {
		writeErr(w, http.StatusInternalServerError, "empty response")
		return
	}

	choice := qwenResp.Choices[0]
	msg := &domain.ResponseMessage{Role: "assistant"}

	if choice.Message != nil {
		msg.Content = choice.Message.Content
		msg.ToolCalls = choice.Message.ToolCalls
	}

	finishReason := "stop"
	if choice.FinishReason != nil {
		finishReason = *choice.FinishReason
	}
	if len(msg.ToolCalls) > 0 {
		finishReason = "tool_calls"
	}

	response := domain.ChatResponse{
		ID:      qwenResp.ID,
		Object:  "chat.completion",
		Created: qwenResp.Created,
		Model:   req.Model,
		Choices: []domain.Choice{{
			Index:        0,
			Message:      msg,
			FinishReason: &finishReason,
		}},
	}

	if qwenResp.Usage != nil {
		response.Usage = qwenResp.Usage
	} else {
		promptTokens := tokenizer.Count(zlm.ExtractTextFromMessages(req.Messages))
		completionTokens := tokenizer.Count(msg.Content)
		response.Usage = &domain.Usage{
			PromptTokens:     promptTokens,
			CompletionTokens: completionTokens,
			TotalTokens:      promptTokens + completionTokens,
		}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}

func ListModels(cfg *config.Config, store *tokenstore.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var models []map[string]any

		qwenModels := []map[string]any{
			{"id": "coder-model", "object": "model", "created": time.Now().Unix(), "owned_by": "qwen"},
			{"id": "vision-model", "object": "model", "created": time.Now().Unix(), "owned_by": "qwen"},
		}
		models = append(models, qwenModels...)

		glmToken, _ := store.GetActiveByProvider("glm")
		if glmToken != nil {
			url := fmt.Sprintf("%s//%s/api/models", cfg.Upstream.Protocol, cfg.Upstream.Host)

			req, err := http.NewRequest("GET", url, nil)
			if err == nil {
				for k, v := range cfg.GetUpstreamHeaders() {
					req.Header.Set(k, v)
				}
				req.Header.Set("Authorization", "Bearer "+glmToken.Token)

				client := &http.Client{Timeout: 10 * time.Second}
				resp, err := client.Do(req)
				if err == nil && resp.StatusCode == http.StatusOK {
					defer resp.Body.Close()

					var upstream struct {
						Data []struct {
							ID   string `json:"id"`
							Name string `json:"name"`
						} `json:"data"`
					}
					if json.NewDecoder(resp.Body).Decode(&upstream) == nil {
						for _, m := range upstream.Data {
							models = append(models, map[string]any{
								"id":       m.ID,
								"object":   "model",
								"created":  time.Now().Unix(),
								"owned_by": "zhipu",
							})
						}
					}
				}
			}
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"object": "list",
			"data":   models,
		})
	}
}

func RegisterAccount(store *tokenstore.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		logger.Info().Msg("starting account registration")

		mail := tempmail.New()
		email, err := mail.CreateEmail()
		if err != nil {
			logger.Error().Err(err).Msg("failed to create temp email")
			writeErr(w, http.StatusInternalServerError, "failed to create temp email")
			return
		}
		logger.Info().Str("email", email.Address).Msg("created temp email")

		password := crypto.GeneratePassword(16)
		name := strings.Split(email.Address, "@")[0]

		creds := browser.Credentials{
			Email:    email.Address,
			Password: password,
			Name:     name,
		}

		br, err := browser.New(false)
		if err != nil {
			logger.Error().Err(err).Msg("failed to start browser")
			writeErr(w, http.StatusInternalServerError, "failed to start browser")
			return
		}
		defer br.Close()

		if _, err := br.RegisterZAI(creds); err != nil {
			logger.Error().Err(err).Msg("registration failed")
			writeErr(w, http.StatusInternalServerError, "registration failed: "+err.Error())
			return
		}

		logger.Info().Msg("waiting for verification email")

		msg, err := mail.WaitForMessage(email.Address, "z.ai", "verify", 2*time.Minute, 3*time.Second)
		if err != nil {
			logger.Error().Err(err).Msg("failed to get verification email")
			writeErr(w, http.StatusInternalServerError, "failed to get verification email")
			return
		}
		if msg == nil {
			logger.Error().Msg("verification email not received")
			writeErr(w, http.StatusInternalServerError, "verification email not received")
			return
		}

		logger.Info().Str("subject", msg.Subject).Msg("got verification email")

		link := tempmail.ExtractVerifyLink(msg.BodyText)
		if link == "" {
			link = tempmail.ExtractVerifyLink(msg.BodyHTML)
		}
		if link == "" {
			logger.Error().Msg("verify link not found in email")
			writeErr(w, http.StatusInternalServerError, "verify link not found")
			return
		}

		logger.Info().Str("link", link).Msg("extracted verify link")

		token, err := br.VerifyEmail(link, password)
		if err != nil {
			logger.Error().Err(err).Msg("email verification failed")
			writeErr(w, http.StatusInternalServerError, "verification failed: "+err.Error())
			return
		}

		saved, err := store.Add(email.Address, token)
		if err != nil {
			logger.Error().Err(err).Msg("failed to save token")
			writeErr(w, http.StatusInternalServerError, "failed to save token")
			return
		}

		logger.Info().Str("id", saved.ID).Msg("token saved to store")

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"success": true,
			"token":   saved,
		})
	}
}

func ListTokens(store *tokenstore.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		tokens, err := store.List()
		if err != nil {
			writeErr(w, http.StatusInternalServerError, "failed to list tokens")
			return
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"tokens": tokens,
		})
	}
}

func ListTokensByProvider(store *tokenstore.Store, prov string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		tokens, err := store.ListByProvider(prov)
		if err != nil {
			writeErr(w, http.StatusInternalServerError, "failed to list tokens")
			return
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"tokens": tokens,
		})
	}
}

func RemoveToken(store *tokenstore.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := chi.URLParam(r, "id")
		if id == "" {
			writeErr(w, http.StatusBadRequest, "missing token id")
			return
		}

		if err := store.Remove(id); err != nil {
			writeErr(w, http.StatusInternalServerError, "failed to remove token")
			return
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"success": true,
		})
	}
}

func ActivateToken(store *tokenstore.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := chi.URLParam(r, "id")
		if id == "" {
			writeErr(w, http.StatusBadRequest, "missing token id")
			return
		}

		if err := store.SetActive(id); err != nil {
			writeErr(w, http.StatusInternalServerError, "failed to activate token")
			return
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"success": true,
		})
	}
}

func ValidateTokenByID(store *tokenstore.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := chi.URLParam(r, "id")
		if id == "" {
			writeErr(w, http.StatusBadRequest, "missing token id")
			return
		}

		token, err := store.GetByID(id)
		if err != nil {
			writeErr(w, http.StatusInternalServerError, "failed to get token")
			return
		}
		if token == nil {
			writeErr(w, http.StatusNotFound, "token not found")
			return
		}

		valid := false
		switch token.Provider {
		case "glm":
			valid = tokenstore.ValidateToken(token.Token)
		case "qwen":
			valid = !qwen.IsTokenExpired(token.ExpiryDate)
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"id":       token.ID,
			"provider": token.Provider,
			"email":    token.Email,
			"valid":    valid,
		})
	}
}

func RegisterQwenAccount(store *tokenstore.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		logger.Info().Msg("starting qwen account registration")

		mail := tempmail.New()
		email, err := mail.CreateEmail()
		if err != nil {
			logger.Error().Err(err).Msg("failed to create temp email")
			writeErr(w, http.StatusInternalServerError, "failed to create temp email")
			return
		}
		logger.Info().Str("email", email.Address).Msg("created temp email")

		password := crypto.GeneratePassword(16)
		name := strings.Split(email.Address, "@")[0]

		br, err := browser.New(false)
		if err != nil {
			logger.Error().Err(err).Msg("failed to start browser")
			writeErr(w, http.StatusInternalServerError, "failed to start browser")
			return
		}
		defer br.Close()

		if err := br.RegisterQwen(email.Address, password, name); err != nil {
			logger.Error().Err(err).Msg("qwen registration failed")
			writeErr(w, http.StatusInternalServerError, "registration failed: "+err.Error())
			return
		}

		logger.Info().Msg("waiting for activation email")

		msg, err := mail.WaitForMessage(email.Address, "qwen", "active", 2*time.Minute, 3*time.Second)
		if err != nil {
			logger.Error().Err(err).Msg("failed to get activation email")
			writeErr(w, http.StatusInternalServerError, "failed to get activation email")
			return
		}
		if msg == nil {
			logger.Error().Msg("activation email not received")
			writeErr(w, http.StatusInternalServerError, "activation email not received")
			return
		}

		logger.Info().Str("subject", msg.Subject).Msg("got activation email")

		link := tempmail.ExtractQwenActivationLink(msg.BodyText)
		if link == "" {
			link = tempmail.ExtractQwenActivationLink(msg.BodyHTML)
		}
		if link == "" {
			logger.Error().Msg("activation link not found in email")
			writeErr(w, http.StatusInternalServerError, "activation link not found")
			return
		}

		logger.Info().Str("link", link).Msg("extracted activation link")

		if err := br.ActivateQwen(link); err != nil {
			logger.Error().Err(err).Msg("activation failed")
			writeErr(w, http.StatusInternalServerError, "activation failed: "+err.Error())
			return
		}

		logger.Info().Msg("account activated, starting device flow")

		deviceCode, err := qwen.RequestDeviceCode()
		if err != nil {
			logger.Error().Err(err).Msg("device code request failed")
			writeErr(w, http.StatusInternalServerError, "device code failed: "+err.Error())
			return
		}

		logger.Info().Str("url", deviceCode.VerificationURIComplete).Msg("confirming auth")

		if err := br.ConfirmQwenAuth(deviceCode.VerificationURIComplete); err != nil {
			logger.Error().Err(err).Msg("auth confirmation failed")
			writeErr(w, http.StatusInternalServerError, "auth confirmation failed: "+err.Error())
			return
		}

		var token *qwen.OAuthToken
		for range 20 {
			time.Sleep(3 * time.Second)
			token, err = qwen.PollForToken(deviceCode.DeviceCode, deviceCode.CodeVerifier)
			if err != nil {
				logger.Error().Err(err).Msg("token poll failed")
				writeErr(w, http.StatusInternalServerError, "token poll failed: "+err.Error())
				return
			}
			if token != nil {
				break
			}
		}

		if token == nil {
			logger.Error().Msg("token poll timeout")
			writeErr(w, http.StatusInternalServerError, "token poll timeout")
			return
		}

		saved, err := store.AddWithProvider("qwen", email.Address, token.AccessToken, token.RefreshToken, token.ExpiryDate)
		if err != nil {
			logger.Error().Err(err).Msg("failed to save token")
			writeErr(w, http.StatusInternalServerError, "failed to save token")
			return
		}

		logger.Info().Str("id", saved.ID).Msg("qwen token saved")

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"success": true,
			"token":   saved,
		})
	}
}

func getStr(m map[string]any, key string) string {
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
