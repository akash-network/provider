package utils

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"os"
	"time"

	"github.com/golang-jwt/jwt/v5"

	ajwt "github.com/akash-network/akash-api/go/util/jwt"
	atls "github.com/akash-network/akash-api/go/util/tls"
	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/akash-network/provider/tools/fromctx"
)

var (
	ErrAuthAmbiguous = errors.New("auth: ambiguous authentication. may not use mTLS and JWT at the same time")
	ErrJWTInvalid    = errors.New("jwt: invalid")
	ErrJWTMissing    = errors.New("jwt: missing")
)

type CertGetter interface {
	GetMTLS(ctx context.Context) ([]tls.Certificate, error)
	GetCACerts(ctx context.Context, domain string) ([]tls.Certificate, error)
	atls.CertificateQuerier
}

func NewServerTLSConfig(ctx context.Context, cquery CertGetter, sni string) (*tls.Config, error) {
	// todo ideally we want here configs to be cached to speed up tls handshake
	// @troian to look at that
	cfg := &tls.Config{
		MinVersion: tls.VersionTLS13,
		GetConfigForClient: func(info *tls.ClientHelloInfo) (*tls.Config, error) {
			var rcfg *tls.Config

			mtls := true
			if info.ServerName == sni {
				currCerts, err := cquery.GetCACerts(info.Context(), sni)
				if err == nil && len(currCerts) > 0 {
					mtls = false
					rcfg = &tls.Config{
						Certificates: currCerts,
						MinVersion:   tls.VersionTLS13,
					}
				}
			}

			if mtls {
				currCerts, err := cquery.GetMTLS(info.Context())
				if err != nil {
					return nil, err
				}

				rcfg = &tls.Config{
					Certificates: currCerts,
					MinVersion:   tls.VersionTLS13,
					ClientAuth:   tls.RequestClientCert,
					VerifyPeerCertificate: func(certificates [][]byte, _ [][]*x509.Certificate) error {
						if len(certificates) > 0 {
							peerCerts := make([]*x509.Certificate, 0, len(certificates))

							for idx := range certificates {
								cert, err := x509.ParseCertificate(certificates[idx])
								if err != nil {
									return err
								}

								peerCerts = append(peerCerts, cert)
							}

							_, _, err := atls.ValidatePeerCertificates(ctx, cquery, peerCerts, []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth})
							if err != nil {
								return err
							}
						}

						return nil
					},
				}
			}

			return rcfg, nil
		},
	}

	return cfg, nil
}

func AuthProcess(ctx context.Context, peerCerts []*x509.Certificate, token string) (*ajwt.Claims, error) {
	claims := &ajwt.Claims{
		Leases: ajwt.Leases{
			Access: ajwt.AccessTypeNone,
		},
	}

	// if a client provides certificate it is mTLS authentication
	// and the certificate has been verified during TLS handshake
	if len(peerCerts) == 1 {
		owner, err := sdk.AccAddressFromBech32(peerCerts[0].Subject.CommonName)
		if err != nil {
			return nil, err
		}

		// authentication via mTLS does not provider granular access
		now := time.Now()
		claims.Issuer = owner.String()
		claims.Version = "v1"
		claims.IssuedAt = jwt.NewNumericDate(now)
		claims.NotBefore = jwt.NewNumericDate(now)
		claims.ExpiresAt = jwt.NewNumericDate(now.Add(15 * time.Minute))
		claims.Leases.Access = ajwt.AccessTypeFull

		err = claims.Validate()
		if err != nil {
			return nil, err
		}
	}

	if (claims.Leases.Access != ajwt.AccessTypeNone) && (token != "") {
		return nil, ErrAuthAmbiguous
	}

	if token != "" {
		// reset claims if token is provided
		claims = &ajwt.Claims{}

		token, err := jwt.ParseWithClaims(token, claims, func(token *jwt.Token) (interface{}, error) {
			issStr, err := token.Claims.GetIssuer()
			if err != nil {
				return nil, err
			}

			iss, err := sdk.AccAddressFromBech32(issStr)
			if err != nil {
				return nil, err
			}

			pstorage, err := fromctx.AccountQuerierFromCtx(ctx)
			if err != nil {
				return nil, err
			}

			pk, err := pstorage.GetAccountPublicKey(ctx, iss)
			if err != nil {
				if errors.Is(err, os.ErrNotExist) {
					return nil, ErrJWTInvalid
				}
				return nil, err
			}

			verifier := ajwt.NewVerifier(pk, iss)

			return verifier, nil
		}, jwt.WithValidMethods([]string{"ES256K", "ES256KADR36"}))

		if err == nil && !token.Valid {
			err = ErrJWTInvalid
		}

		if err != nil {
			return nil, err
		}
	}

	return claims, nil
}
