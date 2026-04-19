package commands

import (
	"fmt"
	"net"
	"strings"
)

func localhostHTTPURL(addr string) (string, error) {
	return localHTTPURL(addr, "localhost")
}

func loopbackHTTPURL(addr string) (string, error) {
	return localHTTPURL(addr, "127.0.0.1")
}

func localHTTPURL(addr, host string) (string, error) {
	addr = strings.TrimSpace(addr)
	if addr == "" {
		return "", fmt.Errorf("http address is empty")
	}

	_, port, err := net.SplitHostPort(addr)
	if err != nil {
		return "", fmt.Errorf("parse http address %q: %w", addr, err)
	}
	if port == "" {
		return "", fmt.Errorf("http address %q has no port", addr)
	}

	return "http://" + net.JoinHostPort(host, port), nil
}
