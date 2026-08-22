# prometheus-config-sync

[Українська](README.uk.md)

`prometheus-config-sync` fetches generated Prometheus scrape configuration and rule bundles from an HTTP source, writes them to a shared filesystem, validates changed content with `promtool`, and requests a Prometheus lifecycle reload.

## Runtime contract

- HTTP source endpoints: `GET /prometheus/config` and `GET /prometheus/rules`.
- A generation is accepted only after two consecutive complete HTTP source snapshots are byte-identical; an unstable snapshot is retried up to three times.
- Generated files: `scrape-configs.yml` and `rules/generated-rules.yml`.
- Applied-generation marker: `.prometheus-config-sync-applied.sha256`.
- Prometheus reload endpoint: `POST /-/reload`.
- HTTP status on port `9534`: `/livez` reports process liveness, `/readyz` reports synchronization readiness, `/healthz` remains a compatibility alias for `/readyz`, and `/metrics` exposes Prometheus metrics.
- Service HTTP endpoints accept `GET` only.

- The source may be any compatible web server. Config-sync appends the two fixed paths to `--source.url`, expects raw YAML with HTTP 200, and optionally sends the configured Bearer token; no proprietary response envelope is required.
- Exactly one config-sync writer may own an output directory.
- Prometheus must mount the same output volume and reference the generated paths from its base configuration.
- The packaged Docker image includes `promtool` at `/usr/local/bin/promtool` and runs as UID/GID `10001`; a configured validation executable must have the exact basename `promtool`.

See [ARCHITECTURE.md](ARCHITECTURE.md) for data flow and failure semantics, [METRICS.md](METRICS.md) for the metric contract, [DEPLOYMENT.md](DEPLOYMENT.md) for Docker, Compose, and Kubernetes operations, and [local smoke testing](docs/LOCAL_SMOKE_TESTING.md) for black-box acceptance.

## Build and test

Go 1.27.0 is the project toolchain.

```bash
make help
make build
make test
make test-race
make lint-install
make check
make full-check
make ci
```

The validation ladder is cumulative:

- `make check` runs formatting, module tidiness, vet, lint, coverage, Compose,
  Helm, and Prometheus configuration/rule validation;
- `make full-check` adds race tests, a native build, release archive smoke, and
  Dockerfile static checks;
- `make ci` adds security scans, default-image Docker smoke, and the complete
  local black-box suite.

Local execution expects `golangci-lint` v2.13.1 built with Go 1.27.0, Docker
with Compose v2, Helm, ShellCheck, jq, Python 3, and actionlint to be available.
GitHub Actions provisions its own Go and Helm toolchains and uses the same
pinned golangci-lint version.
For full local parity, keep a writable `golangci-lint` cache (set via `GOLANGCI_LINT_CACHE` or equivalent) and Docker socket access for image/build targets.

The binary is written to `dist/prometheus-config-sync`:

```bash
./dist/prometheus-config-sync \
  --source.url=http://127.0.0.1:9876 \
  --prometheus.reload-url=http://127.0.0.1:9090/-/reload \
  --output.dir=/etc/prometheus/generated \
  --promtool.path=/usr/local/bin/promtool \
  --interval=30s \
  --web.listen-address=:9534
```

## Configuration

CLI flags override environment values. Durations should use Go duration syntax such as `15s` or `2m`.

| Environment variable                           | Default                          | Purpose                                 |
|------------------------------------------------|----------------------------------|-----------------------------------------|
| `PROMETHEUS_CONFIG_SYNC_SOURCE_URL`            | `http://127.0.0.1:9876`          | Base URL for generated assets           |
| `PROMETHEUS_CONFIG_SYNC_SOURCE_TOKEN`          | empty                            | Optional HTTP source bearer token       |
| `PROMETHEUS_CONFIG_SYNC_PROMETHEUS_RELOAD_URL` | `http://127.0.0.1:9090/-/reload` | Prometheus lifecycle reload URL         |
| `PROMETHEUS_CONFIG_SYNC_OUTPUT_DIR`            | `/etc/prometheus/generated`      | Generated-file directory                |
| `PROMETHEUS_CONFIG_SYNC_PROMTOOL_PATH`         | empty                            | Validator executable                    |
| `PROMETHEUS_CONFIG_SYNC_INTERVAL`              | `30s`                            | Synchronization interval                |
| `PROMETHEUS_CONFIG_SYNC_HTTP_TIMEOUT`          | `10s`                            | HTTP source and reload HTTP timeout     |
| `PROMETHEUS_CONFIG_SYNC_VALIDATION_TIMEOUT`    | `30s`                            | Maximum `promtool` execution time       |
| `PROMETHEUS_CONFIG_SYNC_MAX_CONFIG_BYTES`      | `10485760`                       | Maximum successful config response size |
| `PROMETHEUS_CONFIG_SYNC_MAX_RULES_BYTES`       | `10485760`                       | Maximum successful rules response size  |
| `PROMETHEUS_CONFIG_SYNC_WEB_LISTEN_ADDRESS`    | `:9534`                          | Metrics/health listener                 |
| `PROMETHEUS_CONFIG_SYNC_WEB_METRICS_PATH`      | `/metrics`                       | Prometheus metrics path                 |

Prometheus exporter-toolkit also supplies logging and web TLS/authentication flags; inspect the exact binary contract with `prometheus-config-sync --help`.
The metrics path must be canonical: absolute, non-root, without a trailing or consecutive slash, and distinct from the health endpoints.

