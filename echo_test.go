package appkit_test

import (
	"errors"
	"net/http"
	"net/http/httptest"
	_ "net/http/pprof"
	"strings"
	"testing"

	"github.com/vmkteam/appkit"

	"github.com/getsentry/sentry-go"
	"github.com/labstack/echo/v5"
	"github.com/prometheus/client_golang/prometheus"
)

func serve(e *echo.Echo, path string, headers map[string]string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, path, nil)
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)

	return rec
}

func TestNewEcho(t *testing.T) {
	var (
		gotIP  string
		hasHub bool
	)
	e := appkit.NewEcho()
	e.GET("/", func(c *echo.Context) error {
		ctx := c.Request().Context()
		gotIP = appkit.IPFromContext(ctx)
		hasHub = sentry.GetHubFromContext(ctx) != nil

		return c.NoContent(http.StatusNoContent)
	})

	rec := serve(e, "/", map[string]string{"X-Real-Ip": "10.1.2.3"})
	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusNoContent)
	}
	if gotIP != "10.1.2.3" {
		t.Errorf("IP in request context = %q, want it from X-Real-IP header", gotIP)
	}
	if !hasHub {
		t.Error("sentry hub is not set to request context")
	}
}

func TestHTTPMetrics(t *testing.T) {
	const serverName = "testsrv"

	e := appkit.NewEcho()
	e.Use(appkit.HTTPMetrics(serverName))
	e.GET("/ok", func(c *echo.Context) error {
		return c.String(http.StatusOK, "ok")
	})
	e.GET("/forbidden", func(*echo.Context) error {
		return echo.NewHTTPError(http.StatusForbidden, "forbidden")
	})

	// status code of this error is known only to the error handler of a service
	errTeapot := errors.New("teapot")
	e.GET("/teapot", func(*echo.Context) error {
		return errTeapot
	})
	defaultErrorHandler := e.HTTPErrorHandler
	e.HTTPErrorHandler = func(c *echo.Context, err error) {
		if errors.Is(err, errTeapot) {
			_ = c.NoContent(http.StatusTeapot)
			return
		}
		defaultErrorHandler(c, err)
	}

	for path, wantCode := range map[string]int{
		"/ok":        http.StatusOK,
		"/forbidden": http.StatusForbidden,
		"/teapot":    http.StatusTeapot,
		"/missing":   http.StatusNotFound,
	} {
		if rec := serve(e, path, nil); rec.Code != wantCode {
			t.Errorf("GET %s status = %d, want %d", path, rec.Code, wantCode)
		}
	}

	// collect requests counter as "code uri" => value
	families, err := prometheus.DefaultGatherer.Gather()
	if err != nil {
		t.Fatal(err)
	}
	got := make(map[string]float64)
	for _, mf := range families {
		if mf.GetName() != "app_http_requests_total" {
			continue
		}
		for _, m := range mf.GetMetric() {
			labels := make(map[string]string)
			for _, l := range m.GetLabel() {
				labels[l.GetName()] = l.GetValue()
			}
			if labels["server"] == serverName {
				got[labels["code"]+" "+labels["uri"]] = m.GetCounter().GetValue()
			}
		}
	}

	want := map[string]float64{"200 /ok": 1, "403 /forbidden": 1, "418 /teapot": 1, "404 ": 1}
	for key, value := range want {
		if got[key] != value {
			t.Errorf("app_http_requests_total{%s} = %v, want %v", key, got[key], value)
		}
	}
	if len(got) != len(want) {
		t.Errorf("app_http_requests_total series = %v, want %v", got, want)
	}
}

func TestRenderRoutesAndPprof(t *testing.T) {
	e := appkit.NewEcho()
	e.GET("/", appkit.RenderRoutes("testapp", e))
	e.Any("/debug/pprof/*", appkit.PprofHandler)
	e.Any("/other/*", appkit.PprofHandler)

	rec := serve(e, "/", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET / status = %d, want %d", rec.Code, http.StatusOK)
	}
	for _, want := range []string{"<h1>testapp</h1>", `<a href="/">/</a>`, `<a href="/debug/pprof/">/debug/pprof/</a>`} {
		if !strings.Contains(rec.Body.String(), want) {
			t.Errorf("routes page does not contain %q: %s", want, rec.Body.String())
		}
	}

	if rec := serve(e, "/debug/pprof/cmdline", nil); rec.Code != http.StatusOK {
		t.Errorf("GET /debug/pprof/cmdline status = %d, want %d", rec.Code, http.StatusOK)
	}
	// a path unknown to http.DefaultServeMux
	if rec := serve(e, "/other/cmdline", nil); rec.Code != http.StatusNotFound {
		t.Errorf("GET /other/cmdline status = %d, want %d", rec.Code, http.StatusNotFound)
	}
}

func TestEchoHandler(t *testing.T) {
	var gotIP, gotRequestID string
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotIP = appkit.IPFromContext(r.Context())
		gotRequestID = r.Header.Get("X-Request-Id")
		w.WriteHeader(http.StatusOK)
	})

	e := echo.New()
	e.Any("/rpc", appkit.EchoHandler(next))

	rec := serve(e, "/rpc", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	// httptest.NewRequest sets RemoteAddr to 192.0.2.1:1234
	if gotIP != "192.0.2.1" {
		t.Errorf("IP in request context = %q, want remote address", gotIP)
	}
	if gotRequestID == "" || rec.Header().Get("X-Request-Id") != gotRequestID {
		t.Errorf("X-Request-ID: request = %q, response = %q, want the same generated value", gotRequestID, rec.Header().Get("X-Request-Id"))
	}
	if origin := rec.Header().Get("Access-Control-Allow-Origin"); origin != "*" {
		t.Errorf("Access-Control-Allow-Origin = %q, want *", origin)
	}
}
