package cmd

import (
	"fmt"
	"net"
)

func grpcURI(hostURI string) (string, error) {
	host, _, err := net.SplitHostPort(hostURI)
	if err != nil {
		return "", fmt.Errorf("split host port: %w", err)
	}

	return net.JoinHostPort(host, "8442"), nil
}
