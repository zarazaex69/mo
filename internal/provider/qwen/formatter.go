package qwen

import (
	"bufio"
	"encoding/json"
	"net/http"
	"strings"

	"github.com/zarazaex69/mo/internal/domain"
	"github.com/zarazaex69/mo/internal/pkg/logger"
)

type QwenResponse struct {
	ID      string        `json:"id"`
	Object  string        `json:"object"`
	Created int64         `json:"created"`
	Model   string        `json:"model"`
	Choices []QwenChoice  `json:"choices"`
	Usage   *domain.Usage `json:"usage,omitempty"`
}

type QwenChoice struct {
	Index        int          `json:"index"`
	Message      *QwenMessage `json:"message,omitempty"`
	Delta        *QwenMessage `json:"delta,omitempty"`
	FinishReason *string      `json:"finish_reason"`
}

type QwenMessage struct {
	Role      string            `json:"role,omitempty"`
	Content   string            `json:"content,omitempty"`
	ToolCalls []domain.ToolCall `json:"tool_calls,omitempty"`
}

func ParseSSEStream(resp *http.Response) <-chan *QwenResponse {
	ch := make(chan *QwenResponse)

	go func() {
		defer close(ch)

		scanner := bufio.NewScanner(resp.Body)
		buf := make([]byte, 0, 64*1024)
		scanner.Buffer(buf, 1024*1024)

		for scanner.Scan() {
			line := scanner.Text()
			if len(line) == 0 || !strings.HasPrefix(line, "data: ") {
				continue
			}

			data := line[6:]
			if strings.TrimSpace(data) == "[DONE]" {
				continue
			}

			var qwenResp QwenResponse
			if err := json.Unmarshal([]byte(data), &qwenResp); err != nil {
				logger.Debug().Err(err).Str("data", data).Msg("parse qwen sse failed")
				continue
			}

			ch <- &qwenResp
		}

		if err := scanner.Err(); err != nil {
			logger.Error().Err(err).Msg("qwen sse read error")
		}
	}()

	return ch
}

func ParseNonStreamResponse(resp *http.Response) (*QwenResponse, error) {
	var qwenResp QwenResponse
	if err := json.NewDecoder(resp.Body).Decode(&qwenResp); err != nil {
		return nil, err
	}
	return &qwenResp, nil
}
