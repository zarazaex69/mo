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

func Validate(s interface{}) error {
	err := validate.Struct(s)
	if err == nil {
		return nil
	}

	if errs, ok := err.(validator.ValidationErrors); ok {
		return formatErrors(errs)
	}
	return err
}

func formatErrors(errs validator.ValidationErrors) error {
	var msgs []string
	for _, e := range errs {
		msgs = append(msgs, formatFieldError(e))
	}
	return fmt.Errorf("validation failed: %s", strings.Join(msgs, "; "))
}

func formatFieldError(e validator.FieldError) string {
	field := e.Field()
	tag := e.Tag()
	param := e.Param()

	switch tag {
	case "required":
		return fmt.Sprintf("field '%s' is required", field)
	case "min":
		return fmt.Sprintf("field '%s' must have at least %s items", field, param)
	case "max":
		return fmt.Sprintf("field '%s' must have at most %s items", field, param)
	case "gte":
		return fmt.Sprintf("field '%s' must be >= %s", field, param)
	case "lte":
		return fmt.Sprintf("field '%s' must be <= %s", field, param)
	case "gt":
		return fmt.Sprintf("field '%s' must be > %s", field, param)
	case "lt":
		return fmt.Sprintf("field '%s' must be < %s", field, param)
	case "oneof":
		return fmt.Sprintf("field '%s' must be one of: %s", field, param)
	default:
		return fmt.Sprintf("field '%s' failed '%s'", field, tag)
	}
}
