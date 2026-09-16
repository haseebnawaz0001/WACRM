// Package safehttp builds HTTP clients that will not call back into the
// network the server is sitting on (plan 10, X12).
//
// Every outbound HTTP target in the product — webhook deliveries, custom
// actions, automation's call_webhook, IVR http_callback nodes — is a URL an
// organization typed in. Without a guard, "https://internal.metadata/" is a
// perfectly acceptable webhook URL and the server will fetch it and hand the
// body back, which turns an admin-configured field into a read primitive
// against the private network.
//
// Two layers are needed, not one:
//
//   - ValidateURL rejects the obvious cases when a URL is saved, so an
//     operator gets an error in the form rather than a silent failure later.
//   - Dialer re-checks after DNS resolution, at connect time. Structural
//     validation alone cannot stop a hostname that resolves to 169.254.169.254
//     only on the second lookup (DNS rebinding), because the name looks public.
//
// This package exists so the two layers have exactly one implementation.
// internal/handlers cannot host them: internal/calling needs them too, and
// handlers already imports calling, so the dependency would cycle.
package safehttp

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// blocked reports whether an IP is somewhere a user-configured URL has no
// business reaching.
func blocked(ip net.IP) bool {
	return ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() ||
		ip.IsLinkLocalMulticast() || ip.IsUnspecified()
}

// ValidateURL performs structural validation of an outbound URL. It blocks
// known-internal hostnames and IP literals pointing at private ranges.
// Runtime protection against DNS rebinding is Dialer's job.
func ValidateURL(rawURL string) error {
	u, err := url.Parse(rawURL)
	if err != nil {
		return fmt.Errorf("invalid URL: %w", err)
	}

	if u.Scheme != "https" && u.Scheme != "http" {
		return fmt.Errorf("URL scheme must be http or https")
	}

	hostname := u.Hostname()
	if hostname == "" {
		return fmt.Errorf("URL must have a hostname")
	}

	lower := strings.ToLower(hostname)
	if lower == "localhost" || lower == "0.0.0.0" || strings.HasSuffix(lower, ".local") ||
		strings.HasSuffix(lower, ".internal") {
		return fmt.Errorf("URL must not point to internal addresses")
	}

	if ip := net.ParseIP(hostname); ip != nil && blocked(ip) {
		return fmt.Errorf("URL must not point to internal addresses")
	}

	return nil
}

// Dialer returns a DialContext that refuses to connect to private or loopback
// addresses. It resolves the host itself and dials the address it checked, so
// a name that resolves differently between the check and the connection cannot
// slip past.
func Dialer() func(ctx context.Context, network, addr string) (net.Conn, error) {
	dialer := &net.Dialer{Timeout: 10 * time.Second}
	return func(ctx context.Context, network, addr string) (net.Conn, error) {
		host, port, err := net.SplitHostPort(addr)
		if err != nil {
			return nil, err
		}

		ips, err := net.DefaultResolver.LookupHost(ctx, host)
		if err != nil {
			return nil, err
		}

		for _, ipStr := range ips {
			ip := net.ParseIP(ipStr)
			if ip == nil {
				continue
			}
			if blocked(ip) {
				return nil, fmt.Errorf("connection to private address %s is not allowed", ipStr)
			}
		}

		// Connect to the first resolved IP — the one that was just checked,
		// not a fresh lookup that could return something else.
		return dialer.DialContext(ctx, network, net.JoinHostPort(ips[0], port))
	}
}

// NewTransport returns a pooled transport that dials safely.
func NewTransport() *http.Transport {
	return &http.Transport{
		DialContext:         Dialer(),
		MaxIdleConns:        100,
		MaxIdleConnsPerHost: 10,
		IdleConnTimeout:     90 * time.Second,
	}
}

// NewClient returns an HTTP client for calling a user-configured URL.
func NewClient(timeout time.Duration) *http.Client {
	return &http.Client{Timeout: timeout, Transport: NewTransport()}
}
