package cmd

import (
	"fmt"
	"net"
	"net/url"
)

const gRPCDefaultPort = "8444"

func grpcURI(hostURI string) (string, error) {
	u, err := url.Parse(hostURI)
	if err != nil {
		return "", fmt.Errorf("url parse: %w", err)
	}

	h, _, err := net.SplitHostPort(u.Host)
	if err != nil {
		return "", fmt.Errorf("split host port: %w", err)
	}

	return net.JoinHostPort(h, gRPCDefaultPort), nil
}
