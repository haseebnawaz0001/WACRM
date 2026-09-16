package calling

import (
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/shridarpatil/whatomate/internal/safehttp"
	"github.com/shridarpatil/whatomate/internal/templating"
)

// HTTPCallbackResult holds the response from an HTTP callback.
type HTTPCallbackResult struct {
	StatusCode int
	Body       string
}

// callbackClients caches one safe client per timeout, so an IVR flow running
// on every call does not build a fresh connection pool each time.
var (
	callbackMu      sync.Mutex
	callbackClients = map[time.Duration]*http.Client{}
)

// callbackClient returns the shared SSRF-safe client for a timeout.
func callbackClient(timeout time.Duration) *http.Client {
	callbackMu.Lock()
	defer callbackMu.Unlock()
	if c, ok := callbackClients[timeout]; ok {
		return c
	}
	c := safehttp.NewClient(timeout)
	callbackClients[timeout] = c
	return c
}

// executeHTTPCallback performs an HTTP request with configurable method, headers, and body.
//
// The URL is admin-configured via the IVR flow editor rather than typed by a
// caller, but "admin-configured" is not the same as trusted: anyone who can
// edit a flow could otherwise point a callback at the metadata service or an
// internal host and read the response back through the IVR. The request goes
// through the same validation and SSRF-safe dialer as webhooks (plan 10, X12).
func executeHTTPCallback(url, method string, headers map[string]string, body string, timeout time.Duration) (*HTTPCallbackResult, error) {
	if err := safehttp.ValidateURL(url); err != nil {
		return nil, fmt.Errorf("http callback URL: %w", err)
	}

	var bodyReader io.Reader
	if body != "" {
		bodyReader = strings.NewReader(body)
	}

	req, err := http.NewRequest(method, url, bodyReader)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}

	for k, v := range headers {
		req.Header.Set(k, v)
	}
	if body != "" && req.Header.Get("Content-Type") == "" {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := callbackClient(timeout).Do(req)
	if err != nil {
		return nil, fmt.Errorf("http request: %w", err)
	}
	defer resp.Body.Close() //nolint:errcheck

	respBody, err := io.ReadAll(io.LimitReader(resp.Body, 64*1024)) // limit to 64KB
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}

	return &HTTPCallbackResult{
		StatusCode: resp.StatusCode,
		Body:       string(respBody),
	}, nil
}

// interpolateTemplate replaces {{key}} placeholders with values from the
// variables map, encoded for the destination (plan 10, S6/X9).
//
// The traversal is the shared renderer's. The mode matters because these
// templates become URLs, headers and JSON bodies, and the variables include
// caller-supplied data: without encoding, a value containing a quote or a
// newline rewrites the request rather than filling it in.
func interpolateTemplate(tpl string, vars map[string]string, mode templating.EscapeMode) string {
	if !strings.Contains(tpl, "{{") {
		return tpl
	}
	return templating.RenderEscapedStrings(tpl, vars, mode)
}
