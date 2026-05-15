package errors

import (
	stderrors "errors"
	"fmt"
)

// AppError is a typed, HTTP-aware application error.
type AppError struct {
	Code    string
	Status  int
	Message string
	// Err is an optional wrapped cause (not exposed to API clients).
	Err error
}

func (e *AppError) Error() string {
	if e.Err != nil {
		return fmt.Sprintf("%s: %s: %v", e.Code, e.Message, e.Err)
	}
	return fmt.Sprintf("%s: %s", e.Code, e.Message)
}

func (e *AppError) Unwrap() error { return e.Err }

// Is matches by Code so wrapped copies still compare equal to the sentinel.
func (e *AppError) Is(target error) bool {
	var t *AppError
	if stderrors.As(target, &t) {
		return e.Code == t.Code
	}
	return false
}

// WithCause returns a copy of the sentinel carrying an underlying cause.
func (e *AppError) WithCause(cause error) *AppError {
	cp := *e
	cp.Err = cause
	return &cp
}

var (
	ErrCMSInvalid       = &AppError{Code: "CMS_INVALID", Status: 422, Message: "CMS signature is invalid"}
	ErrCertRevoked      = &AppError{Code: "CERT_REVOKED", Status: 422, Message: "Certificate is revoked"}
	ErrDocumentNotFound = &AppError{Code: "DOCUMENT_NOT_FOUND", Status: 404, Message: "Document not found"}
	ErrUnauthorized     = &AppError{Code: "UNAUTHORIZED", Status: 401, Message: "Unauthorized"}
	ErrForbidden        = &AppError{Code: "FORBIDDEN", Status: 403, Message: "Forbidden"}
	ErrInvalidRequest   = &AppError{Code: "INVALID_REQUEST", Status: 400, Message: "Invalid request"}
	ErrInternal         = &AppError{Code: "INTERNAL", Status: 500, Message: "Internal server error"}
)

// As extracts an *AppError from err, or maps unknown errors to ErrInternal.
func As(err error) *AppError {
	if err == nil {
		return nil
	}
	var ae *AppError
	if stderrors.As(err, &ae) {
		return ae
	}
	return ErrInternal.WithCause(err)
}
