package utils

import (
	"strings"

	"github.com/akash-network/provider/utils/httperror"
)

// AuthHeaderToken extracts a bearer token from Authorization header values.
func AuthHeaderToken(authHeaders []string) (string, error) {
	if len(authHeaders) == 0 {
		return "", nil
	}

	if len(authHeaders) != 1 {
		return "", httperror.ErrInvalidAuthHeader
	}

	authHeader := authHeaders[0]
	if authHeader == "" {
		return "", nil
	}

	authHeaderParts := strings.Fields(authHeader)
	if len(authHeaderParts) != 2 || strings.ToLower(authHeaderParts[0]) != "bearer" {
		return "", httperror.ErrInvalidAuthHeader
	}

	return authHeaderParts[1], nil
}
