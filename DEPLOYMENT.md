# Deployment

[Українська](DEPLOYMENT.uk.md)

## Required Prometheus configuration

Prometheus must enable lifecycle reload and load generated files from the shared mount:

```yaml
scrape_config_files:
  - /etc/prometheus/generated/scrape-configs*.yml
rule_files:
  - /etc/prometheus/generated/rules/*.yml
```

Start Prometheus with `--web.enable-lifecycle`. Restrict the reload endpoint to trusted workloads through network policy or another internal boundary.
The glob deliberately allows Prometheus to start before the first generated scrape file exists.

## Container contract

- The process runs as UID/GID `10001`.
- `/etc/prometheus/generated` must be writable.
- `/tmp` must be writable because validation creates a temporary configuration tree.
- The generated directory also stores `.prometheus-config-sync-applied.sha256`; preserve it across config-sync restarts so already applied generations do not reload redundantly.
- Generated scrape and rule files are service-owned and normalized to mode `0644`; rollback restores this invariant rather than preserving manual permission changes.
- Startup attempts to remove orphaned atomic-write temporary files for the three service-owned output names while leaving unrelated files and directories untouched.
- Port `9534` exposes `/metrics`, process liveness at `/livez`, and synchronization readiness at `/readyz`; `/healthz` is a compatibility alias for readiness.
- `promtool` is included at `/usr/local/bin/promtool`.

The image HEALTHCHECK calls `http://127.0.0.1:9534/livez`. A raw-container override of `--web.listen-address` must also replace or disable that fixed Docker healthcheck. The Helm chart deliberately keeps the listener on `:9534` so its Service and probes cannot drift.

## Helm persistence contract

The chart supports two exclusive modes:

1. `persistence.create=true` creates a PVC. Prometheus must be configured separately to mount that claim.
2. `persistence.create=false` plus `persistence.existingClaim=<name>` reuses an existing claim already shared with Prometheus.

The default access mode is `ReadWriteMany` because separate Prometheus and config-sync pods may be scheduled on different nodes. Use `ReadWriteOnce` only when the storage driver and scheduling model allow both pods to mount the claim safely.

The chart uses `Recreate` and enforces `replicaCount=1`; multiple writers are unsupported.

## HTTP source authentication

No token is configured by default. Use an existing Secret:

```bash
helm upgrade --install prometheus-config-sync ./deploy/prometheus-config-sync \
  --set persistence.create=false \
  --set persistence.existingClaim=prometheus-generated \
  --set sourceAuth.existingSecret=prometheus-config-source-client \
  --set sourceAuth.existingSecretKey=token
```

For local evaluation only, the chart can create a Secret with `sourceAuth.create=true` and `sourceAuth.token`. Avoid storing production tokens in values files or shell history.

ConfigMap changes automatically roll the pod. For an externally managed Secret, set `sourceAuth.rolloutChecksum` to the Secret version or content digest; changing it updates the pod template without exposing the token.

## Security and scheduling

The chart uses a non-root user, drops all Linux capabilities, enables RuntimeDefault seccomp, disables service-account token mounting, and keeps the root filesystem read-only. Writable mounts are limited to the generated PVC and `/tmp`.

NetworkPolicy, ServiceMonitor, PrometheusRule, and PDB are disabled by default. Enable NetworkPolicy only after supplying ingress and egress rules for Prometheus scraping, HTTP source, DNS, and the Prometheus reload endpoint.

The optional `PrometheusRule` matches the standalone rule contract, including exact health, staged errors, pending reloads, unrecovered failures, and freshness escalation. `prometheusRule.maxSyncAgeSeconds` controls the warning threshold and `prometheusRule.criticalSyncAgeSeconds` controls the larger critical threshold.

The singleton PDB uses `maxUnavailable: 1` so voluntary maintenance is not blocked indefinitely. Enabling NetworkPolicy without both ingress and egress rules is rejected. Chart-managed runtime flags cannot be overridden through `config.extraArgs`; use their dedicated values instead. Set `image.digest` to deploy an immutable image reference.

## systemd

The example unit runs as the existing `prometheus` user/group, reads `/etc/default/prometheus-config-sync`, and expects the configured output directory to be writable by that account.

The native release archive contains the service binary and the files under `deploy/systemd/`, but does not bundle `promtool`. Install a compatible Prometheus executable with the exact basename `promtool` at `/usr/local/bin/promtool`, or point `PROMETHEUS_CONFIG_SYNC_PROMTOOL_PATH` to another executable named `promtool`, before starting the unit. Removing that variable disables generated-content validation and is not recommended for production.

```bash
sudo install -d -o prometheus -g prometheus -m 0755 /etc/prometheus/generated
sudo install -m 0644 deploy/systemd/prometheus-config-sync.default /etc/default/prometheus-config-sync
sudo install -m 0644 deploy/systemd/prometheus-config-sync.service /etc/systemd/system/prometheus-config-sync.service
sudo systemctl daemon-reload
sudo systemctl enable --now prometheus-config-sync
```

The commands assume the `prometheus` user/group and `/usr/local/bin/prometheus-config-sync` already exist. The unit uses a private `/tmp`, protects operating-system trees through `ProtectSystem=true`, and grants no Linux capabilities. Write access is controlled by normal filesystem permissions instead of a fixed systemd allowlist, so `PROMETHEUS_CONFIG_SYNC_OUTPUT_DIR` may select another location writable by `prometheus`. Change the environment value, directory ownership, and Prometheus configuration or mount together when using another location.

## Validation

```bash
make helm-template-check
make helm-lint
make helm-package
```

The render matrix covers default, development, existing-PVC, managed-token, observability, and network-policy modes, plus invalid multi-writer, persistence, route, and secret combinations.
