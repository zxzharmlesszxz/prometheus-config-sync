# Метрики

[English](METRICS.md)

Типовий endpoint — `GET /metrics` на порту `9534`. Registry також містить build info (`prometheus_config_sync_build_info`), Go runtime і process metrics.

| Метрика                                                 | Тип       | Значення                                           |
|---------------------------------------------------------|-----------|----------------------------------------------------|
| `prometheus_config_sync_build_info`                     | gauge     | Версія збірки, revision, branch, версія Go         |
| `prometheus_config_sync_syncs_total`                    | counter   | Завершені sync-спроби                              |
| `prometheus_config_sync_sync_failures_total`            | counter   | Невдалі sync-спроби                                |
| `prometheus_config_sync_sync_errors_total{stage}`       | counter   | Помилки за bounded processing stage                |
| `prometheus_config_sync_reloads_total`                  | counter   | Успішні reload-запити                              |
| `prometheus_config_sync_changes_total`                  | counter   | Опубліковані зміни generated content               |
| `prometheus_config_sync_healthy`                        | gauge     | Результат останнього sync: `1` healthy, `0` failed |
| `prometheus_config_sync_last_success_timestamp_seconds` | gauge     | Час останньої успішної синхронізації               |
| `prometheus_config_sync_last_failure_timestamp_seconds` | gauge     | Час останньої невдалої синхронізації               |
| `prometheus_config_sync_last_reload_timestamp_seconds`  | gauge     | Час останнього успішного reload                    |
| `prometheus_config_sync_last_change_timestamp_seconds`  | gauge     | Час останньої успішної публікації змінених assets  |
| `prometheus_config_sync_sync_duration_seconds`          | histogram | Тривалість завершених спроб                        |

До першої відповідної події timestamp gauge дорівнює `0`; у PromQL це значення потрібно трактувати як “ще не спостерігалося”. Standalone alerts і Grafana dashboard розміщено в `examples/`.

Для freshness синхронізації використовуйте `last_success_timestamp_seconds`. `last_change_timestamp_seconds` фіксує лише publication нового content і може необмежено залишатися незмінною, коли HTTP source стабільно повертає ту саму healthy generation.

`sync_errors_total` використовує лише bounded stages `fetch`, `snapshot`, `state`, `validation`, `publication`, `reload`, `marker` і defensive fallback `unknown`. Error messages та dynamic values ніколи не стають labels. `healthy` є Prometheus-представленням readiness із `/readyz` та `/healthz`; `/livez` і Prometheus `up` показують process та scrape availability.

`last_success` оновлюється лише для вже applied generation або після повного ланцюжка validation, publication, успішного reload і запису marker. Reload failure залишає health негативним і повторюється в наступному циклі.

Repository містить standalone rules у [examples/prometheus/alerts/prometheus-config-sync.yml](examples/prometheus/alerts/prometheus-config-sync.yml) та еквівалентний optional Helm `PrometheusRule`. Обидва покривають target availability, точний sync health, freshness, staged errors, pending reload, unrecovered failure та critical staleness. Optional `ServiceMonitor` і `PrometheusRule` потребують Prometheus Operator CRDs.
