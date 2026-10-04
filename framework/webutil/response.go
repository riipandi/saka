// Package web is the request kit over the stdlib signatures: the API envelope,
// request decoding and validation, pagination, and the per-request helpers
// (PathParam, Query, Decode, Principal) the REST handlers read. Handlers keep
// func(w http.ResponseWriter, r *http.Request); nothing here wraps the pair.
// The ConnectRPC surface is deliberately outside: its procedures answer
// connect.Request and its cross-cutting concerns are interceptors.
package webutil

import (
	"context"
	"encoding/json/v2"
	"net/http"
	"strconv"
	"strings"

	"go.jetify.com/typeid"
)

// Envelope status values.
const (
	StatusSuccess = "success"
	StatusError   = "error"
)

const (
	RequestIDHeader = "X-Request-Id"
	traceIDHeader   = "X-Trace-Id"
)

type requestPrefix struct{}

func (requestPrefix) Prefix() string { return "req" }

// RequestID identifies a single API request for tracing and support.
type RequestID = typeid.TypeID[requestPrefix]

// requestIDContextKey scopes the request ID inside a request context.
type requestIDContextKey struct{}

// NewRequestID generates a fresh request ID.
func NewRequestID() string {
	return typeid.Must(typeid.New[RequestID]()).String()
}

// WithRequestID attaches a request ID to a context.
func WithRequestID(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, requestIDContextKey{}, id)
}

// RequestIDFromContext returns the request ID in ctx, if present.
func RequestIDFromContext(ctx context.Context) string {
	id, _ := ctx.Value(requestIDContextKey{}).(string)
	return id
}

// Envelope is the standard API response wrapper.
type Envelope struct {
	Status   string   `json:"status"`
	Message  string   `json:"message,omitempty"`
	Data     any      `json:"data,omitzero"`
	Error    any      `json:"error,omitzero"`
	Metadata Metadata `json:"metadata"`
	Links    Links    `json:"links,omitzero"`
}

// Metadata carries request and pagination data.
type Metadata struct {
	StatusCode int        `json:"status_code"`
	RequestID  string     `json:"request_id"`
	TraceID    string     `json:"trace_id,omitempty"`
	RateLimit  *RateLimit `json:"rate_limit,omitempty"`

	Page           *int `json:"page,omitempty"`
	Limit          *int `json:"limit,omitempty"`
	TotalPages     *int `json:"total_pages,omitempty"`
	TotalItems     *int `json:"total_items,omitempty"`
	FirstItemIndex *int `json:"first_item_index,omitempty"`
	LastItemIndex  *int `json:"last_item_index,omitempty"`
}

// RateLimit reports the per-window allowance of the rate limiter.
type RateLimit struct {
	Limit     int   `json:"limit"`
	Remaining int   `json:"remaining"`
	Reset     int64 `json:"reset"` // unix timestamp when the window resets
}

// Links maps link relations to URLs. A nil entry marshals as null.
type Links map[string]*string

// Option customizes the envelope built by Success and Fail.
type Option func(*Envelope)

// WithError attaches structured error details (an object or array).
// WithMessage sets the sentence the envelope carries. An error names its
// refusal through Fail's message; a success names its outcome here.
func WithMessage(message string) Option {
	return func(env *Envelope) {
		env.Message = message
	}
}

func WithError(detail any) Option {
	return func(e *Envelope) { e.Error = detail }
}

// WithPagination embeds precomputed pagination metadata.
func WithPagination(p Pagination) Option {
	return func(e *Envelope) { e.Metadata.applyPagination(p) }
}

// WithPaginationFrom computes pagination metadata from params and totalItems.
func WithPaginationFrom(params PaginationParams, totalItems int) Option {
	return WithPagination(NewPagination(params, totalItems))
}

// Success writes a success envelope with an optional data payload.
func Success(w http.ResponseWriter, r *http.Request, status int, data any, opts ...Option) {
	env := &Envelope{Status: StatusSuccess, Data: data, Metadata: newMetadata(w, r, status)}
	writeEnvelope(w, env, opts)
}

// Fail writes an error envelope. Error details go through WithError.
func Fail(w http.ResponseWriter, r *http.Request, status int, message string, opts ...Option) {
	env := &Envelope{Status: StatusError, Message: message, Metadata: newMetadata(w, r, status)}
	writeEnvelope(w, env, opts)
}

// WriteJSON writes v as JSON without an envelope.
func WriteJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.MarshalWrite(w, v); err != nil {
		http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
	}
}

// NotFoundJSON writes a 404 error envelope.
func NotFoundJSON(w http.ResponseWriter, r *http.Request) {
	Fail(w, r, http.StatusNotFound, "not found")
}

// MethodNotAllowedJSON writes a 405 error envelope.
func MethodNotAllowedJSON(w http.ResponseWriter, r *http.Request) {
	Fail(w, r, http.StatusMethodNotAllowed, "method not allowed")
}

// BadRequestJSON writes a 400 error envelope.
func BadRequestJSON(w http.ResponseWriter, r *http.Request, message string) {
	Fail(w, r, http.StatusBadRequest, message)
}

func writeEnvelope(w http.ResponseWriter, env *Envelope, opts []Option) {
	for _, opt := range opts {
		if opt != nil {
			opt(env)
		}
	}
	WriteJSON(w, env.Metadata.StatusCode, env)
}

func newMetadata(w http.ResponseWriter, r *http.Request, status int) Metadata {
	m := Metadata{StatusCode: status, RequestID: requestID(w, r)}
	if trace := strings.TrimSpace(r.Header.Get(traceIDHeader)); trace != "" {
		m.TraceID = trace
	}
	m.RateLimit = rateLimitFromHeaders(w.Header())
	return m
}

// requestID gets the request ID from context, headers, or a new value.
func requestID(w http.ResponseWriter, r *http.Request) string {
	if id := RequestIDFromContext(r.Context()); id != "" {
		w.Header().Set(RequestIDHeader, id)
		return id
	}

	id := strings.TrimSpace(r.Header.Get(RequestIDHeader))
	if id == "" {
		id = NewRequestID()
	}
	w.Header().Set(RequestIDHeader, id)
	return id
}

// rateLimitFromHeaders reads rate-limit headers, if complete.
func rateLimitFromHeaders(h http.Header) *RateLimit {
	limit, err1 := strconv.Atoi(h.Get("X-RateLimit-Limit"))
	remaining, err2 := strconv.Atoi(h.Get("X-RateLimit-Remaining"))
	reset, err3 := strconv.ParseInt(h.Get("X-RateLimit-Reset"), 10, 64)
	if err1 != nil || err2 != nil || err3 != nil {
		return nil
	}
	return &RateLimit{Limit: limit, Remaining: remaining, Reset: reset}
}
