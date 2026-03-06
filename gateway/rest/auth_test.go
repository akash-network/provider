package rest

import (
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/golang-jwt/jwt/v5"
	"github.com/stretchr/testify/require"
)

func TestDefaultErrorHandler(t *testing.T) {
	tests := []struct {
		name           string
		err            error
		expectedStatus int
		bodyContains   string
	}{
		{
			name:           "jwt_missing",
			err:            ErrJWTMissing,
			expectedStatus: http.StatusBadRequest,
			bodyContains:   "JWT is missing",
		},
		{
			name:           "jwt_invalid",
			err:            ErrJWTInvalid,
			expectedStatus: http.StatusUnauthorized,
			bodyContains:   "JWT is invalid",
		},
		{
			name:           "unauthorized",
			err:            ErrUnauthorized,
			expectedStatus: http.StatusUnauthorized,
			bodyContains:   "unauthorized access",
		},
		{
			name:           "invalid_request",
			err:            ErrInvalidRequest,
			expectedStatus: http.StatusBadRequest,
			bodyContains:   "invalid request",
		},
		{
			name:           "jwt_expired",
			err:            jwt.ErrTokenExpired,
			expectedStatus: http.StatusUnauthorized,
			bodyContains:   "JWT is expired",
		},
		{
			name:           "jwt_invalid_claims",
			err:            jwt.ErrTokenInvalidClaims,
			expectedStatus: http.StatusBadRequest,
			bodyContains:   "JWT has invalid claims",
		},
		{
			name:           "jwt_not_valid_yet",
			err:            jwt.ErrTokenNotValidYet,
			expectedStatus: http.StatusUnauthorized,
			bodyContains:   "JWT is invalid",
		},
		{
			name:           "jwt_used_before_issued",
			err:            jwt.ErrTokenUsedBeforeIssued,
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
			err:            fmt.Errorf("context: %w", ErrJWTMissing),
			expectedStatus: http.StatusBadRequest,
			bodyContains:   "JWT is missing",
		},
		{
			name:           "wrapped_err_token_expired",
			err:            fmt.Errorf("validation: %w", jwt.ErrTokenExpired),
			expectedStatus: http.StatusUnauthorized,
			bodyContains:   "JWT is expired",
		},
		{
			name:           "invalid_auth_header",
			err:            ErrInvalidAuthHeader,
			expectedStatus: http.StatusBadRequest,
			bodyContains:   "invalid authorization header",
		},
		{
			name:           "wrapped_err_invalid_auth_header",
			err:            fmt.Errorf("error extracting token: %w", ErrInvalidAuthHeader),
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
