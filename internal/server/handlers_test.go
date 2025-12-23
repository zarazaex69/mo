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

// MockAIClient satisfies the AIClienter interface
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

// MockTokener satisfies the utils.Tokener interface
type MockTokener struct {
	counts map[string]int
}

func (m *MockTokener) Init() error { return nil }
func (m *MockTokener) Count(text string) int {
	if val, ok := m.counts[text]; ok {
		return val
	}
	return len(strings.Fields(text)) // fallback simple count
}

func TestChatCompletions(t *testing.T) {
	// Setup a base configuration
	cfg := &config.Config{
		Model: config.ModelConfig{
			Default: "gpt-4-turbo",
		},
	}

	tests := []struct {
		name           string
		requestBody    interface{} // Can be string (malformed) or domain.ChatRequest
		setupMock      func(*MockAIClient)
		expectedStatus int
		verifyResponse func(*testing.T, *httptest.ResponseRecorder)
	}{
		{
			name:           "Invalid JSON",
			requestBody:    `{ "messages": [`,        // Malformed JSON
			setupMock:      func(m *MockAIClient) {}, // No call expected
			expectedStatus: http.StatusBadRequest,
			verifyResponse: func(t *testing.T, w *httptest.ResponseRecorder) {
				assert.Contains(t, w.Body.String(), "Invalid JSON request")
			},
		},
		{
			name: "Validation Error - Empty Messages",
			requestBody: domain.ChatRequest{
				Model:    "gpt-4",
				Messages: []domain.Message{}, // Empty messages
			},
			setupMock:      func(m *MockAIClient) {},
			expectedStatus: http.StatusBadRequest,
			verifyResponse: func(t *testing.T, w *httptest.ResponseRecorder) {
				// Validator should complain about min=1
				assert.Contains(t, w.Body.String(), "validation failed: field 'Messages' must have at least 1 items")
			},
		},
		{
			name: "Upstream Error",
			requestBody: domain.ChatRequest{
				Model: "gpt-4",
				Messages: []domain.Message{
					{Role: "user", Content: "Hello"},
				},
			},
			setupMock: func(m *MockAIClient) {
				m.On("SendChatRequest", mock.Anything, mock.Anything).
					Return(nil, errors.New("upstream connection failed"))
			},
			expectedStatus: http.StatusInternalServerError,
			verifyResponse: func(t *testing.T, w *httptest.ResponseRecorder) {
				assert.Contains(t, w.Body.String(), "Failed to process request")
			},
		},
		{
			name: "Success - Non-Streaming",
			requestBody: domain.ChatRequest{
				Model: "gpt-4",
				Messages: []domain.Message{
					{Role: "user", Content: "Say hi"},
				},
				Stream: false,
			},
			setupMock: func(m *MockAIClient) {
				// Simulate SSE response from upstream
				sseResponse := `data: {"data": {"phase": "answer", "delta_content": "Hello"}}` + "\n\n" +
					`data: {"data": {"phase": "answer", "delta_content": " World", "done": true}}` + "\n\n" +
					`data: [DONE]` + "\n\n"

				resp := &http.Response{
					StatusCode: 200,
					Body:       io.NopCloser(strings.NewReader(sseResponse)),
				}
				m.On("SendChatRequest", mock.Anything, mock.Anything).Return(resp, nil)
			},
			expectedStatus: http.StatusOK,
			verifyResponse: func(t *testing.T, w *httptest.ResponseRecorder) {
				var resp domain.ChatResponse
				err := json.Unmarshal(w.Body.Bytes(), &resp)
				require.NoError(t, err)

				assert.Equal(t, "chat.completion", resp.Object)
				assert.NotEmpty(t, resp.Choices)
				assert.Equal(t, "assistant", resp.Choices[0].Message.Role)
				assert.Equal(t, "Hello World", resp.Choices[0].Message.Content)
			},
		},
		{
			name: "Success - Streaming",
			requestBody: domain.ChatRequest{
				Model: "gpt-4",
				Messages: []domain.Message{
					{Role: "user", Content: "Count to 2"},
				},
				Stream: true,
			},
			setupMock: func(m *MockAIClient) {
				// Simulate SSE response from upstream
				sseResponse := `data: {"data": {"phase": "answer", "delta_content": "1"}}` + "\n\n" +
					`data: {"data": {"phase": "answer", "delta_content": "2", "done": true}}` + "\n\n" +
					`data: [DONE]` + "\n\n"

				resp := &http.Response{
					StatusCode: 200,
					Body:       io.NopCloser(strings.NewReader(sseResponse)),
				}
				m.On("SendChatRequest", mock.Anything, mock.Anything).Return(resp, nil)
			},
			expectedStatus: http.StatusOK,
			verifyResponse: func(t *testing.T, w *httptest.ResponseRecorder) {
				contentType := w.Header().Get("Content-Type")
				assert.Contains(t, contentType, "text/event-stream")

				body := w.Body.String()
				assert.Contains(t, body, "data: {") // Contains JSON data lines
				assert.Contains(t, body, "[DONE]")

				// Verify chunks roughly
				// We expect chunk 1 with content "1", chunk 2 with content "2", stop chunk
				assert.Contains(t, body, `"content":"1"`)
				assert.Contains(t, body, `"content":"2"`)
				assert.Contains(t, body, `"finish_reason":"stop"`)
			},
		},
		{
			name: "Success - Streaming with Usage",
			requestBody: domain.ChatRequest{
				Model: "gpt-4",
				Messages: []domain.Message{
					{Role: "user", Content: "Count to 1"},
				},
				Stream:     true,
				StreamOpts: &domain.StreamOptions{IncludeUsage: true},
			},
			setupMock: func(m *MockAIClient) {
				sseResponse := `data: {"data": {"phase": "answer", "delta_content": "1", "done": true}}` + "\n\n" +
					`data: [DONE]` + "\n\n"

				resp := &http.Response{
					StatusCode: 200,
					Body:       io.NopCloser(strings.NewReader(sseResponse)),
				}
				m.On("SendChatRequest", mock.Anything, mock.Anything).Return(resp, nil)
			},
			expectedStatus: http.StatusOK,
			verifyResponse: func(t *testing.T, w *httptest.ResponseRecorder) {
				body := w.Body.String()
				// Should find usage in the last chunk before [DONE]
				assert.Contains(t, body, `"usage":{`)
				assert.Contains(t, body, `"prompt_tokens":`)
				assert.Contains(t, body, `"total_tokens":`)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Mock dependencies
			mockAI := new(MockAIClient)
			mockTokenizer := &MockTokener{counts: make(map[string]int)}

			tt.setupMock(mockAI)

			// Prepare Request
			var reqBodyFunc io.Reader
			if s, ok := tt.requestBody.(string); ok {
				reqBodyFunc = strings.NewReader(s)
			} else {
				b, _ := json.Marshal(tt.requestBody)
				reqBodyFunc = bytes.NewReader(b)
			}

			req := httptest.NewRequest("POST", "/v1/chat/completions", reqBodyFunc)
			req.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()

			// Handler under test
			handler := ChatCompletions(cfg, mockAI, mockTokenizer)
			handler(w, req)

			// Verify
			assert.Equal(t, tt.expectedStatus, w.Code)
			if tt.verifyResponse != nil {
				tt.verifyResponse(t, w)
			}

			mockAI.AssertExpectations(t)
		})
	}
}

// Ensure the helper works locally for running single file tests if Tokener is complex
func (m *MockTokener) CountTokens(messages []domain.Message) int {
	return 0
}
