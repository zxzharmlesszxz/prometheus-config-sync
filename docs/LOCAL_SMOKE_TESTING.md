# Local smoke testing

The repository contains a container-only black-box suite for the complete
HTTP-source-to-Prometheus flow. Docker with Compose v2 is the only host runtime
required; the assertions execute inside an isolated Python container.
The helper image, test client, fixture server, and deterministic HTTP source payloads live under [`test/smoke`](../test/smoke/README.md).

## Topology

```text
smoke-test ---> HTTP source fixture ---> config-sync ---> shared generated volume
                    control API             |                    |
                                            +----------------> Prometheus <-----------+
                                               |
                                 Grafana <------+
```

The HTTP source fixture serves the normal `/prometheus/config` and
`/prometheus/rules` endpoints and exposes private control endpoints used by
the test client to switch between base, changed, invalid, and intentional-error
modes. It never contacts a production service.

## Complete suite

```sh
make smoke
```

The stack remains running after success or failure so its state and logs can
be inspected. The suite verifies:

- config-sync liveness, synchronization readiness, exact readiness gauge, build metrics, and successful synchronization;
- generated scrape configuration and rules on the shared volume;
- active generated targets and loaded generated rules in Prometheus;
- Grafana health, provisioned datasource, and dashboard;
- one published-change increment and reload after changed assets, with neither repeated for identical assets;
- unready state and failure metrics during an HTTP source outage;
- rejection of invalid generated YAML without replacing the last valid files;
- no redundant reloads for unchanged generated payloads;
- automatic readiness recovery when HTTP source is restored;
- clean config-sync shutdown, exit code zero, restart, repeated acceptance, and
  no redundant reload when the persisted assets are still identical;
- structured lifecycle logs, actionable invalid-startup failure, and clean
  SIGTERM handling while the initial HTTP source request is failing;
- runtime UID, generated-file ownership, embedded `promtool`, process count,
  PID 1 file-descriptor count, and an informational Docker resource snapshot.

Stop the stack and remove its disposable volumes with:

```sh
make smoke-down
```

## Individual scenarios

| Command                          | Purpose                                                                     |
|----------------------------------|-----------------------------------------------------------------------------|
| `make smoke-up`                  | Build and recreate HTTP source, Prometheus, config-sync, and Grafana.       |
| `make smoke-fixtures`            | Build the helper image and validate deterministic Prometheus fixtures.      |
| `make smoke-test`                | Run the baseline health and observability assertions.                       |
| `make smoke-change-test`         | Exercise changed and unchanged HTTP source payloads.                        |
| `make smoke-failure-test`        | Exercise an HTTP source outage and recovery.                                |
| `make smoke-validation-test`     | Require invalid HTTP source assets to leave the valid generation untouched. |
| `make smoke-reload-retry-test`   | Require unchanged generated payloads to avoid redundant reloads.            |
| `make smoke-restart-test`        | Stop/start config-sync and repeat baseline acceptance.                      |
| `make smoke-runtime-test`        | Check runtime identity, ownership, tools, and idle bounds.                  |
| `make smoke-runtime-compat-test` | Check UID/GID, generated ownership, and embedded tools.                     |
| `make smoke-resource-test`       | Enforce process and PID 1 file-descriptor limits.                           |
| `make smoke-log-test`            | Validate expected structured lifecycle records.                             |
| `make smoke-fatal-log-test`      | Require invalid startup to fail with an actionable error.                   |
| `make smoke-startup-signal-test` | Require clean SIGTERM handling during initial-sync failure.                 |
| `make smoke-compatibility`       | Build and smoke-test Alpine, Bookworm, and Trixie images.                   |
| `make smoke-logs`                | Follow all long-running service logs.                                       |
| `make smoke-down`                | Stop the stack and delete disposable volumes.                               |

The scenario targets expect a stack started by `make smoke-up`.

## Local endpoints

All published ports bind only to `127.0.0.1`.

| Service             | Default address                             |
|---------------------|---------------------------------------------|
| config-sync         | <http://127.0.0.1:9534>                     |
| Prometheus          | <http://127.0.0.1:9090>                     |
| HTTP source fixture | <http://127.0.0.1:9876>                     |
| Grafana             | <http://127.0.0.1:3000> (`admin` / `admin`) |

Config-sync serves process liveness at `/livez`, synchronization readiness at `/readyz`, the compatibility readiness alias `/healthz`, and metrics at `/metrics`.

Override `SOURCE_PORT`, `PROMETHEUS_PORT`, `CONFIG_SYNC_PORT`, or
`GRAFANA_PORT` when a host port is occupied. Container-to-container addresses
remain unchanged. `SMOKE_TIMEOUT`, `SMOKE_INTERVAL`,
`SMOKE_MAX_IDLE_PROCESSES`, and `SMOKE_MAX_PID1_FDS` control retry, polling,
and resource gates. `make smoke-up` explicitly applies `SMOKE_INTERVAL` so a
developer `.env` cannot make the timing assertions vacuous.

## Diagnostics and scope

On failure, leave the stack running and inspect it with:

```sh
docker compose ps
make smoke-logs
docker compose exec config-sync ls -lR /etc/prometheus/generated
```

The suite validates the packaged service against deterministic local doubles.
It does not validate the production HTTP source implementation, Kubernetes PVC
semantics, remote authentication/TLS, or multi-node storage behavior.
