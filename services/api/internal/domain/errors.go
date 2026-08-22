package domain

import (
	"errors"
	"net/http"
)

// Error is the domain error contract (PRD §11.2).
// Every rule violation from §8 maps to one of these codes.
type Error struct {
	Code    string            `json:"code"`
	Message string            `json:"message"`
	Status  int               `json:"-"`
	Fields  map[string]string `json:"fields,omitempty"`
}

func (e *Error) Error() string { return e.Code + ": " + e.Message }

func (e *Error) WithField(field, msg string) *Error {
	if e.Fields == nil {
		e.Fields = map[string]string{}
	}
	e.Fields[field] = msg
	return e
}

var (
	ErrValidation      = &Error{Code: "validation_error", Message: "Validation failed.", Status: http.StatusBadRequest}
	ErrEmailTaken      = &Error{Code: "email_taken", Message: "An account with this email already exists.", Status: http.StatusConflict}
	ErrUsernameTaken   = &Error{Code: "username_taken", Message: "This username is already taken.", Status: http.StatusConflict}
	ErrInvalidCreds    = &Error{Code: "invalid_credentials", Message: "Email or password is incorrect.", Status: http.StatusUnauthorized}
	ErrEmailNotVerified = &Error{Code: "email_not_verified", Message: "Verify your email before doing this.", Status: http.StatusForbidden}
	Err2FARequired      = &Error{Code: "2fa_required", Message: "Two-factor authentication is required.", Status: http.StatusUnauthorized}
	ErrAccountSuspended = &Error{Code: "account_suspended", Message: "Account temporarily suspended.", Status: http.StatusForbidden}
	ErrAccountBanned   = &Error{Code: "account_banned", Message: "Account banned.", Status: http.StatusForbidden}
	Err2FAEnrollmentRequired = &Error{Code: "admin_2fa_required", Message: "Enable two-factor authentication to use admin features (PRD §5.9.1).", Status: http.StatusForbidden}
	ErrNotAuthenticated = &Error{Code: "not_authenticated", Message: "Authentication required.", Status: http.StatusUnauthorized}
	ErrSessionInvalid  = &Error{Code: "session_invalid", Message: "Session expired or revoked. Sign in again.", Status: http.StatusUnauthorized}
	ErrTokenInvalid    = &Error{Code: "token_invalid", Message: "Token is invalid or expired.", Status: http.StatusBadRequest}
	ErrCSRF            = &Error{Code: "csrf_invalid", Message: "CSRF validation failed.", Status: http.StatusForbidden}
	ErrRateLimited     = &Error{Code: "rate_limited", Message: "Too many attempts. Try again soon.", Status: http.StatusTooManyRequests}
	ErrNotFound        = &Error{Code: "not_found", Message: "Resource not found.", Status: http.StatusNotFound}
	ErrForbidden       = &Error{Code: "forbidden", Message: "You don't have permission to do this.", Status: http.StatusForbidden}
	ErrInternal        = &Error{Code: "internal_error", Message: "Something went wrong.", Status: http.StatusInternalServerError}
)

// FromError normalizes any error into a domain Error for the HTTP layer.
func FromError(err error) *Error {
	if err == nil {
		return nil
	}
	var de *Error
	if errors.As(err, &de) {
		return de
	}
	return &Error{Code: ErrInternal.Code, Message: ErrInternal.Message, Status: ErrInternal.Status}
}
