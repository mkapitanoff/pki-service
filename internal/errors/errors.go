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
	// Details — machine-readable extra context for the client. Surfaced in
	// the response body as "error.details". Set via WithDetails.
	Details map[string]any
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
	ErrCMSInvalid        = &AppError{Code: "CMS_INVALID", Status: 422, Message: "CMS signature is invalid"}
	ErrCertRevoked       = &AppError{Code: "CERT_REVOKED", Status: 422, Message: "Certificate is revoked"}
	ErrDocumentNotFound  = &AppError{Code: "DOCUMENT_NOT_FOUND", Status: 404, Message: "Document not found"}
	ErrUnauthorized      = &AppError{Code: "UNAUTHORIZED", Status: 401, Message: "Unauthorized"}
	ErrForbidden         = &AppError{Code: "FORBIDDEN", Status: 403, Message: "Forbidden"}
	ErrInvalidRequest    = &AppError{Code: "INVALID_REQUEST", Status: 400, Message: "Invalid request"}
	ErrInternal          = &AppError{Code: "INTERNAL", Status: 500, Message: "Internal server error"}
	ErrEmailTaken        = &AppError{Code: "EMAIL_TAKEN", Status: 409, Message: "Email already registered"}
	ErrInvalidCredentials = &AppError{Code: "INVALID_CREDENTIALS", Status: 401, Message: "Invalid email or password"}
	ErrAlreadySigned     = &AppError{Code: "ALREADY_SIGNED", Status: 409, Message: "This person has already signed this document"}

	// CMS / signing — granular codes for Lovable integration debugging.
	ErrCMSBase64Invalid   = &AppError{Code: "INVALID_CMS_BASE64", Status: 422, Message: "CMS payload is not valid base64"}
	ErrCMSStructureInvalid = &AppError{Code: "INVALID_CMS_STRUCTURE", Status: 422, Message: "CMS payload could not be parsed"}
	ErrHashMismatch       = &AppError{Code: "HASH_MISMATCH", Status: 422, Message: "CMS messageDigest does not match document hash"}
	ErrCertNotTrusted     = &AppError{Code: "CERT_NOT_TRUSTED", Status: 422, Message: "Certificate chain is not trusted"}
	ErrSessionExpired     = &AppError{Code: "SESSION_EXPIRED", Status: 410, Message: "Signing session has expired"}
	ErrSessionNotFound    = &AppError{Code: "SESSION_NOT_FOUND", Status: 404, Message: "Signing session not found"}
	ErrDuplicateName      = &AppError{Code: "DUPLICATE_DOCUMENT_NAME", Status: 409, Message: "documents[].name must be unique within a session"}
	ErrPayloadTooLarge    = &AppError{Code: "PAYLOAD_TOO_LARGE", Status: 413, Message: "Request body exceeds the configured limit"}
)

// WithDetails returns a copy of the sentinel carrying machine-readable details
// for the client (e.g. {"name": "doc.pdf", "index": 3}).
func (e *AppError) WithDetails(details map[string]any) *AppError {
	cp := *e
	cp.Details = details
	return &cp
}

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
