# Prometheus examples

This folder contains Prometheus-related examples in the same layout as upstream service examples:

- [`alerts`](./alerts) — packaged alerting/rules and unit tests.
  - [`prometheus-config-sync.yml`](./alerts/prometheus-config-sync.yml) — runtime alert and recording rules.
  - [`prometheus-config-sync.test.yml`](./alerts/prometheus-config-sync.test.yml) — `promtool test rules` scenarios.

Where these files are used in the project:

- Docker Compose mounts `examples/prometheus/alerts/prometheus-config-sync.yml` as:
  - `/etc/prometheus/rules/prometheus-config-sync.yml`
- `make prometheus-rules-check` validates both the rules and test cases under `examples/prometheus/alerts`.
- The standalone rules and optional Helm `PrometheusRule` implement the same alerting contract; Helm exposes warning and critical freshness thresholds through chart values.
- Docker Compose initializes only the generated volume ownership and permissions; the scrape glob permits an empty volume until config-sync publishes the first generation.
