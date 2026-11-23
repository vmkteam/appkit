package appkit

import (
	"context"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

const (
	ctxCallerName contextKey = "callerName"
)

// Labels of HTTP client metrics.
const (
	labelCode   = "code"
	labelMethod = "method"
	labelCaller = "caller"
	labelOrigin = "origin"
)

var (
	clientMetricsOnce sync.Once
	clientRequests    = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: metricsNamespace,
			Subsystem: metricsSubsystemHTTPClient,
			Name:      "requests_total",
			Help:      "Requests count by code/method/client/origin.",
		},
		[]string{labelCode, labelMethod, labelCaller, labelOrigin},
	)
	clientDurations = prometheus.NewSummaryVec(
		prometheus.SummaryOpts{
			Namespace: metricsNamespace,
			Subsystem: metricsSubsystemHTTPClient,
			Name:      "responses_duration_seconds",
			Help:      "Response time by code/method/client/origin.",
		},
		[]string{labelCode, labelMethod, labelCaller, labelOrigin},
	)
	clientInflights = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Namespace: metricsNamespace,
			Subsystem: metricsSubsystemHTTPClient,
			Name:      "requests_inflight",
			Help:      "Gauge for inflight requests.",
		},
		[]string{labelCaller, labelOrigin},
	)
)

// NewCallerNameContext creates new context with caller name.
func NewCallerNameContext(ctx context.Context, callerName string) context.Context {
	return context.WithValue(ctx, ctxCallerName, callerName)
}

// CallerNameFromContext returns caller name from context.
func CallerNameFromContext(ctx context.Context) string {
	r, _ := ctx.Value(ctxCallerName).(string)
	return r
}

type metricsRoundTripper struct {
	base http.RoundTripper
}

func (m *metricsRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	var origin string
	if req.URL != nil {
		origin = req.URL.Scheme + "://" + req.URL.Host
	}
	labels := prometheus.Labels{
		labelCaller: CallerNameFromContext(req.Context()),
		labelOrigin: origin,
	}

	start := time.Now()
	clientInflights.With(labels).Inc()
	resp, err := m.base.RoundTrip(req)
	clientInflights.With(labels).Dec()
	duration := time.Since(start).Seconds()

	var code string
	if resp != nil {
		code = strconv.Itoa(resp.StatusCode)
	}
	labels[labelCode] = code
	labels[labelMethod] = req.Method

	clientRequests.With(labels).Inc()
	clientDurations.With(labels).Observe(duration)

	return resp, err
}

// WithMetricsTransport wraps http transport, adds client metrics tracking.
func WithMetricsTransport(base http.RoundTripper) http.RoundTripper {
	clientMetricsOnce.Do(func() {
		prometheus.MustRegister(clientRequests, clientDurations, clientInflights)
	})
	return &metricsRoundTripper{base: base}
}

type headerRoundTripper struct {
	base    http.RoundTripper
	headers http.Header
}

func (h *headerRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	reqClone := req.Clone(req.Context())

	for key, values := range h.headers {
		for _, value := range values {
			reqClone.Header.Add(key, value)
		}
	}

	return h.base.RoundTrip(reqClone)
}

// WithHeadersTransport wraps http transport, adds provided headers for each request.
func WithHeadersTransport(base http.RoundTripper, headers http.Header) http.RoundTripper {
	return &headerRoundTripper{
		base:    base,
		headers: headers,
	}
}

// NewHTTPClient returns http client with metrics and headers for internal service calls.
func NewHTTPClient(appName, version string, timeout time.Duration) *http.Client {
	transport := WithHeadersTransport(http.DefaultTransport, NewInternalHeaders(appName, version))
	transport = WithMetricsTransport(transport)

	return &http.Client{
		Timeout:   timeout,
		Transport: transport,
	}
}
