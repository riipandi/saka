package responder

import (
	"context"
	"log/slog"
	"net/http"

	"github.com/riipandi/saka/pkg/validate"
)

// WriteError maps an error to the standard response envelope.
//
// A validation error is the caller's to fix, so it travels: 422 with the field
// errors attached. Anything else is opaque — the client learns that the
// request failed and nothing about why — and the cause is written to the log
// instead.
//
// The log line is not a courtesy. This is the one place an unexpected failure
// becomes a response, so a run that answers "internal error" without naming
// the cause leaves an operator with a status code and no thread to pull. The
// request id ties the line to the response the client was given.
func WriteError(w http.ResponseWriter, r *http.Request, err error) {
	if validate.IsValidationError(err) {
		Fail(w, r, http.StatusUnprocessableEntity, "validation failed",
			WithError(validate.FieldErrors(err)))
		return
	}

	// The context is detached: a cancelled request is exactly when the cause
	// is worth keeping, and the response has already failed by then.
	slog.ErrorContext(context.WithoutCancel(r.Context()), "request failed",
		slog.String("request_id", RequestIDFromContext(r.Context())),
		slog.String("method", r.Method),
		slog.String("path", r.URL.Path),
		slog.String("error", err.Error()))

	Fail(w, r, http.StatusInternalServerError, "internal error")
}
