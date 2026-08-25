package telemetry

import (
	"net/http"
	"time"

	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
)

// WrapTransport returns an http.RoundTripper that records a client span per
// outbound request. Nil rt uses http.DefaultTransport. The span name is
// method + path, not the full URL, so a token in a query string does not become
// a span name.
func WrapTransport(rt http.RoundTripper) http.RoundTripper {
	if rt == nil {
		rt = http.DefaultTransport
	}
	return otelhttp.NewTransport(rt, otelhttp.WithSpanNameFormatter(clientSpanName))
}

// HTTPClient returns a client whose transport records client spans.
// A timeout of 0 means no client-wide deadline, which is what the stdio SSE
// proxy needs: http.Client.Timeout would otherwise cut the stream.
func HTTPClient(timeout time.Duration) *http.Client {
	return &http.Client{Timeout: timeout, Transport: WrapTransport(nil)}
}

// clientSpanName names an outbound span as METHOD /path so the query string
// never becomes the name.
func clientSpanName(_ string, r *http.Request) string {
	if r == nil || r.URL == nil {
		return "HTTP"
	}
	return r.Method + " " + r.URL.Path
}

// HTTPHandler wraps an inbound handler so each request becomes a server span
// named "METHOD /path". Query strings are omitted from the name on purpose.
func HTTPHandler(h http.Handler) http.Handler {
	return otelhttp.NewHandler(h, "http.server",
		otelhttp.WithSpanNameFormatter(func(_ string, r *http.Request) string {
			if r == nil {
				return "HTTP"
			}
			return r.Method + " " + r.URL.Path
		}),
	)
}
