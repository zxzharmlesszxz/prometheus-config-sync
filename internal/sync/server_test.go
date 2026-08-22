package sync

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestHandlerServesLandingPage(t *testing.T) {
	t.Parallel()

	metrics := NewMetrics()
	metrics.MarkHealthy(true)
	handler := mustNewHandler(t, "/metrics", metrics)

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("GET / status = %d, want %d", rec.Code, http.StatusOK)
	}

	body := rec.Body.String()
	if !strings.Contains(body, "Prometheus config sync service") {
		t.Fatalf("GET / body missing description: %s", body)
	}
	if !strings.Contains(body, "/metrics") {
		t.Fatalf("GET / body missing metrics link: %s", body)
	}
	if !strings.Contains(body, "/livez") || !strings.Contains(body, "/readyz") {
		t.Fatalf("GET / body missing health links: %s", body)
	}
}

func TestHandlerServesMetrics(t *testing.T) {
	t.Parallel()

	metrics := NewMetrics()
	metrics.MarkHealthy(true)
	metrics.ObserveSync(time.Second, errors.New("validation failed"), failureStageValidation)
	metrics.MarkPublishedChange()
	handler := mustNewHandler(t, "/metrics", metrics)

	req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("GET /metrics status = %d, want %d", rec.Code, http.StatusOK)
	}

	body := rec.Body.String()
	if !strings.Contains(body, "prometheus_config_sync_build_info") {
		t.Fatalf("GET /metrics body missing build info metric: %s", body)
	}
	if !strings.Contains(body, "prometheus_config_sync_syncs_total") {
		t.Fatalf("GET /metrics body missing syncs_total metric: %s", body)
	}
	if !strings.Contains(body, "prometheus_config_sync_healthy 0") {
		t.Fatalf("GET /metrics body missing unhealthy gauge: %s", body)
	}
	if !strings.Contains(body, `prometheus_config_sync_sync_errors_total{stage="validation"} 1`) {
		t.Fatalf("GET /metrics body missing staged sync error: %s", body)
	}
	if !strings.Contains(body, "prometheus_config_sync_changes_total 1") {
		t.Fatalf("GET /metrics body missing changes_total metric: %s", body)
	}
}

func TestHandlerServesReadinessWhenHealthy(t *testing.T) {
	t.Parallel()

	metrics := NewMetrics()
	metrics.MarkHealthy(true)
	handler := mustNewHandler(t, "/metrics", metrics)

	for _, path := range []string{"/readyz", "/healthz"} {
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		if rec.Code != http.StatusOK {
			t.Errorf("GET %s status = %d, want %d", path, rec.Code, http.StatusOK)
		}
		if rec.Body.String() != "ok\n" {
			t.Errorf("GET %s body = %q, want %q", path, rec.Body.String(), "ok\n")
		}
	}
}

func TestHandlerServesReadinessWhenUnhealthy(t *testing.T) {
	t.Parallel()

	metrics := NewMetrics()
	metrics.MarkHealthy(false)
	handler := mustNewHandler(t, "/metrics", metrics)

	for _, path := range []string{"/readyz", "/healthz"} {
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		if rec.Code != http.StatusServiceUnavailable {
			t.Errorf("GET %s status = %d, want %d", path, rec.Code, http.StatusServiceUnavailable)
		}
	}
}

func TestHandlerServesLivezWhenSyncIsUnhealthy(t *testing.T) {
	t.Parallel()

	metrics := NewMetrics()
	metrics.MarkHealthy(false)
	handler := mustNewHandler(t, "/metrics", metrics)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/livez", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /livez status = %d, want %d", rec.Code, http.StatusOK)
	}
}

func TestHandlerRejectsUnsupportedMethods(t *testing.T) {
	t.Parallel()

	metrics := NewMetrics()
	handler := mustNewHandler(t, "/metrics", metrics)
	for _, path := range []string{"/", "/metrics", "/livez", "/readyz", "/healthz"} {
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, path, nil))
		if rec.Code != http.StatusMethodNotAllowed {
			t.Errorf("POST %s status = %d, want %d", path, rec.Code, http.StatusMethodNotAllowed)
		}
	}
}

func TestObserveSyncUsesUnknownFailureStage(t *testing.T) {
	t.Parallel()

	metrics := NewMetrics()
	metrics.ObserveSync(time.Second, errors.New("unclassified"), "")
	handler := mustNewHandler(t, "/metrics", metrics)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	if !strings.Contains(rec.Body.String(), `prometheus_config_sync_sync_errors_total{stage="unknown"} 1`) {
		t.Fatalf("GET /metrics body missing unknown error stage: %s", rec.Body.String())
	}
}

func TestNewHandlerRejectsInvalidMetricsPaths(t *testing.T) {
	t.Parallel()

	for _, path := range []string{"", "/", "metrics", "/healthz", "/livez", "/readyz", "/metrics?format=openmetrics", "/{name}", "/metrics*", "/metrics/", "/metrics//nested", "/metrics%", `/metrics\\name`} {
		invalidPath := path
		t.Run(invalidPath, func(t *testing.T) {
			t.Parallel()
			metrics := NewMetrics()
			if _, err := NewHandler(invalidPath, metrics.Registry(), metrics); err == nil {
				t.Fatalf("NewHandler(%q) error = nil, want error", invalidPath)
			}
		})
	}
}

func mustNewHandler(t *testing.T, path string, metrics *Metrics) http.Handler {
	t.Helper()
	handler, err := NewHandler(path, metrics.Registry(), metrics)
	if err != nil {
		t.Fatalf("NewHandler(%q) error = %v", path, err)
	}
	return handler
}
