package rest

import (
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/akash-network/provider/tools/logging/logtest"
	"github.com/akash-network/provider/utils/httperror"
)

func TestAuthErrorResponseLogsUnknownError(t *testing.T) {
	rec := logtest.New()
	err := errors.New("backend exploded")

	res := authErrorResponse(rec, err)

	require.Equal(t, http.StatusInternalServerError, res.StatusCode)
	entries := rec.Entries()
	require.Len(t, entries, 1)
	require.Equal(t, "error", entries[0].Level)
	require.Equal(t, err, entries[0].KV["err"])
}

func TestAuthErrorResponseDoesNotLogHTTPErrors(t *testing.T) {
	rec := logtest.New()

	res := authErrorResponse(rec, httperror.ErrJWTInvalid)

	require.Equal(t, http.StatusUnauthorized, res.StatusCode)
	require.Empty(t, rec.Entries())
}

func TestDefaultErrorHandler(t *testing.T) {
	tests := []struct {
		name           string
		err            error
		expectedStatus int
		bodyContains   string
	}{
		{
			name:           "jwt_missing",
			err:            httperror.ErrJWTMissing,
			expectedStatus: http.StatusBadRequest,
			bodyContains:   "JWT is missing",
		},
		{
			name:           "jwt_invalid",
			err:            httperror.ErrJWTInvalid,
			expectedStatus: http.StatusUnauthorized,
			bodyContains:   "JWT is invalid",
		},
		{
			name:           "unauthorized",
			err:            httperror.ErrUnauthorized,
			expectedStatus: http.StatusUnauthorized,
			bodyContains:   "unauthorized access",
		},
		{
			name:           "invalid_request",
			err:            httperror.ErrInvalidRequest,
			expectedStatus: http.StatusBadRequest,
			bodyContains:   "invalid request",
		},
		{
			name:           "jwt_expired",
			err:            httperror.ErrJWTExpired,
			expectedStatus: http.StatusUnauthorized,
			bodyContains:   "JWT is expired",
		},
		{
			name:           "jwt_invalid_claims",
			err:            httperror.ErrJWTInvalidClaims,
			expectedStatus: http.StatusBadRequest,
			bodyContains:   "JWT has invalid claims",
		},
		{
			name:           "jwt_not_valid_yet",
			err:            httperror.ErrJWTInvalid,
			expectedStatus: http.StatusUnauthorized,
			bodyContains:   "JWT is invalid",
		},
		{
			name:           "jwt_used_before_issued",
			err:            httperror.ErrJWTInvalid,
			expectedStatus: http.StatusUnauthorized,
			bodyContains:   "JWT is invalid",
		},
		{
			name:           "unknown_error",
			err:            errors.New("some error"),
			expectedStatus: http.StatusInternalServerError,
			bodyContains:   "unknown error while processing JWT",
		},
		{
			name:           "unknown_error_generic_message",
			err:            errors.New("custom error detail"),
			expectedStatus: http.StatusInternalServerError,
			bodyContains:   "unknown error while processing JWT",
		},
		{
			name:           "wrapped_err_jwt_missing",
			err:            fmt.Errorf("context: %w", httperror.ErrJWTMissing),
			expectedStatus: http.StatusBadRequest,
			bodyContains:   "JWT is missing",
		},
		{
			name:           "wrapped_err_token_expired",
			err:            fmt.Errorf("validation: %w", httperror.ErrJWTExpired),
			expectedStatus: http.StatusUnauthorized,
			bodyContains:   "JWT is expired",
		},
		{
			name:           "invalid_auth_header",
			err:            httperror.ErrInvalidAuthHeader,
			expectedStatus: http.StatusBadRequest,
			bodyContains:   "invalid authorization header",
		},
		{
			name:           "wrapped_err_invalid_auth_header",
			err:            fmt.Errorf("error extracting token: %w", httperror.ErrInvalidAuthHeader),
			expectedStatus: http.StatusBadRequest,
			bodyContains:   "invalid authorization header",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			DefaultErrorHandler(w, nil, tt.err)
			require.Equal(t, tt.expectedStatus, w.Code)
			if tt.bodyContains != "" {
				require.Contains(t, w.Body.String(), tt.bodyContains)
			}
		})
	}
}
