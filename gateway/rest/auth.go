package rest

import (
	"errors"
	"fmt"
	"net/http"
	"strings"

	gcontext "github.com/gorilla/context"

	gwutils "github.com/akash-network/provider/gateway/utils"
)

var (
	// ErrJWTMissing is returned when the JWT is missing.
	ErrJWTMissing = errors.New("jwt: missing")

	// ErrJWTInvalid is returned when the JWT is invalid.
	ErrJWTInvalid     = errors.New("jwt: invalid")
	ErrInvalidRequest = errors.New("invalid request")
	ErrUnauthorized   = errors.New("unauthorized")
)

// invalidError handles wrapping a JWT validation error with
// the concrete error ErrJWTInvalid. We do not expose this
// publicly because the interface methods of Is and Unwrap
// should give the user all they need.
type invalidError struct {
	details error
}

// Is allows the error to support equality to ErrJWTInvalid.
func (e invalidError) Is(target error) bool {
	return target == ErrJWTInvalid
}

// Error returns a string representation of the error.
func (e invalidError) Error() string {
	return fmt.Sprintf("%s: %s", ErrJWTInvalid, e.details)
}

// Unwrap allows the error to support equality to the
// underlying error and not just ErrJWTInvalid.
func (e invalidError) Unwrap() error {
	return e.details
}

// AuthHeaderTokenExtractor is a TokenExtractor that takes a request
// and extracts the token from the Authorization header.
func AuthHeaderTokenExtractor(r *http.Request) (string, error) {
	authHeader := r.Header.Get("Authorization")
	if authHeader == "" {
		return "", nil // No error, just no JWT.
	}

	authHeaderParts := strings.Fields(authHeader)
	if len(authHeaderParts) != 2 || strings.ToLower(authHeaderParts[0]) != "bearer" {
		return "", errors.New("authorization header format must be Bearer {token}")
	}

	return authHeaderParts[1], nil
}

func DefaultErrorHandler(w http.ResponseWriter, _ *http.Request, err error) {
	w.Header().Set("Content-Type", "application/json")

	switch {
	case errors.Is(err, ErrJWTMissing):
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"message":"JWT is missing"}`))
	case errors.Is(err, ErrJWTInvalid):
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"message":"JWT is invalid"}`))
	case errors.Is(err, ErrUnauthorized):
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"message":"unauthorized access"}`))
	case errors.Is(err, ErrInvalidRequest):
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"message":"invalid request"}`))
	default:
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(fmt.Sprintf(`{"message":"unknown error while processing JWT. %s"}`, err.Error())))
	}
}

// prepareAuthMiddleware returns an HTTP middleware that validates client identity
// through mTLS certificates or JWT tokens and injects authentication claims into the request context.
func prepareAuthMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		tokString, err := AuthHeaderTokenExtractor(r)
		if err != nil {
			// This is not ErrJWTMissing because an error here means that the
			// tokenExtractor had an error and _not_ that the token was missing.
			DefaultErrorHandler(w, r, fmt.Errorf("error extracting token: %w", err))
			return
		}

		claims, err := gwutils.AuthProcess(r.Context(), r.TLS.PeerCertificates, tokString)
		if err != nil {
			DefaultErrorHandler(w, r, err)
			return
		}

		gcontext.Set(r, claimsContextKey, claims)

		next.ServeHTTP(w, r)
	})
}

// authorizeProviderMiddleware is an HTTP middleware that checks if a request is authorized
// based on the claims and provider context, returning an appropriate error if unauthorized.
func authorizeProviderMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		provider := requestProvider(r)
		claims := requestClaims(r)

		var err error
		if !claims.AuthorizeForProvider(provider) {
			err = ErrUnauthorized
		}

		if err != nil {
			DefaultErrorHandler(w, r, err)
			return
		}

		next.ServeHTTP(w, r)
	})
}
