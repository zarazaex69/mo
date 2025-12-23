package validator

import (
	"fmt"
	"strings"

	"github.com/go-playground/validator/v10"
)

var validate *validator.Validate

func init() {
	validate = validator.New()
}

// Validate validates a struct using validator tags
func Validate(s interface{}) error {
	if err := validate.Struct(s); err != nil {
		if validationErrors, ok := err.(validator.ValidationErrors); ok {
			return formatValidationErrors(validationErrors)
		}
		return err
	}
	return nil
}

// formatValidationErrors converts validator errors to a readable format
func formatValidationErrors(errs validator.ValidationErrors) error {
	var messages []string
	for _, err := range errs {
		messages = append(messages, formatFieldError(err))
	}
	return fmt.Errorf("validation failed: %s", strings.Join(messages, "; "))
}

// formatFieldError formats a single field validation error
func formatFieldError(err validator.FieldError) string {
	field := err.Field()
	tag := err.Tag()

	switch tag {
	case "required":
		return fmt.Sprintf("field '%s' is required", field)
	case "min":
		return fmt.Sprintf("field '%s' must have at least %s items", field, err.Param())
	case "max":
		return fmt.Sprintf("field '%s' must have at most %s items", field, err.Param())
	case "gte":
		return fmt.Sprintf("field '%s' must be >= %s", field, err.Param())
	case "lte":
		return fmt.Sprintf("field '%s' must be <= %s", field, err.Param())
	case "gt":
		return fmt.Sprintf("field '%s' must be > %s", field, err.Param())
	case "lt":
		return fmt.Sprintf("field '%s' must be < %s", field, err.Param())
	case "oneof":
		return fmt.Sprintf("field '%s' must be one of: %s", field, err.Param())
	default:
		return fmt.Sprintf("field '%s' failed validation '%s'", field, tag)
	}
}
