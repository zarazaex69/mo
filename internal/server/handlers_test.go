package server

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"github.com/zarazaex69/mo/internal/config"
	"github.com/zarazaex69/mo/internal/domain"
)

type MockAIClient struct {
	mock.Mock
}

func (m *MockAIClient) SendChatRequest(req *domain.ChatRequest, chatID string) (*http.Response, error) {
	args := m.Called(req, chatID)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*http.Response), args.Error(1)
}

type MockTokener struct {
	counts map[string]int
}

func (m *MockTokener) Init() error { return nil }
func (m *MockTokener) Count(text string) int {
	if val, ok := m.counts[text]; ok {
		return val
	}
	return len(strings.Fields(text))
}

func TestChatCompletions(t *testing.T) {
	cfg := &config.Config{
		Model: config.ModelConfig{Default: "gpt-4-turbo"},
	}

	tests := []struct {
		name       string
		body       interface{}
		setup      func(*MockAIClient)
		wantStatus int
		verify     func(*testing.T, *httptest.ResponseRecorder)
	}{
		{
			name:       "invalid json",
			body:       `{ "messages": [`,
			setup:      func(m *MockAIClient) {},
			wantStatus: http.StatusBadRequest,
			verify: func(t *testing.T, w *httptest.ResponseRecorder) {
				assert.Contains(t, w.Body.String(), "Invalid JSON request")
			},
		},
		{
			name:       "empty messages",
			body:       domain.ChatRequest{Model: "gpt-4", Messages: []domain.Message{}},
			setup:      func(m *MockAIClient) {},
			wantStatus: http.StatusBadRequest,
			verify: func(t *testing.T, w *httptest.ResponseRecorder) {
				assert.Contains(t, w.Body.String(), "validation failed")
			},
		},
		{
			name: "upstream error",
			body: domain.ChatRequest{
				Model:    "gpt-4",
				Messages: []domain.Message{{Role: "user", Content: "Hello"}},
			},
			setup: func(m *MockAIClient) {
				m.On("SendChatRequest", mock.Anything, mock.Anything).
					Return(nil, errors.New("connection failed"))
			},
			wantStatus: http.StatusInternalServerError,
			verify: func(t *testing.T, w *httptest.ResponseRecorder) {
				assert.Contains(t, w.Body.String(), "Failed to process request")
			},
		},
		{
			name: "non-streaming success",
			body: domain.ChatRequest{
				Model:    "gpt-4",
				Messages: []domain.Message{{Role: "user", Content: "Say hi"}},
				Stream:   false,
			},
			setup: func(m *MockAIClient) {
				sse := `data: {"data": {"phase": "answer", "delta_content": "Hello"}}` + "\n\n" +
					`data: {"data": {"phase": "answer", "delta_content": " World", "done": true}}` + "\n\n" +
					`data: [DONE]` + "\n\n"
				resp := &http.Response{
					StatusCode: 200,
					Body:       io.NopCloser(strings.NewReader(sse)),
				}
				m.On("SendChatRequest", mock.Anything, mock.Anything).Return(resp, nil)
			},
			wantStatus: http.StatusOK,
			verify: func(t *testing.T, w *httptest.ResponseRecorder) {
				var resp domain.ChatResponse
				err := json.Unmarshal(w.Body.Bytes(), &resp)
				require.NoError(t, err)
				assert.Equal(t, "chat.completion", resp.Object)
				assert.NotEmpty(t, resp.Choices)
				assert.Equal(t, "Hello World", resp.Choices[0].Message.Content)
			},
		},

		{
			name: "streaming success",
			body: domain.ChatRequest{
				Model:    "gpt-4",
				Messages: []domain.Message{{Role: "user", Content: "Count to 2"}},
				Stream:   true,
			},
			setup: func(m *MockAIClient) {
				sse := `data: {"data": {"phase": "answer", "delta_content": "1"}}` + "\n\n" +
					`data: {"data": {"phase": "answer", "delta_content": "2", "done": true}}` + "\n\n" +
					`data: [DONE]` + "\n\n"
				resp := &http.Response{
					StatusCode: 200,
					Body:       io.NopCloser(strings.NewReader(sse)),
				}
				m.On("SendChatRequest", mock.Anything, mock.Anything).Return(resp, nil)
			},
			wantStatus: http.StatusOK,
			verify: func(t *testing.T, w *httptest.ResponseRecorder) {
				assert.Contains(t, w.Header().Get("Content-Type"), "text/event-stream")
				body := w.Body.String()
				assert.Contains(t, body, `"content":"1"`)
				assert.Contains(t, body, `"content":"2"`)
				assert.Contains(t, body, `"finish_reason":"stop"`)
				assert.Contains(t, body, "[DONE]")
			},
		},
		{
			name: "streaming with usage",
			body: domain.ChatRequest{
				Model:      "gpt-4",
				Messages:   []domain.Message{{Role: "user", Content: "Count to 1"}},
				Stream:     true,
				StreamOpts: &domain.StreamOptions{IncludeUsage: true},
			},
			setup: func(m *MockAIClient) {
				sse := `data: {"data": {"phase": "answer", "delta_content": "1", "done": true}}` + "\n\n" +
					`data: [DONE]` + "\n\n"
				resp := &http.Response{
					StatusCode: 200,
					Body:       io.NopCloser(strings.NewReader(sse)),
				}
				m.On("SendChatRequest", mock.Anything, mock.Anything).Return(resp, nil)
			},
			wantStatus: http.StatusOK,
			verify: func(t *testing.T, w *httptest.ResponseRecorder) {
				body := w.Body.String()
				assert.Contains(t, body, `"usage":{`)
				assert.Contains(t, body, `"prompt_tokens":`)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockAI := new(MockAIClient)
			mockTokenizer := &MockTokener{counts: make(map[string]int)}

			tt.setup(mockAI)

			var reqBody io.Reader
			if s, ok := tt.body.(string); ok {
				reqBody = strings.NewReader(s)
			} else {
				b, _ := json.Marshal(tt.body)
				reqBody = bytes.NewReader(b)
			}

			req := httptest.NewRequest("POST", "/v1/chat/completions", reqBody)
			req.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()

			handler := ChatCompletions(cfg, mockAI, mockTokenizer)
			handler(w, req)

			assert.Equal(t, tt.wantStatus, w.Code)
			if tt.verify != nil {
				tt.verify(t, w)
			}

			mockAI.AssertExpectations(t)
		})
	}
}
