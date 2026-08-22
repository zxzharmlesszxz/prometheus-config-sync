# Example assets

This directory keeps local examples for integration and observability checks:

- [`prometheus`](./prometheus/README.md) — Prometheus alerts and unit tests.
- [`grafana`](./grafana/README.md) — Grafana dashboard provisioning payload.

Core usage:

- Use `make prometheus-rules-check` to validate `examples/prometheus/alerts`.
- Use `make smoke` for the full container smoke path (uses source + Prometheus + config-sync + Grafana + checks).
