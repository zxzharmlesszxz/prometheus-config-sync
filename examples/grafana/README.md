# Grafana examples

This folder contains Grafana artifacts in the same placement pattern used in service example sets:

- [`prometheus-config-sync-dashboard.json`](./prometheus-config-sync-dashboard.json) — provisionable Grafana v2 resource JSON used by Docker Compose (`apiVersion: dashboard.grafana.app/v2`).

The dashboard is now organized into tabs:

- `Overview`: a two-by-three operational summary with scrape availability, exact sync health, success freshness, pending reload state, unrecovered failure state, and the one-hour sync success rate.
- `Service`: a balanced two-by-two layout with attempt/staged-error rates, change/reload rates, synchronization latency percentiles, and adaptive-window average duration.
- `Runtime`: Go and process runtime metrics in the shared four-by-three service layout: CPU, memory, heap, goroutines, GC, file descriptors, separate receive/transmit network I/O, threads, objects, allocations, FD usage, and average GC pause.
- `Scrape`: Prometheus target scrape metrics in a two-by-two layout: availability, scrape duration, samples, and series added.

`make dashboard-check` validates:
- JSON syntax of the dashboard payload,
- balanced PromQL expression structure in embedded `expr` strings (legacy `targets[*].expr` and v2 `query` payloads).

How it is provisioned:

- Compose passes this file into the container as:
  - `/var/lib/grafana/dashboards/prometheus-config-sync.json:ro`
- Provisioning config points the `file` provider to `/var/lib/grafana/dashboards`.
