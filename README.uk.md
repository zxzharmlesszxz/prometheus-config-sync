# prometheus-config-sync

[English](README.md)

`prometheus-config-sync` отримує з HTTP source згенеровані scrape-конфігурації та правила Prometheus, записує їх у спільну файлову систему, перевіряє змінений вміст через `promtool` і викликає lifecycle reload Prometheus.

## Контракт виконання

- Джерела: `GET /prometheus/config` і `GET /prometheus/rules`.
- Generation приймається лише після двох послідовних byte-identical повних HTTP source snapshots; нестабільний snapshot повторюється до трьох разів.
- Результат: `scrape-configs.yml` і `rules/generated-rules.yml`.
- Маркер застосованої генерації: `.prometheus-config-sync-applied.sha256`.
- Reload: `POST /-/reload`.
- HTTP status на порту `9534`: `/livez` показує liveness процесу, `/readyz` — synchronization readiness, `/healthz` лишається compatibility alias для `/readyz`, а `/metrics` віддає Prometheus metrics.
- HTTP endpoints сервісу приймають лише `GET`.

- Source може бути будь-яким сумісним web server. Config-sync додає два фіксовані paths до `--source.url`, очікує raw YAML із HTTP 200 та опціонально надсилає configured Bearer token; proprietary response envelope не потрібен.
- Один output directory може мати лише один config-sync writer.
- Prometheus має монтувати той самий volume та посилатися на згенеровані шляхи у своїй базовій конфігурації.
- Docker image містить `/usr/local/bin/promtool` і працює з UID/GID `10001`; configured validation executable повинен мати точний basename `promtool`.

Архітектуру описано в [ARCHITECTURE.uk.md](ARCHITECTURE.uk.md), метрики — у [METRICS.uk.md](METRICS.uk.md), розгортання — у [DEPLOYMENT.uk.md](DEPLOYMENT.uk.md), а black-box acceptance — у [локальному smoke testing](docs/LOCAL_SMOKE_TESTING.uk.md).

## Розробка

```bash
make help
make build
make test
make test-race
make check
make full-check
make ci
```

Драбина перевірок є кумулятивною:

- `make check` запускає formatting, module tidiness, vet, lint, coverage,
  Compose, Helm та validation конфігурації/rules Prometheus;
- `make full-check` додає race tests, native build, release archive smoke та
  Dockerfile static checks;
- `make ci` додає security scans, Docker smoke типового image і повний
  локальний black-box suite.

Для локального запуску потрібні `golangci-lint` v2.12.2, Docker із Compose v2, Helm,
ShellCheck, jq, Python 3 та actionlint. GitHub Actions сам готує Go/Helm
toolchains і запускає ту саму pinned версію golangci-lint.
Для повного локального parity додайте доступ до Docker socket для image/build
цілей та writable кеш `golangci-lint` (через `GOLANGCI_LINT_CACHE` або еквівалент).

Go toolchain проєкту — 1.26.6. Binary створюється у `dist/prometheus-config-sync`:

```bash
./dist/prometheus-config-sync \
  --source.url=http://127.0.0.1:9876 \
  --prometheus.reload-url=http://127.0.0.1:9090/-/reload \
  --output.dir=/etc/prometheus/generated \
  --promtool.path=/usr/local/bin/promtool \
  --interval=30s \
  --web.listen-address=:9534
```

## Конфігурація

CLI flags мають пріоритет над environment. Duration-значення використовують Go duration syntax, наприклад `15s` або `2m`.

| Environment variable                           | Типове значення                  | Призначення                              |
|------------------------------------------------|----------------------------------|------------------------------------------|
| `PROMETHEUS_CONFIG_SYNC_SOURCE_URL`            | `http://127.0.0.1:9876`          | Base URL generated assets                |
| `PROMETHEUS_CONFIG_SYNC_SOURCE_TOKEN`          | порожнє                          | Необов'язковий HTTP source bearer token  |
| `PROMETHEUS_CONFIG_SYNC_PROMETHEUS_RELOAD_URL` | `http://127.0.0.1:9090/-/reload` | Prometheus lifecycle reload URL          |
| `PROMETHEUS_CONFIG_SYNC_OUTPUT_DIR`            | `/etc/prometheus/generated`      | Generated-file directory                 |
| `PROMETHEUS_CONFIG_SYNC_PROMTOOL_PATH`         | порожнє                          | Validator executable                     |
| `PROMETHEUS_CONFIG_SYNC_INTERVAL`              | `30s`                            | Інтервал синхронізації                   |
| `PROMETHEUS_CONFIG_SYNC_HTTP_TIMEOUT`          | `10s`                            | HTTP source і reload HTTP timeout        |
| `PROMETHEUS_CONFIG_SYNC_VALIDATION_TIMEOUT`    | `30s`                            | Максимальний час виконання `promtool`    |
| `PROMETHEUS_CONFIG_SYNC_MAX_CONFIG_BYTES`      | `10485760`                       | Максимальний розмір успішної config body |
| `PROMETHEUS_CONFIG_SYNC_MAX_RULES_BYTES`       | `10485760`                       | Максимальний розмір успішної rules body  |
| `PROMETHEUS_CONFIG_SYNC_WEB_LISTEN_ADDRESS`    | `:9534`                          | Listener метрик і health                 |
| `PROMETHEUS_CONFIG_SYNC_WEB_METRICS_PATH`      | `/metrics`                       | Шлях Prometheus metrics                  |

