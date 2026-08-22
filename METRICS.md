# Metrics

[Українська](METRICS.uk.md)

The default endpoint is `GET /metrics` on port `9534`. The service uses an isolated pedantic Prometheus registry and includes Go process/runtime and build information collectors.

| Metric                                                  | Type      | Meaning                                                |
|---------------------------------------------------------|-----------|--------------------------------------------------------|
| `prometheus_config_sync_build_info`                     | gauge     | Build version, revision, branch, Go version            |
| `prometheus_config_sync_syncs_total`                    | counter   | Completed synchronization attempts                     |
| `prometheus_config_sync_sync_failures_total`            | counter   | Failed synchronization attempts                        |
| `prometheus_config_sync_sync_errors_total{stage}`       | counter   | Errors classified by bounded processing stage          |
| `prometheus_config_sync_reloads_total`                  | counter   | Successful Prometheus reload requests                  |
| `prometheus_config_sync_changes_total`                  | counter   | Published generated-content changes                    |
| `prometheus_config_sync_healthy`                        | gauge     | Latest synchronization result: `1` healthy, `0` failed |
| `prometheus_config_sync_last_success_timestamp_seconds` | gauge     | Last successful sync Unix timestamp                    |
| `prometheus_config_sync_last_failure_timestamp_seconds` | gauge     | Last failed sync Unix timestamp                        |
| `prometheus_config_sync_last_reload_timestamp_seconds`  | gauge     | Last successful reload Unix timestamp                  |
| `prometheus_config_sync_last_change_timestamp_seconds`  | gauge     | Last successful changed-asset publication timestamp    |
| `prometheus_config_sync_sync_duration_seconds`          | histogram | End-to-end duration of completed attempts              |

Timestamp gauges are `0` until their first event. Queries and dashboards should treat zero as “not observed”, not as an event at Unix epoch.

Use `last_success_timestamp_seconds` to measure synchronization freshness. `last_change_timestamp_seconds` records content publication only and may legitimately remain unchanged indefinitely while HTTP source continues returning the same healthy generation.

`sync_errors_total` uses only the bounded stages `fetch`, `snapshot`, `state`, `validation`, `publication`, `reload`, `marker`, and the defensive fallback `unknown`. Error messages and dynamic values are never used as labels. `healthy` is the Prometheus representation of the readiness state exposed by `/readyz` and `/healthz`; `/livez` and Prometheus `up` represent process and scrape availability instead.

`last_success` advances only when the desired generation is already marked applied or after validation, publication, successful Prometheus reload, and marker persistence all complete. A reload failure therefore keeps health negative and is retried on the next cycle.

The repository supplies standalone rules in [examples/prometheus/alerts/prometheus-config-sync.yml](examples/prometheus/alerts/prometheus-config-sync.yml) and an equivalent optional Helm `PrometheusRule`. Both cover target availability, exact sync health, freshness, staged errors, pending reloads, unrecovered failures, and critical staleness. Optional `ServiceMonitor` and `PrometheusRule` resources require Prometheus Operator CRDs.
