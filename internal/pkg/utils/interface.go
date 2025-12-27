package utils

type Tokener interface {
	Init() error
	Count(text string) int
}
