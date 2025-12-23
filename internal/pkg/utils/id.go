package utils

import (
	"github.com/google/uuid"
)

// GenerateID generates a UUID v4
func GenerateID() string {
	return uuid.New().String()
}

// GenerateChatCompletionID generates an OpenAI-compatible chat completion ID
func GenerateChatCompletionID() string {
	return "chatcmpl-" + uuid.New().String()
}

// GenerateRequestID generates a unique request ID for tracking
func GenerateRequestID() string {
	return uuid.New().String()
}
