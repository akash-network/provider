package common

import (
	"errors"
	"net/http"
)

// ListenAndServe runs srv.ListenAndServe and reports a graceful shutdown
// (http.ErrServerClosed) as success, so operators exit cleanly on shutdown.
func ListenAndServe(srv *http.Server) error {
	if err := srv.ListenAndServe(); !errors.Is(err, http.ErrServerClosed) {
		return err
	}

	return nil
}
