// Package problem emits RFC 7807 Problem Details responses.
//
// This package is the ONLY approved way for API handlers to return an error
// to the client. Handlers should never write raw error strings or
// call http.Error directly.
package problem

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/google/uuid"
)

// Details is the JSON document structure from RFC 7807.
type Details struct {
	Type     string `json:"type"`
	Title    string `json:"title"`
	Status   int    `json:"status"`
	Detail   string `json:"detail,omitempty"`
	Instance string `json:"instance,omitempty"`
	TraceID  string `json:"trace_id,omitempty"`
}

// Error is an application error that can be directly converted into a
// problem response.
type Error struct {
	HTTPStatus int
	TypeSlug   string // e.g. "topic-url-not-recognized"
	Title      string
	Detail     string
}

func (e *Error) Error() string { return e.Title + ": " + e.Detail }

// New builds a new *Error.
func New(status int, slug, title, detail string) *Error {
	return &Error{HTTPStatus: status, TypeSlug: slug, Title: title, Detail: detail}
}

// Common factories.
var (
	ErrUnauthorized = func(detail string) *Error {
		return New(http.StatusUnauthorized, "unauthorized", "Unauthorized", detail)
	}
	ErrForbidden = func(detail string) *Error {
		return New(http.StatusForbidden, "forbidden", "Forbidden", detail)
	}
	ErrNotFound = func(detail string) *Error {
		return New(http.StatusNotFound, "not-found", "Not Found", detail)
	}
	ErrConflict = func(detail string) *Error {
		return New(http.StatusConflict, "conflict", "Conflict", detail)
	}
	ErrUnprocessable = func(detail string) *Error {
		return New(http.StatusUnprocessableEntity, "unprocessable", "Unprocessable Entity", detail)
	}
	ErrBadRequest = func(detail string) *Error {
		return New(http.StatusBadRequest, "bad-request", "Bad Request", detail)
	}
	ErrInternal = func(detail string) *Error {
		return New(http.StatusInternalServerError, "internal", "Internal Server Error", detail)
	}
)

// Write renders an error as a JSON RFC 7807 response.
// baseURL is the public base URL used to build the `type` field.
func Write(w http.ResponseWriter, r *http.Request, baseURL string, err error) {
	var pe *Error
	if !errors.As(err, &pe) {
		pe = ErrInternal(err.Error())
	}

	traceID := r.Header.Get("X-Request-ID")
	if traceID == "" {
		traceID = uuid.NewString()
	}

	d := Details{
		Type:     baseURL + "/errors/" + pe.TypeSlug,
		Title:    pe.Title,
		Status:   pe.HTTPStatus,
		Detail:   pe.Detail,
		Instance: r.URL.Path,
		TraceID:  traceID,
	}

	w.Header().Set("Content-Type", "application/problem+json")
	w.Header().Set("X-Request-ID", traceID)
	w.WriteHeader(pe.HTTPStatus)
	_ = json.NewEncoder(w).Encode(d)
}
