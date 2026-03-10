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
	ErrJWTMissing        = NewHttpError(http.StatusBadRequest, errors.New("JWT is missing"))
	ErrInvalidAuthHeader = NewHttpError(http.StatusBadRequest, errors.New("invalid authorization header"))
	ErrInvalidRequest    = NewHttpError(http.StatusBadRequest, errors.New("invalid request"))
	ErrJWTInvalidClaims  = NewHttpError(http.StatusBadRequest, errors.New("JWT has invalid claims"))
	ErrAuthAmbiguous     = NewHttpError(http.StatusBadRequest, errors.New("auth: ambiguous authentication. may not use mTLS and JWT at the same time"))

	ErrUnauthorized = NewHttpError(http.StatusUnauthorized, errors.New("unauthorized access"))
	ErrJWTInvalid   = NewHttpError(http.StatusUnauthorized, errors.New("JWT is invalid"))
	ErrJWTExpired   = NewHttpError(http.StatusUnauthorized, errors.New("JWT is expired"))
)

// HttpError wraps an error with the appropriate HTTP status code.
type HttpError struct {
	Err        error
	StatusCode int
}

func (e *HttpError) Error() string {
	return e.Err.Error()
}

func (e *HttpError) Unwrap() error {
	return e.Err
}

func NewHttpError(statusCode int, err error) *HttpError {
	return &HttpError{Err: err, StatusCode: statusCode}
}

func StatusCodeFrom(err error) int {
	if err == nil {
		return http.StatusOK
	}
	var ce *HttpError
	if errors.As(err, &ce) {
		return ce.StatusCode
	}
	return http.StatusInternalServerError
}
