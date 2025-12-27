package utils

import "github.com/google/uuid"

func GenerateID() string {
	return uuid.New().String()
}

func GenerateChatCompletionID() string {
	return "chatcmpl-" + uuid.New().String()
}

func GenerateRequestID() string {
	return uuid.New().String()
}
