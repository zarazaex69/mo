package domain

// ChatRequest represents an OpenAI-compatible chat completion request
type ChatRequest struct {
	Model       string         `json:"model"`
	Messages    []Message      `json:"messages" validate:"required,min=1,dive"`
	Stream      bool           `json:"stream"`
	Temperature *float64       `json:"temperature,omitempty" validate:"omitempty,gte=0,lte=2"`
	MaxTokens   *int           `json:"max_tokens,omitempty" validate:"omitempty,gt=0"`
	TopP        *float64       `json:"top_p,omitempty" validate:"omitempty,gte=0,lte=1"`
	StreamOpts  *StreamOptions `json:"stream_options,omitempty"`
	Tools       []Tool         `json:"tools,omitempty"`
	Thinking    *bool          `json:"thinking,omitempty"`
}

// Tool represents a tool definition
type Tool struct {
	Type     string       `json:"type"`
	Function ToolFunction `json:"function"`
}

// ToolFunction represents a function tool
type ToolFunction struct {
	Name        string      `json:"name"`
	Description string      `json:"description"`
	Parameters  interface{} `json:"parameters"`
}

// Message represents a single chat message
type Message struct {
	Role    string      `json:"role" validate:"required,oneof=system user assistant"`
	Content interface{} `json:"content" validate:"required"`
}

// StreamOptions controls streaming behavior
type StreamOptions struct {
	IncludeUsage bool `json:"include_usage"`
}

// ChatResponse represents an OpenAI-compatible chat completion response
type ChatResponse struct {
	ID      string   `json:"id"`
	Object  string   `json:"object"`
	Created int64    `json:"created"`
	Model   string   `json:"model"`
	Choices []Choice `json:"choices"`
	Usage   *Usage   `json:"usage,omitempty"`
}

// Choice represents a single completion choice
type Choice struct {
	Index        int              `json:"index"`
	Message      *ResponseMessage `json:"message,omitempty"`
	Delta        *ResponseMessage `json:"delta,omitempty"`
	FinishReason *string          `json:"finish_reason"`
}

// ResponseMessage represents a response message
type ResponseMessage struct {
	Role             string     `json:"role,omitempty"`
	Content          string     `json:"content,omitempty"`
	ReasoningContent string     `json:"reasoning_content,omitempty"`
	ToolCalls        []ToolCall `json:"tool_calls,omitempty"`
	ToolCall         string     `json:"tool_call,omitempty"` // Raw tool_call string from Z.AI
}

// ToolCall represents a tool call
type ToolCall struct {
	ID       string       `json:"id"`
	Type     string       `json:"type"`
	Function FunctionCall `json:"function"`
}

// FunctionCall represents a function call
type FunctionCall struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

// Usage represents token usage statistics
type Usage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}

// User represents an authenticated user
type User struct {
	ID    string
	Token string
}

// HealthResponse represents a health check response
type HealthResponse struct {
	Status  string `json:"status"`
	Version string `json:"version"`
}

// ModelsResponse represents the /v1/models response
type ModelsResponse struct {
	Object string  `json:"object"`
	Data   []Model `json:"data"`
}

// Model represents a single model
type Model struct {
	ID      string `json:"id"`
	Object  string `json:"object"`
	Created int64  `json:"created"`
	OwnedBy string `json:"owned_by"`
}

// ZaiResponse represents a response from Z.AI API
type ZaiResponse struct {
	Data *ZaiResponseData `json:"data"`
}

// ZaiResponseData represents the data field in Z.AI response
type ZaiResponseData struct {
	Phase        string `json:"phase"`
	DeltaContent string `json:"delta_content"`
	EditContent  string `json:"edit_content"`
	Done         bool   `json:"done"`
}

// UpstreamError represents an error from the upstream API
type UpstreamError struct {
	StatusCode int
	Message    string
}

func (e *UpstreamError) Error() string {
	return e.Message
}

func NewUpstreamError(statusCode int, message string) *UpstreamError {
	return &UpstreamError{
		StatusCode: statusCode,
		Message:    message,
	}
}
