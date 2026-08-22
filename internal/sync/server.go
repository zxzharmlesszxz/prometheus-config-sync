package sync

import (
	"errors"
	"net/http"
	"regexp"
	"strings"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/prometheus/common/version"
	"github.com/prometheus/exporter-toolkit/web"
)

var metricsPathPattern = regexp.MustCompile(`^/[A-Za-z0-9._~!$&()+,;=:@/-]*$`)

func NewHandler(metricsPath string, registry *prometheus.Registry, metrics *Metrics) (http.Handler, error) {
	if err := validateMetricsPath(metricsPath); err != nil {
		return nil, err
	}

	mux := http.NewServeMux()
	mux.Handle(metricsPath, getOnly(promhttp.HandlerFor(registry, promhttp.HandlerOpts{})))
	readyHandler := getOnly(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if !metrics.healthy.Load() {
			http.Error(w, "sync unhealthy", http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		_, _ = w.Write([]byte("ok\n"))
	}))
	mux.Handle("/readyz", readyHandler)
	mux.Handle("/healthz", readyHandler)
	mux.Handle("/livez", getOnly(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		_, _ = w.Write([]byte("ok\n"))
	})))

	landingPage, err := web.NewLandingPage(web.LandingConfig{
		Name:        "prometheus_config_sync",
		Description: "Prometheus config sync service",
		Version:     version.Info(),
		Links: []web.LandingLinks{
			{Address: metricsPath, Text: "Metrics"},
			{Address: "/livez", Text: "Liveness"},
			{Address: "/readyz", Text: "Readiness"},
		},
	})
	if err != nil {
		return nil, err
	}
	mux.Handle("/", getOnly(landingPage))

	return mux, nil
}

func validateMetricsPath(metricsPath string) error {
	if metricsPath == "" {
		return errors.New("web.metrics-path is required")
	}
	if metricsPath == "/" {
		return errors.New("web.metrics-path must not be the root path")
	}
	if metricsPath == "/healthz" || metricsPath == "/livez" || metricsPath == "/readyz" {
		return errors.New("web.metrics-path must not conflict with a health endpoint")
	}
	if !metricsPathPattern.MatchString(metricsPath) || metricsPath[len(metricsPath)-1] == '/' || strings.Contains(metricsPath, "//") {
		return errors.New("web.metrics-path must be a literal absolute HTTP path")
	}
	return nil
}

func getOnly(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			w.Header().Set("Allow", http.MethodGet)
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		next.ServeHTTP(w, r)
	})
}
