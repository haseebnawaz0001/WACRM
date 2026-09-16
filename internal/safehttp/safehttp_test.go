package safehttp

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestValidateURL(t *testing.T) {
	blocked := []string{
		"http://localhost/x",
		"http://127.0.0.1/x",
		"https://[::1]/x",
		"http://169.254.169.254/latest/meta-data/",
		"http://10.0.0.5/x",
		"http://192.168.1.1/x",
		"http://172.16.0.1/x",
		"http://0.0.0.0/x",
		"http://db.internal/x",
		"http://printer.local/x",
		"ftp://example.com/x",
		"file:///etc/passwd",
		"not a url at all::",
		"https:///nohost",
	}
	for _, u := range blocked {
		require.Error(t, ValidateURL(u), "expected %q to be rejected", u)
	}

	allowed := []string{
		"https://hooks.example.com/abc",
		"http://example.com:8080/path?q=1",
		"https://8.8.8.8/x",
	}
	for _, u := range allowed {
		require.NoError(t, ValidateURL(u), "expected %q to be allowed", u)
	}
}

// TestDialerBlocksLoopback starts a real local server and shows the dialer
// refuses it. Structural validation cannot catch this case on its own: the
// client is pointed at a literal that ValidateURL would also reject, so the
// test asserts the second layer independently by dialing directly.
func TestDialerBlocksLoopback(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	client := NewClient(5 * time.Second)
	_, err := client.Get(srv.URL)
	require.Error(t, err, "dialer must refuse a loopback address")
	require.Contains(t, err.Error(), "private address")
}

func TestNewClientHasTimeoutAndSafeTransport(t *testing.T) {
	c := NewClient(3 * time.Second)
	require.Equal(t, 3*time.Second, c.Timeout)
	tr, ok := c.Transport.(*http.Transport)
	require.True(t, ok, "transport should be *http.Transport")
	require.NotNil(t, tr.DialContext, "transport must use the safe dialer")
}
