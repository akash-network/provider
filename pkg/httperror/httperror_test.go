package httperror

import (
	"errors"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestStatusCodeFrom(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want int
	}{
		{"nil", nil, http.StatusOK},
		{"generic", errors.New("fail"), http.StatusInternalServerError},
		{"custom_503", NewHttpError(http.StatusServiceUnavailable, errors.New("refused")), http.StatusServiceUnavailable},
		{"custom_500", NewHttpError(500, errors.New("fail")), http.StatusInternalServerError},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, StatusCodeFrom(tt.err))
		})
	}
}
