package utils

// Tokener defines the interface for the token counting service.
type Tokener interface {
	Init() error
	Count(text string) int
}
