package rest

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"cosmossdk.io/log"
	gcontext "github.com/gorilla/context"
	"github.com/gorilla/mux"
	"pkg.akt.dev/go/util/ctxlog"

	gwutils "github.com/akash-network/provider/gateway/utils"
	"github.com/akash-network/provider/utils/httperror"
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
		return "", httperror.ErrInvalidAuthHeader
	}

	return authHeaderParts[1], nil
}

// DefaultErrorHandler writes err as a JSON error response. Errors that are not
// *httperror.HttpError are logged with the logger found in the request context.
func DefaultErrorHandler(w http.ResponseWriter, r *http.Request, err error) {
	var logger log.Logger
	if r != nil {
		logger = ctxlog.LogcFromCtx(r.Context())
	} else {
		logger = log.NewNopLogger()
	}

	writeAuthError(logger, w, err)
}

func writeAuthError(logger log.Logger, w http.ResponseWriter, err error) {
	w.Header().Set("Content-Type", "application/json")

	ce := authErrorResponse(logger, err)
	w.WriteHeader(ce.StatusCode)
	body, _ := json.Marshal(map[string]string{"message": ce.Err.Error()})
	_, _ = w.Write(body)
}

func authErrorResponse(logger log.Logger, err error) *httperror.HttpError {
	if err == nil {
		panic("authErrorResponse called with nil error")
	}

	var httpError *httperror.HttpError
	if errors.As(err, &httpError) {
		return httpError
	}

	logger.Error("auth error", "err", err)
	return httperror.NewHttpError(http.StatusInternalServerError, errors.New("unknown error while processing JWT"))
}

// prepareAuthMiddleware returns an HTTP middleware that validates client identity
// through mTLS certificates or JWT tokens and injects authentication claims into the request context.
func prepareAuthMiddleware(logger log.Logger) mux.MiddlewareFunc {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			tokString, err := AuthHeaderTokenExtractor(r)
			if err != nil {
				// Error here means the Authorization header format was invalid, not that the token was absent.
				writeAuthError(logger, w, err)
				return
			}

			claims, err := gwutils.AuthProcess(r.Context(), r.TLS.PeerCertificates, tokString)
			if err != nil {
				writeAuthError(logger, w, err)
				return
			}

			gcontext.Set(r, claimsContextKey, claims)

			next.ServeHTTP(w, r)
		})
	}
}

// authorizeProviderMiddleware is an HTTP middleware that checks if a request is authorized
// based on the claims and provider context, returning an appropriate error if unauthorized.
func authorizeProviderMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		provider := requestProvider(r)
		claims := requestClaims(r)

		var err error
		if !claims.AuthorizeForProvider(provider) {
			err = httperror.ErrUnauthorized
		}

		if err != nil {
			DefaultErrorHandler(w, r, err)
			return
		}

		next.ServeHTTP(w, r)
	})
}
