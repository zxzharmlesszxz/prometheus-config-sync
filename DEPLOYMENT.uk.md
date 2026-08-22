# Розгортання

[English](DEPLOYMENT.md)

## Обов'язкова конфігурація Prometheus

Prometheus має запускатися з `--web.enable-lifecycle`, монтувати спільний volume та використовувати generated files:

```yaml
scrape_config_files:
  - /etc/prometheus/generated/scrape-configs*.yml
rule_files:
  - /etc/prometheus/generated/rules/*.yml
```

Reload endpoint потрібно обмежити trusted workloads через NetworkPolicy або інший internal boundary.
Glob навмисно дозволяє Prometheus стартувати до появи першого generated scrape file.

## Контракт контейнера

- Процес працює з UID/GID `10001`.
- `/etc/prometheus/generated` має бути writable.
- `/tmp` має бути writable, оскільки validation створює тимчасове configuration tree.
- Generated directory містить `.prometheus-config-sync-applied.sha256`; marker потрібно зберігати між restart, щоб already applied generation не викликала зайвий reload.
- Generated scrape і rule files належать сервісу та нормалізуються до mode `0644`; rollback відновлює цей інваріант, а не ручні зміни permissions.
- Під час startup сервіс намагається видалити orphaned atomic-write temporary files лише для трьох service-owned output names; сторонні файли й directories не змінюються.
- Порт `9534` експонує `/metrics`, process liveness на `/livez` і synchronization readiness на `/readyz`; `/healthz` є compatibility alias readiness.
- `promtool` доступний у `/usr/local/bin/promtool`.

Image HEALTHCHECK викликає `http://127.0.0.1:9534/livez`. Якщо raw container перевизначає `--web.listen-address`, потрібно також замінити або вимкнути фіксований Docker healthcheck. Helm chart навмисно фіксує listener на `:9534`, щоб Service і probes не розсинхронізувалися.

## Persistence у Helm

Підтримуються два взаємовиключні режими:

1. `persistence.create=true` створює PVC, який потрібно окремо змонтувати в Prometheus.
2. `persistence.create=false` разом із `persistence.existingClaim=<name>` використовує наявний claim.

Типовий `ReadWriteMany` потрібний, коли Prometheus і config-sync можуть працювати на різних nodes. `ReadWriteOnce` доречний лише за сумісної storage/scheduling моделі.

Chart використовує `Recreate` і дозволяє лише одну replica. Кілька writers не підтримуються.

## HTTP source authentication

У production використовуйте existing Secret:

```bash
helm upgrade --install prometheus-config-sync ./deploy/prometheus-config-sync \
  --set persistence.create=false \
  --set persistence.existingClaim=prometheus-generated \
  --set sourceAuth.existingSecret=prometheus-config-source-client \
  --set sourceAuth.existingSecretKey=token
```

`sourceAuth.create=true` призначений лише для локальної перевірки, оскільки token у values або shell history не є безпечним secret-management contract.

ConfigMap-зміни автоматично запускають rollout. Для external Secret передавайте його версію або digest через `sourceAuth.rolloutChecksum`; зміна цього значення оновлює pod template без розкриття token.

## Security та scheduling

Chart використовує non-root user, видаляє всі Linux capabilities, вмикає RuntimeDefault seccomp, вимикає service-account token mount і залишає root filesystem read-only. Writable mounts обмежено generated PVC і `/tmp`.

ServiceMonitor, PrometheusRule, NetworkPolicy та PDB вимкнені за замовчуванням. NetworkPolicy потрібно вмикати лише з правилами для DNS, HTTP source, reload endpoint і Prometheus scraping.

Optional `PrometheusRule` відповідає standalone rule contract і включає exact health, staged errors, pending reload, unrecovered failures та freshness escalation. `prometheusRule.maxSyncAgeSeconds` керує warning threshold, а `prometheusRule.criticalSyncAgeSeconds` — більшим critical threshold.

Singleton PDB використовує `maxUnavailable: 1`, тому не блокує voluntary maintenance назавжди. Chart відхиляє NetworkPolicy без ingress та egress rules. Керовані runtime flags не можна перевизначити через `config.extraArgs`; для них потрібно використовувати dedicated values. `image.digest` дозволяє immutable image reference.

## systemd

Приклад unit працює від наявного користувача та групи `prometheus`, читає `/etc/default/prometheus-config-sync` і очікує, що configured output directory доступний цьому account для запису.

Native release archive містить binary сервісу та файли в `deploy/systemd/`, але не містить `promtool`. Перед запуском unit потрібно встановити сумісний executable із точним basename `promtool` у `/usr/local/bin/promtool` або спрямувати `PROMETHEUS_CONFIG_SYNC_PROMTOOL_PATH` на інший executable з ім'ям `promtool`. Видалення цієї змінної вимикає validation generated content і не рекомендується для production.

Після встановлення binary та `promtool`:

```bash
sudo install -d -o prometheus -g prometheus -m 0755 /etc/prometheus/generated
sudo install -m 0644 deploy/systemd/prometheus-config-sync.default /etc/default/prometheus-config-sync
sudo install -m 0644 deploy/systemd/prometheus-config-sync.service /etc/systemd/system/prometheus-config-sync.service
sudo systemctl daemon-reload
sudo systemctl enable --now prometheus-config-sync
```

Unit використовує private `/tmp`, захищає operating-system trees через `ProtectSystem=true` і має порожній capability set. Write access визначається звичайними filesystem permissions, а не фіксованим systemd allowlist, тому `PROMETHEUS_CONFIG_SYNC_OUTPUT_DIR` може вказувати на інший writable для `prometheus` каталог. Разом потрібно змінити environment value, ownership каталогу та Prometheus config або mount.

## Validation

```bash
make helm-template-check
make helm-lint
make helm-package
```

Render matrix перевіряє default, development, existing-PVC, managed-token, observability і network-policy modes, а також відхиляє invalid multi-writer, persistence, route та secret combinations.
