package webutil

import (
	"encoding/json/v2"
	"errors"
	"io"

	"encoding/json/jsontext"

	validation "github.com/go-ozzo/ozzo-validation/v4"
)

// FieldError locates one failed rule on the request payload.
type FieldError struct {
	Field   string `json:"field"`
	Message string `json:"message"`
}

// validatable is implemented by payloads with validation rules.
type validatable interface {
	Validate() error
}

// Request decodes the JSON body and runs validation when supported.
func Request(r io.Reader, dst any) error {
	if err := json.UnmarshalRead(r, dst); err != nil {
		return err
	}
	if v, ok := dst.(validatable); ok {
		return v.Validate()
	}
	return nil
}

// IsValidationError reports whether err contains validation failures.
func IsValidationError(err error) bool {
	var errs validation.Errors
	return errors.As(err, &errs)
}

// FieldErrors converts an error into field-level response entries. An
// error that carries ozzo field detail is expanded; any other error is
// reported with its own message, so a domain sentinel (an invalid
// username, say) is not misreported as a malformed body. Only a decode
// failure reads as "malformed JSON".
func FieldErrors(err error) []FieldError {
	if err == nil {
		return nil
	}

	if errs, ok := errors.AsType[validation.Errors](err); ok {
		out := make([]FieldError, 0, len(errs))
		for field, fieldErr := range errs {
			out = append(out, FieldError{Field: field, Message: fieldErr.Error()})
		}
		return out
	}

	if isDecodeError(err) {
		return []FieldError{{Field: "body", Message: "malformed JSON"}}
	}

	return []FieldError{{Field: "body", Message: err.Error()}}
}

// isDecodeError reports whether err came from decoding the request body
// rather than from a validation rule.
func isDecodeError(err error) bool {
	if _, ok := errors.AsType[*jsontext.SyntacticError](err); ok {
		return true
	}
	var typeErr *json.SemanticError
	return errors.As(err, &typeErr)
}
