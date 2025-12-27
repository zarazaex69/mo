package domain

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

type Tool struct {
	Type     string       `json:"type"`
	Function ToolFunction `json:"function"`
}

type ToolFunction struct {
	Name        string      `json:"name"`
	Description string      `json:"description"`
	Parameters  interface{} `json:"parameters"`
}

type Message struct {
	Role    string      `json:"role" validate:"required,oneof=system user assistant"`
	Content interface{} `json:"content" validate:"required"`
}

type StreamOptions struct {
	IncludeUsage bool `json:"include_usage"`
}

type ChatResponse struct {
	ID      string   `json:"id"`
	Object  string   `json:"object"`
	Created int64    `json:"created"`
	Model   string   `json:"model"`
	Choices []Choice `json:"choices"`
	Usage   *Usage   `json:"usage,omitempty"`
}

type Choice struct {
	Index        int              `json:"index"`
	Message      *ResponseMessage `json:"message,omitempty"`
	Delta        *ResponseMessage `json:"delta,omitempty"`
	FinishReason *string          `json:"finish_reason"`
}

type ResponseMessage struct {
	Role             string     `json:"role,omitempty"`
	Content          string     `json:"content,omitempty"`
	ReasoningContent string     `json:"reasoning_content,omitempty"`
	ToolCalls        []ToolCall `json:"tool_calls,omitempty"`
	ToolCall         string     `json:"tool_call,omitempty"`
}

type ToolCall struct {
	ID       string       `json:"id"`
	Type     string       `json:"type"`
	Function FunctionCall `json:"function"`
}

type FunctionCall struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

type Usage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}

type User struct {
	ID    string
	Token string
}

type HealthResponse struct {
	Status  string `json:"status"`
	Version string `json:"version"`
}

type ModelsResponse struct {
	Object string  `json:"object"`
	Data   []Model `json:"data"`
}

type Model struct {
	ID      string `json:"id"`
	Object  string `json:"object"`
	Created int64  `json:"created"`
	OwnedBy string `json:"owned_by"`
}

type ZaiResponse struct {
	Data *ZaiResponseData `json:"data"`
}

type ZaiResponseData struct {
	Phase        string `json:"phase"`
	DeltaContent string `json:"delta_content"`
	EditContent  string `json:"edit_content"`
	Done         bool   `json:"done"`
}

type UpstreamError struct {
	StatusCode int
	Message    string
}

func (e *UpstreamError) Error() string {
	return e.Message
}

func NewUpstreamError(code int, msg string) *UpstreamError {
	return &UpstreamError{StatusCode: code, Message: msg}
}
