# systemd deployment

This directory contains the host-deployment assets:

- `prometheus-config-sync.service` — hardened systemd unit;
- `prometheus-config-sync.default` — environment defaults installed as `/etc/default/prometheus-config-sync`.

Installation and runtime requirements are documented in [`DEPLOYMENT.md`](../../DEPLOYMENT.md#systemd).