## Local Docker Compose

The local stack is self-contained. It starts a controllable HTTP source fixture, Prometheus, config-sync, and Grafana. An initialization container prepares ownership and permissions on the generated volume; the scrape glob lets Prometheus start before config-sync publishes the first generation. The dedicated smoke container validates files, APIs, invalid-payload isolation, failure recovery, restart behavior, and runtime bounds without host Python dependencies.

```bash
cp .env.example .env
make smoke
```

`make smoke` builds the fixtures, starts the long-running stack in the background, runs the complete acceptance suite, and leaves the services available for inspection. Use `make compose-up` separately when an attached foreground stack is desired, or `make compose-smoke` for only the baseline smoke scenario.

Endpoints:

- config-sync: `http://localhost:9534`;
- Prometheus: `http://localhost:9090`;
- controllable HTTP source fixture: `http://localhost:9876`;
- Grafana: `http://localhost:3000` (`admin` / `admin`).

If a default host port is occupied, override `SOURCE_PORT`, `PROMETHEUS_PORT`, `CONFIG_SYNC_PORT`, or `GRAFANA_PORT` in `.env`. Containerized smoke scenarios use internal Compose addresses and do not require matching host URL overrides. `SMOKE_BASE_URL` is used only by `make http-smoke` against an already running service.

Useful commands:

```bash
make compose-ps
make compose-logs
make compose-smoke
make compose-down
```

See [docs/LOCAL_SMOKE_TESTING.md](docs/LOCAL_SMOKE_TESTING.md) for individual scenarios and diagnostics. The local fixture is deliberately deterministic and does not replace integration testing against the real HTTP source.

## Docker image

```bash
make docker-build
make docker-smoke
make docker-check
make smoke-compatibility
```

The Docker smoke target verifies UID/GID, the declared image user, CLI
help/version, embedded `promtool`, and OCI source/title metadata. The default runtime
is pinned Alpine. `smoke-compatibility` builds and checks Alpine, Debian
Bookworm slim, and Debian Trixie slim variants. Multi-platform targets are
available as `docker-buildx` and `docker-buildx-push`.

## Kubernetes and Helm

The chart is at [deploy/prometheus-config-sync/Chart.yaml](deploy/prometheus-config-sync/Chart.yaml). A typical installation reuses a PVC already mounted by Prometheus:

```bash
helm upgrade --install prometheus-config-sync ./deploy/prometheus-config-sync \
  --namespace monitoring \
  --set persistence.create=false \
  --set persistence.existingClaim=prometheus-generated \
  --set config.sourceURL=http://prometheus-config-source:9876 \
  --set config.prometheusReloadURL=http://prometheus:9090/-/reload
```

The chart deliberately rejects multiple replicas, disabled persistence, ambiguous PVC modes, noncanonical or health-endpoint metrics paths, and ambiguous HTTP source secret modes. See [DEPLOYMENT.md](DEPLOYMENT.md) before installing: the PVC and Prometheus base configuration are external parts of the contract.

```bash
make helm-template-check
make helm-lint
make helm-package
```

## GitHub Actions

- [CI](.github/workflows/ci.yml) invokes reusable [Checks](.github/workflows/checks.yml) on pushes, pull requests, and manual dispatches.
- Checks cover Go formatting/tidiness/vet/lint/coverage, race tests, security scans, Compose, Helm, Prometheus rules, Docker smoke, and release archives.
- [Release](.github/workflows/release.yml) accepts canonical `vMAJOR.MINOR.PATCH` tags, republishes the same checks, creates binary archives/checksums/SBOM/Helm package, and publishes multi-architecture GHCR images with provenance and SBOM attestations.
- [Dependabot](.github/dependabot.yml) tracks Go modules, Docker bases, and GitHub Actions.

Repository administrators must enable GitHub Actions and GHCR package publication before the release workflow can publish.

## systemd

Examples remain available for non-container deployments:

- [environment defaults](deploy/systemd/prometheus-config-sync.default);
- [systemd unit](deploy/systemd/prometheus-config-sync.service).

Install the service binary and a compatible `promtool`, then make the configured generated directory writable by the existing `prometheus` user/group before enabling the unit. The native release archive does not bundle `promtool`. The hardened example uses `/etc/default/prometheus-config-sync`, a private `/tmp`, and filesystem permissions instead of a fixed systemd writable-path allowlist, so `PROMETHEUS_CONFIG_SYNC_OUTPUT_DIR` remains configurable.

## Monitoring examples

- [Example catalog](examples/README.md) with Prometheus and Grafana assets.
- [Grafana dashboard](examples/grafana/prometheus-config-sync-dashboard.json)
- [Prometheus alerts](examples/prometheus/alerts/prometheus-config-sync.yml)
- [Prometheus examples index](examples/prometheus/README.md)
- Prometheus base configuration (embedded here):

```yaml
global:
  scrape_interval: 15s
  evaluation_interval: 15s

scrape_configs:
  - job_name: prometheus
    static_configs:
      - targets: ["prometheus:9090"]
  - job_name: prometheus-config-source
    static_configs:
      - targets: ["source:9876"]
  - job_name: prometheus-config-sync
    static_configs:
      - targets: ["config-sync:9534"]

scrape_config_files:
  - /etc/prometheus/generated/scrape-configs*.yml

rule_files:
  - /etc/prometheus/rules/prometheus-config-sync.yml
  - /etc/prometheus/generated/rules/*.yml
```