Prometheus exporter-toolkit також додає logging і web TLS/authentication flags; точний контракт показує `prometheus-config-sync --help`.
Metrics path має бути canonical: absolute, non-root, без trailing або consecutive slash і не може збігатися з health endpoints.

## Локальний Compose

Compose є самодостатнім: він запускає керований HTTP source fixture, Prometheus, config-sync і Grafana. Initialization container готує ownership і permissions generated volume, а scrape glob дозволяє Prometheus стартувати до публікації першої generation. Окремий smoke-контейнер перевіряє generated files, API, ізоляцію невалідних payloads, відсутність reload без змін, failure recovery, restart і runtime bounds без host-залежності від Python.

```bash
cp .env.example .env
make smoke
```

`make smoke` збирає fixtures, запускає довготривалий стек у background, виконує повний acceptance suite та залишає сервіси доступними для діагностики. `make compose-up` використовується окремо для attached foreground stack, а `make compose-smoke` — для одного базового smoke-сценарію.

Доступні адреси:

- `http://localhost:9534` — config-sync;
- `http://localhost:9090` — Prometheus;
- `http://localhost:9876` — HTTP source fixture;
- `http://localhost:3000` — Grafana (`admin` / `admin`).

Якщо host-порт зайнятий, перевизначте `SOURCE_PORT`, `PROMETHEUS_PORT`, `CONFIG_SYNC_PORT` або `GRAFANA_PORT` у `.env`. Контейнерні smoke-сценарії використовують внутрішні Compose-адреси й не потребують відповідних host URL overrides. `SMOKE_BASE_URL` використовується лише ціллю `make http-smoke` для вже запущеного сервісу.

Корисні команди:

```bash
make compose-ps
make compose-logs
make compose-smoke
make compose-down
```

Детальні сценарії та діагностику описано в [docs/LOCAL_SMOKE_TESTING.uk.md](docs/LOCAL_SMOKE_TESTING.uk.md). Fixture потрібен для детермінованої локальної перевірки, але не замінює інтеграційні тести зі справжнім HTTP source.

## Docker image

```bash
make docker-build
make docker-smoke
make docker-check
make smoke-compatibility
```

Docker smoke перевіряє UID/GID, declared image user, CLI help/version, embedded `promtool`, OCI source та title metadata. Типовий runtime — pinned Alpine. `smoke-compatibility` збирає й перевіряє Alpine, Debian Bookworm slim і Debian Trixie slim. Multi-platform targets: `docker-buildx` і `docker-buildx-push`.

## Helm

Chart розташований у [deploy/prometheus-config-sync](deploy/prometheus-config-sync). Для production зазвичай використовується PVC, який уже змонтовано у Prometheus:

```bash
helm upgrade --install prometheus-config-sync ./deploy/prometheus-config-sync \
  --namespace monitoring \
  --set persistence.create=false \
  --set persistence.existingClaim=prometheus-generated \
  --set config.sourceURL=http://prometheus-config-source:9876 \
  --set config.prometheusReloadURL=http://prometheus:9090/-/reload
```

Chart навмисно дозволяє лише одну replica, вимагає persistence та відхиляє noncanonical або health-endpoint metrics paths. Prometheus має монтувати той самий claim read-only і використовувати:

```yaml
scrape_config_files:
  - /etc/prometheus/generated/scrape-configs*.yml
rule_files:
  - /etc/prometheus/generated/rules/*.yml
```

Перед встановленням прочитайте [DEPLOYMENT.uk.md](DEPLOYMENT.uk.md).

```bash
make helm-template-check
make helm-lint
make helm-package
```

## GitHub Actions

- [CI](.github/workflows/ci.yml) запускає reusable [Checks](.github/workflows/checks.yml) для push, pull request і manual dispatch.
- Checks охоплюють Go formatting/tidiness/vet/lint/coverage, race tests, security scans, Compose, Helm, Prometheus rules, Docker smoke та release archives.
- [Release](.github/workflows/release.yml) приймає канонічні теги `vMAJOR.MINOR.PATCH`, повторює checks, створює binary archives, checksums, SBOM, Helm package та multi-architecture GHCR images із provenance і SBOM attestations.
- [Dependabot](.github/dependabot.yml) стежить за Go modules, Docker bases і GitHub Actions.

Перед publication потрібно увімкнути GitHub Actions і GHCR package publication у repository settings.

## systemd

Для non-container deployment доступні [environment defaults](deploy/systemd/prometheus-config-sync.default) і [systemd unit](deploy/systemd/prometheus-config-sync.service). Перед запуском зробіть configured generated directory writable для наявного користувача/групи `prometheus` та встановіть сумісний `promtool`; fixed systemd writable-path allowlist не використовується, тому `PROMETHEUS_CONFIG_SYNC_OUTPUT_DIR` залишається configurable. Повний контракт описано в [DEPLOYMENT.uk.md](DEPLOYMENT.uk.md#systemd).

## Моніторинг

- [Каталог прикладів](examples/README.md): Prometheus і Grafana assets.
- [Опис метрик](METRICS.uk.md)
- [Grafana dashboard](examples/grafana/prometheus-config-sync-dashboard.json)
- [Prometheus alerts](examples/prometheus/alerts/prometheus-config-sync.yml)
- [Прометей-приклади](examples/prometheus/README.md)
- Базова конфігурація Prometheus:

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
