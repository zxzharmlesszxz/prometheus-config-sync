# Локальне smoke-тестування

Репозиторій містить container-only black-box suite для повного потоку від
HTTP source до Prometheus. На host потрібен лише Docker із Compose v2; усі
assertions виконуються всередині ізольованого Python-контейнера.
Helper image, test client, fixture server і детерміновані HTTP source payloads розміщено в [`test/smoke`](../test/smoke/README.md).

## Топологія

```text
smoke-test ---> HTTP source fixture ---> config-sync ---> shared generated volume
                    control API             |                    |
                                            +----------------> Prometheus <-----------+
                                               |
                                 Grafana <------+
```

HTTP source fixture обслуговує штатні `/prometheus/config` і
`/prometheus/rules`, а приватні control endpoints дозволяють перемикати
base, changed, invalid та intentional-error режими. З production-сервісами
fixture не взаємодіє.

## Повний suite

```sh
make smoke
```

Після успіху або помилки стек залишається запущеним для діагностики. Suite
перевіряє:

- process liveness, synchronization readiness, точний readiness gauge, build metrics і успішну синхронізацію config-sync;
- згенеровані scrape config і rules на спільному volume;
- активні generated targets і завантажені rules у Prometheus;
- health Grafana, provisioned datasource і dashboard;
- один increment published change і reload після зміни assets без повторення для ідентичних assets;
- unready state та failure metrics під час недоступності HTTP source;
- відмову публікувати невалідний YAML зі збереженням останніх валідних файлів;
- відсутність зайвих reload для незмінного згенерованого payload;
- автоматичне відновлення readiness після повернення HTTP source;
- коректну зупинку config-sync, exit code `0`, restart, повторний acceptance та
  відсутність зайвого reload, якщо збережені assets ідентичні;
- structured lifecycle logs, actionable invalid-startup failure та коректний
  SIGTERM під час невдалого початкового HTTP source request;
- runtime UID, ownership файлів, вбудований `promtool`, кількість процесів,
  PID 1 file descriptors та інформаційний Docker resource snapshot.

Зупинити стек і видалити його disposable volumes:

```sh
make smoke-down
```

## Окремі сценарії

| Команда                          | Призначення                                                               |
|----------------------------------|---------------------------------------------------------------------------|
| `make smoke-up`                  | Зібрати й перевідтворити HTTP source, Prometheus, config-sync та Grafana. |
| `make smoke-fixtures`            | Зібрати helper image та перевірити детерміновані Prometheus fixtures.     |
| `make smoke-test`                | Перевірити базовий health та observability contract.                      |
| `make smoke-change-test`         | Перевірити змінені та незмінені HTTP source payloads.                     |
| `make smoke-failure-test`        | Перевірити збій HTTP source і відновлення.                                |
| `make smoke-validation-test`     | Перевірити, що invalid assets не замінюють валідну generation.            |
| `make smoke-reload-retry-test`   | Перевірити, що незмінний payload не викликає зайвого reload.              |
| `make smoke-restart-test`        | Перезапустити config-sync і повторити acceptance.                         |
| `make smoke-runtime-test`        | Перевірити runtime identity, ownership, tools та idle bounds.             |
| `make smoke-runtime-compat-test` | Перевірити UID/GID, generated ownership та embedded tools.                |
| `make smoke-resource-test`       | Перевірити process і PID 1 file-descriptor limits.                        |
| `make smoke-log-test`            | Перевірити очікувані structured lifecycle records.                        |
| `make smoke-fatal-log-test`      | Вимагати actionable error для некоректного startup.                       |
| `make smoke-startup-signal-test` | Перевірити SIGTERM під час initial-sync failure.                          |
| `make smoke-compatibility`       | Зібрати та перевірити Alpine, Bookworm і Trixie images.                   |
| `make smoke-logs`                | Стежити за логами всіх сервісів.                                          |
| `make smoke-down`                | Зупинити стек і видалити disposable volumes.                              |

Сценарні цілі очікують стек, запущений через `make smoke-up`.

## Локальні адреси

Усі опубліковані порти прив'язані лише до `127.0.0.1`.

| Сервіс              | Типова адреса                               |
|---------------------|---------------------------------------------|
| config-sync         | <http://127.0.0.1:9534>                     |
| Prometheus          | <http://127.0.0.1:9090>                     |
| HTTP source fixture | <http://127.0.0.1:9876>                     |
| Grafana             | <http://127.0.0.1:3000> (`admin` / `admin`) |

Config-sync віддає process liveness на `/livez`, synchronization readiness на `/readyz`, compatibility readiness alias `/healthz` і metrics на `/metrics`.

Якщо host-порт зайнятий, перевизначте `SOURCE_PORT`, `PROMETHEUS_PORT`,
`CONFIG_SYNC_PORT` або `GRAFANA_PORT`. Внутрішні адреси контейнерів не
змінюються. `SMOKE_TIMEOUT`, `SMOKE_INTERVAL`, `SMOKE_MAX_IDLE_PROCESSES` і
`SMOKE_MAX_PID1_FDS` керують retry, polling та resource gates. `make smoke-up`
явно застосовує `SMOKE_INTERVAL`, тому локальний `.env` розробника не може
зробити timing assertions формальними.

## Діагностика та межі

Після помилки залиште стек запущеним і перевірте:

```sh
docker compose ps
make smoke-logs
docker compose exec config-sync ls -lR /etc/prometheus/generated
```

Suite перевіряє packaged service із детермінованими локальними doubles. Він не
перевіряє production HTTP source, Kubernetes PVC semantics, remote
authentication/TLS або multi-node storage.
