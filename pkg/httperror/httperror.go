// Package httperror provides a shared error type that carries HTTP status codes
// for gateway responses. The gateway uses StatusCodeFrom(err) only.
//
// Domain-specific mappers (wrap errors before returning to gateway):
//   - cluster/kube/errors.WrapClusterErrorForGateway
//   - manifest.WrapSubmitErrorForGateway
//   - gateway/utils.WrapAuthErrorForGateway (JWT parse errors)
package httperror

import (
	"errors"
	"net/http"
)

var (
	ErrJWTMissing        = NewError(http.StatusBadRequest, errors.New("JWT is missing"))
	ErrInvalidAuthHeader = NewError(http.StatusBadRequest, errors.New("invalid authorization header"))
	ErrInvalidRequest    = NewError(http.StatusBadRequest, errors.New("invalid request"))
	ErrJWTInvalidClaims  = NewError(http.StatusBadRequest, errors.New("JWT has invalid claims"))
	ErrAuthAmbiguous     = NewError(http.StatusBadRequest, errors.New("auth: ambiguous authentication. may not use mTLS and JWT at the same time"))

	ErrUnauthorized = NewError(http.StatusUnauthorized, errors.New("unauthorized access"))
	ErrJWTInvalid   = NewError(http.StatusUnauthorized, errors.New("JWT is invalid"))
	ErrJWTExpired   = NewError(http.StatusUnauthorized, errors.New("JWT is expired"))
)

// CustomError wraps an error with the appropriate HTTP status code.
type CustomError struct {
	Err        error
	StatusCode int
}

func (e *CustomError) Error() string {
	return e.Err.Error()
}

func (e *CustomError) Unwrap() error {
	return e.Err
}

func NewError(statusCode int, err error) *CustomError {
	return &CustomError{Err: err, StatusCode: statusCode}
}

func StatusCodeFrom(err error) int {
	if err == nil {
		return http.StatusOK
	}
	var ce *CustomError
	if errors.As(err, &ce) {
		return ce.StatusCode
	}
	return http.StatusInternalServerError
}
