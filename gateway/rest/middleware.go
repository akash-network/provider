package rest

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/gorilla/context"
	"github.com/gorilla/mux"

	ajwt "pkg.akt.dev/go/util/jwt"

	sdk "github.com/cosmos/cosmos-sdk/types"
	dtypes "pkg.akt.dev/go/node/deployment/v1"
	mtypes "pkg.akt.dev/go/node/market/v1"
	mquery "pkg.akt.dev/node/x/market/query"
)

type contextKey int

const (
	leaseContextKey contextKey = iota + 1
	deploymentContextKey
	logFollowContextKey
	tailLinesContextKey
	serviceContextKey
	ownerContextKey
	providerContextKey
	servicesContextKey
	claimsContextKey
)

func requestLeaseID(req *http.Request) mtypes.LeaseID {
	return context.Get(req, leaseContextKey).(mtypes.LeaseID)
}

func requestLogFollow(req *http.Request) bool {
	return context.Get(req, logFollowContextKey).(bool)
}

func requestLogTailLines(req *http.Request) *int64 {
	return context.Get(req, tailLinesContextKey).(*int64)
}

func requestService(req *http.Request) string {
	return context.Get(req, serviceContextKey).(string)
}

func requestServices(req *http.Request) string {
	return context.Get(req, servicesContextKey).(string)
}

func requestProvider(req *http.Request) sdk.Address {
	return context.Get(req, providerContextKey).(sdk.Address)
}

func requestOwner(req *http.Request) sdk.Address {
	return context.Get(req, ownerContextKey).(sdk.Address)
}

func requestClaims(req *http.Request) *ajwt.Claims {
	return context.Get(req, claimsContextKey).(*ajwt.Claims)
}

func requestDeploymentID(req *http.Request) dtypes.DeploymentID {
	return context.Get(req, deploymentContextKey).(dtypes.DeploymentID)
}

func requireEndpointScopeForDeploymentID(scope ajwt.PermissionScope) mux.MiddlewareFunc {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			claims := requestClaims(r)
			did := requestDeploymentID(r)
			provider := requestProvider(r)

			if !claims.AuthorizeDeploymentIDForPermissionScope(did, provider, scope) {
				DefaultErrorHandler(w, r, ErrUnauthorized)
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}

func requireEndpointScopeForLeaseID(scope ajwt.PermissionScope) mux.MiddlewareFunc {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			claims := requestClaims(r)
			lid := requestLeaseID(r)

			if !claims.AuthorizeLeaseIDForPermissionScope(lid, scope) {
				DefaultErrorHandler(w, r, ErrUnauthorized)
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}

func requireOwner(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		claims := requestClaims(r)

		if claims.IssuerAddress().Empty() {
			DefaultErrorHandler(w, r, ErrUnauthorized)
			return
		}

		context.Set(r, ownerContextKey, claims.IssuerAddress())

		next.ServeHTTP(w, r)
	})
}

// func requireDeploymentID() mux.MiddlewareFunc {
func requireDeploymentID(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id, err := parseDeploymentID(r)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		claims := requestClaims(r)
		provider := requestProvider(r)

		if !claims.AuthorizeForDeploymentID(id, provider) {
			DefaultErrorHandler(w, r, ErrUnauthorized)
			return
		}

		context.Set(r, deploymentContextKey, id)

		next.ServeHTTP(w, r)
	})
}

func requireLeaseID(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id, err := parseLeaseID(r)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		claims := requestClaims(r)
		if !claims.AuthorizeForLeaseID(id) {
			DefaultErrorHandler(w, r, ErrUnauthorized)
			return
		}

		context.Set(r, leaseContextKey, id)
		next.ServeHTTP(w, r)
	})
}

func requireService() mux.MiddlewareFunc {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			vars := mux.Vars(req)

			svc := vars["serviceName"]
			if svc == "" {
				http.Error(w, "empty service name", http.StatusBadRequest)
				return
			}

			context.Set(req, serviceContextKey, svc)
			next.ServeHTTP(w, req)
		})
	}
}

func parseDeploymentID(req *http.Request) (dtypes.DeploymentID, error) {
	var parts []string
	parts = append(parts, requestOwner(req).String())
	parts = append(parts, mux.Vars(req)["dseq"])
	return dtypes.ParseDeploymentPath(parts)
}

func parseLeaseID(req *http.Request) (mtypes.LeaseID, error) {
	vars := mux.Vars(req)

	parts := []string{
		requestOwner(req).String(),
		vars["dseq"],
		vars["gseq"],
		vars["oseq"],
		requestProvider(req).String(),
	}

	return mquery.ParseLeasePath(parts)
}

func requestStreamParams(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		vars := req.URL.Query()

		var err error

		defer func() {
			if err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
		}()

		var tailLines *int64

		services := vars.Get("service")
		if strings.HasSuffix(services, ",") {
			err = fmt.Errorf("parameter \"service\" must not contain trailing comma")
			return
		}

		follow := false

		if val := vars.Get("follow"); val != "" {
			follow, err = strconv.ParseBool(val)
			if err != nil {
				return
			}
		}

		vl := new(int64)
		if val := vars.Get("tail"); val != "" {
			*vl, err = strconv.ParseInt(val, 10, 32)
			if err != nil {
				return
			}

			if *vl < -1 {
				err = fmt.Errorf("parameter \"tail\" contains invalid value")
				return
			}
		} else {
			*vl = -1
		}

		if *vl > -1 {
			tailLines = vl
		}

		context.Set(req, logFollowContextKey, follow)
		context.Set(req, tailLinesContextKey, tailLines)
		context.Set(req, servicesContextKey, services)

		next.ServeHTTP(w, req)
	})
}
