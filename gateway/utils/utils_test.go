// Tests assert JWT error→status mapping (jwtErrorToHTTP) and AuthProcess ErrAuthAmbiguous, no-token→claims.
package utils

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"errors"
	"math/big"
	"net/http"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/stretchr/testify/require"

	ajwt "pkg.akt.dev/go/util/jwt"

	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/akash-network/provider/utils/httperror"
)

func TestJwtErrorToHTTP(t *testing.T) {
	tests := []struct {
		name           string
		token          *jwt.Token
		err            error
		expectedStatus int
		expectedMsg    string
	}{
		{
			name:           "nil_err_nil_token",
			token:          nil,
			err:            nil,
			expectedStatus: http.StatusUnauthorized,
			expectedMsg:    "JWT is invalid",
		},
		{
			name:           "nil_err_invalid_token",
			token:          &jwt.Token{Valid: false},
			err:            nil,
			expectedStatus: http.StatusUnauthorized,
			expectedMsg:    "JWT is invalid",
		},
		{
			name:           "token_invalid_claims",
			token:          &jwt.Token{Valid: false},
			err:            jwt.ErrTokenInvalidClaims,
			expectedStatus: http.StatusBadRequest,
			expectedMsg:    "JWT has invalid claims",
		},
		{
			name:           "token_expired",
			token:          &jwt.Token{Valid: false},
			err:            jwt.ErrTokenExpired,
			expectedStatus: http.StatusUnauthorized,
			expectedMsg:    "JWT is expired",
		},
		{
			name:           "token_not_valid_yet",
			token:          &jwt.Token{Valid: false},
			err:            jwt.ErrTokenNotValidYet,
			expectedStatus: http.StatusUnauthorized,
			expectedMsg:    "JWT is invalid",
		},
		{
			name:           "token_used_before_issued",
			token:          &jwt.Token{Valid: false},
			err:            jwt.ErrTokenUsedBeforeIssued,
			expectedStatus: http.StatusUnauthorized,
			expectedMsg:    "JWT is invalid",
		},
		{
			name:           "unknown_jwt_error",
			token:          nil,
			err:            jwt.ErrTokenSignatureInvalid,
			expectedStatus: http.StatusUnauthorized,
			expectedMsg:    "JWT is invalid",
		},
		{
			name:           "wrapped_err_token_expired",
			token:          nil,
			err:            errors.Join(jwt.ErrTokenExpired, errors.New("context")),
			expectedStatus: http.StatusUnauthorized,
			expectedMsg:    "JWT is expired",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ce := jwtErrorToHTTP(tt.token, tt.err)
			require.NotNil(t, ce)
			require.Equal(t, tt.expectedStatus, ce.StatusCode)
			require.Contains(t, ce.Error(), tt.expectedMsg)
		})
	}
}

func TestAuthProcess_ErrAuthAmbiguous(t *testing.T) {
	addr := sdk.AccAddress(make([]byte, 20)).String()
	cert := mustCreateCertWithCN(t, addr)
	ctx := context.Background()

	_, err := AuthProcess(ctx, []*x509.Certificate{cert}, "some-token")
	require.Error(t, err)
	require.ErrorIs(t, err, httperror.ErrAuthAmbiguous)
}

func TestAuthProcess_NoTokenReturnsClaims(t *testing.T) {
	ctx := context.Background()
	claims, err := AuthProcess(ctx, nil, "")
	require.NoError(t, err)
	require.NotNil(t, claims)
	require.Equal(t, ajwt.AccessTypeNone, claims.Leases.Access)
}

func mustCreateCertWithCN(t *testing.T, cn string) *x509.Certificate {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: cn},
		NotBefore:    time.Now(),
		NotAfter:     time.Now().Add(24 * time.Hour),
	}
	certDER, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	require.NoError(t, err)
	cert, err := x509.ParseCertificate(certDER)
	require.NoError(t, err)
	return cert
}
