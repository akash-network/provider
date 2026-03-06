package rest

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"strings"

	"github.com/golang-jwt/jwt/v5"
	gcontext "github.com/gorilla/context"

	gwutils "github.com/akash-network/provider/gateway/utils"
)

var (
	ErrJWTMissing        = errors.New("jwt: missing")
	ErrJWTInvalid        = errors.New("jwt: invalid")
	ErrInvalidRequest    = errors.New("invalid request")
	ErrUnauthorized      = errors.New("unauthorized")
	ErrInvalidAuthHeader = errors.New("authorization header format must be Bearer {token}")
)

// AuthHeaderTokenExtractor is a TokenExtractor that takes a request
// and extracts the token from the Authorization header.
func AuthHeaderTokenExtractor(r *http.Request) (string, error) {
	authHeader := r.Header.Get("Authorization")
	if authHeader == "" {
		return "", nil // No error, just no JWT.
	}

	authHeaderParts := strings.Fields(authHeader)
	if len(authHeaderParts) != 2 || strings.ToLower(authHeaderParts[0]) != "bearer" {
		return "", ErrInvalidAuthHeader
	}

	return authHeaderParts[1], nil
}

func DefaultErrorHandler(w http.ResponseWriter, _ *http.Request, err error) {
	w.Header().Set("Content-Type", "application/json")

	code, msg := authErrorResponse(err)
	w.WriteHeader(code)
	body, _ := json.Marshal(map[string]string{"message": msg})
	_, _ = w.Write(body)
}

func authErrorResponse(err error) (int, string) {
	if err == nil {
		panic("authErrorResponse called with nil error")
	}
	switch {
	case errors.Is(err, ErrJWTMissing):
		return http.StatusBadRequest, "JWT is missing"
	case errors.Is(err, ErrInvalidRequest):
		return http.StatusBadRequest, "invalid request"
	case errors.Is(err, jwt.ErrTokenInvalidClaims):
		return http.StatusBadRequest, "JWT has invalid claims"
	case errors.Is(err, ErrUnauthorized):
		return http.StatusUnauthorized, "unauthorized access"
	case errors.Is(err, ErrJWTInvalid), errors.Is(err, jwt.ErrTokenNotValidYet), errors.Is(err, jwt.ErrTokenUsedBeforeIssued):
		return http.StatusUnauthorized, "JWT is invalid"
	case errors.Is(err, jwt.ErrTokenExpired):
		return http.StatusUnauthorized, "JWT is expired"
	case errors.Is(err, ErrInvalidAuthHeader):
		return http.StatusBadRequest, "invalid authorization header"
	default:
		log.Printf("auth error: %v", err)
		return http.StatusInternalServerError, "unknown error while processing JWT"
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
