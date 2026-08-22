include Makefile.mk

.PHONY: help fmt fmt-check tidy tidy-check mod-download build vet lint lint-install lint-version actionlint shellcheck python-check dashboard-check govulncheck gosec security-go test test-race coverage coverage-check release-archives release-checksums release release-smoke docker-build docker-build-alpine docker-build-bookworm docker-build-trixie docker-build-comparison docker-check docker-runtime-smoke docker-smoke docker-buildx docker-buildx-push docker-push compose compose-up compose-down compose-logs compose-ps compose-config compose-smoke http-smoke smoke-fixtures smoke-up smoke-test smoke-change-test smoke-failure-test smoke-validation-test smoke-reload-retry-test smoke-restart-test smoke-runtime-compat-test smoke-resource-test smoke-runtime-test smoke-log-test smoke-fatal-log-test smoke-startup-signal-test smoke-logs smoke-down smoke smoke-compatibility prometheus-config-check prometheus-rules-check helm-lint helm-template-check helm-package check full-check ci clean size
.SILENT: compose compose-config compose-down compose-logs compose-ps compose-up size
.NOTPARALLEL: full-check ci release release-smoke

help: ## Show available make targets.
	@printf "\033[33mUsage:\033[0m\n"
	@grep -hE '^[a-zA-Z0-9_-]+:.*?## .*$$' $(MAKEFILE_LIST) | sort | awk 'BEGIN {FS = ":.*?## "};{printf "\033[36m%-30s\033[0m %s\n", $$1, $$2}'

fmt: ## Format Go files.
	$(GOFMT) -w $(GO_FILES)

fmt-check: ## Check Go formatting.
	@test -z "$$($(GOFMT) -l $(GO_FILES))"

tidy: ## Run go mod tidy.
	$(GO) mod tidy

tidy-check: ## Require go.mod and go.sum to already be tidy.
	$(GO) mod tidy -diff

mod-download: ## Download Go modules.
	$(GO) mod download

build: ## Build the service binary into dist/.
	mkdir -p $(DIST_DIR)
	CGO_ENABLED=$(CGO_ENABLED) $(GO) build -buildvcs=false -trimpath -ldflags "$(LDFLAGS)" -o $(BUILD_OUTPUT) $(MAIN_PACKAGE)
	$(MAKE) size

vet: ## Run go vet.
	$(GO) vet ./...

lint-install: ## Install the project-pinned golangci-lint version with the active Go toolchain.
	$(GO) install $(GOLANGCI_LINT_MODULE)/cmd/golangci-lint@$(GOLANGCI_LINT_VERSION)

lint-version: ## Require the project golangci-lint version and build toolchain.
	@version="$$($(GOLANGCI_LINT) version)"; \
	echo "$$version" | grep -F "version $(patsubst v%,%,$(GOLANGCI_LINT_VERSION)) " >/dev/null || { echo "required golangci-lint $(GOLANGCI_LINT_VERSION), got: $$version"; exit 1; }; \
	echo "$$version" | grep -F "built with go$(GO_VERSION) " >/dev/null || { echo "required golangci-lint built with go$(GO_VERSION), got: $$version"; exit 1; }

lint: lint-version ## Run golangci-lint.
	PATH="$(dir $(GO)):$$PATH" $(GOLANGCI_LINT) run ./...

actionlint: ## Validate GitHub Actions workflows.
	$(ACTIONLINT)

shellcheck: ## Validate shell scripts.
	$(SHELLCHECK) $(SHELL_FILES)

python-check: ## Compile Python smoke helpers without writing into the repository.
	@cache="$$(mktemp -d)"; trap 'rm -rf "$$cache"' EXIT INT TERM; PYTHONPYCACHEPREFIX="$$cache" $(PYTHON) -m py_compile $(PYTHON_FILES)

dashboard-check: ## Validate the provisioned Grafana dashboard JSON.
	$(JQ) empty examples/grafana/prometheus-config-sync-dashboard.json
	$(PYTHON) scripts/dashboard-check.py examples/grafana/prometheus-config-sync-dashboard.json

govulncheck: ## Scan reachable Go code for known vulnerabilities.
	$(GO) run golang.org/x/vuln/cmd/govulncheck@$(GOVULNCHECK_VERSION) ./...

gosec: ## Run Go security static analysis.
	$(GO) run github.com/securego/gosec/v2/cmd/gosec@$(GOSEC_VERSION) -exclude-generated ./...

security-go: govulncheck gosec ## Run Go vulnerability and security scans.

test: ## Run Go tests.
	$(GO) test -buildvcs=false ./...

test-race: ## Run Go tests with the race detector.
	CGO_ENABLED=1 $(GO) test -buildvcs=false -race ./...

coverage: ## Run tests with coverage and write reports.
	$(GO) test -buildvcs=false -covermode=atomic -coverprofile=$(COVERAGE_PROFILE) ./...
	$(GO) tool cover -func=$(COVERAGE_PROFILE) | tee $(COVERAGE_REPORT)

coverage-check: coverage ## Enforce the coverage threshold.
	@coverage="$$(awk '/^total:/ {gsub(/%/, "", $$3); print $$3}' $(COVERAGE_REPORT))"; \
	awk -v coverage="$$coverage" -v threshold="$(COVERAGE_THRESHOLD)" 'BEGIN { \
		if (coverage + 0 < threshold + 0) { printf "coverage %.1f%% is below %.1f%%\n", coverage, threshold; exit 1 } \
		printf "coverage %.1f%% meets %.1f%%\n", coverage, threshold \
	}'

release-archives: ## Cross-build release archives into dist/.
	mkdir -p $(DIST_DIR)
	@set -eu; \
	for platform in $(PLATFORMS); do \
		goos="$${platform%/*}"; goarch="$${platform#*/}"; \
		archive="$(PROJECT_NAME)_$(VERSION)_$${goos}_$${goarch}"; workdir="$(DIST_DIR)/$$archive"; binary="$(PROJECT_NAME)"; \
		if [ "$$goos" = windows ]; then binary="$$binary.exe"; fi; \
		rm -rf "$$workdir"; mkdir -p "$$workdir"; \
		CGO_ENABLED=$(CGO_ENABLED) GOOS="$$goos" GOARCH="$$goarch" $(GO) build -buildvcs=false -trimpath -ldflags "$(LDFLAGS)" -o "$$workdir/$$binary" $(MAIN_PACKAGE); \
		cp README.md ARCHITECTURE.md METRICS.md DEPLOYMENT.md "$$workdir/"; \
		mkdir -p "$$workdir/deploy"; cp -R deploy/systemd "$$workdir/deploy/"; \
		COPYFILE_DISABLE=1 tar -C $(DIST_DIR) -czf "$(DIST_DIR)/$$archive.tar.gz" "$$archive"; rm -rf "$$workdir"; \
	done

release-checksums: release-archives ## Write SHA256 checksums for release archives.
	@set -eu; cd $(DIST_DIR); \
	if command -v sha256sum >/dev/null 2>&1; then sha256sum *.tar.gz > checksums.txt; else shasum -a 256 *.tar.gz > checksums.txt; fi; cat checksums.txt

release: ## Build release archives and checksums from a clean output directory.
	$(MAKE) clean
	$(MAKE) release-checksums

release-smoke: release ## Build release archives and smoke-test the native archive.
	@set -eu; goos="$$($(GO) env GOOS)"; goarch="$$($(GO) env GOARCH)"; \
	archive="$(DIST_DIR)/$(PROJECT_NAME)_$(VERSION)_$${goos}_$${goarch}.tar.gz"; tmp="$$(mktemp -d)"; trap 'rm -rf "$$tmp"' EXIT; \
	COPYFILE_DISABLE=1 tar -C "$$tmp" -xzf "$$archive"; binary="$$tmp/$(PROJECT_NAME)_$(VERSION)_$${goos}_$${goarch}/$(PROJECT_NAME)"; \
	"$$binary" --help >/dev/null 2>&1; "$$binary" --version 2>&1 | grep -F "$(VERSION)" >/dev/null

docker-build: ## Build the Docker image.
	$(DOCKER) build --build-arg LDFLAGS="$(LDFLAGS)" --build-arg IMAGE_SOURCE="$(IMAGE_SOURCE)" --build-arg RUNTIME_IMAGE="$(RUNTIME_IMAGE)" --build-arg RUNTIME_FAMILY="$(RUNTIME_FAMILY)" -t $(DOCKER_IMAGE) .

docker-build-alpine: ## Build the Alpine runtime image.
	$(DOCKER) build --build-arg LDFLAGS="$(LDFLAGS)" --build-arg IMAGE_SOURCE="$(IMAGE_SOURCE)" --build-arg RUNTIME_IMAGE="$(ALPINE_RUNTIME_IMAGE)" --build-arg RUNTIME_FAMILY=alpine -t $(DOCKER_ALPINE_IMAGE) .

docker-build-bookworm: ## Build the Debian Bookworm slim runtime image.
	$(DOCKER) build --build-arg LDFLAGS="$(LDFLAGS)" --build-arg IMAGE_SOURCE="$(IMAGE_SOURCE)" --build-arg RUNTIME_IMAGE="$(BOOKWORM_RUNTIME_IMAGE)" --build-arg RUNTIME_FAMILY=debian -t $(DOCKER_BOOKWORM_IMAGE) .

docker-build-trixie: ## Build the Debian Trixie slim runtime image.
	$(DOCKER) build --build-arg LDFLAGS="$(LDFLAGS)" --build-arg IMAGE_SOURCE="$(IMAGE_SOURCE)" --build-arg RUNTIME_IMAGE="$(TRIXIE_RUNTIME_IMAGE)" --build-arg RUNTIME_FAMILY=debian -t $(DOCKER_TRIXIE_IMAGE) .

docker-build-comparison: docker-build-alpine docker-build-bookworm docker-build-trixie ## Build all supported runtime variants.

docker-check: ## Run Dockerfile static build checks.
	$(DOCKER) build --check .

docker-runtime-smoke: ## Verify an already-built image user, tools, metadata, help, and version.
	@set -eu; test "$$($(DOCKER) run --rm --entrypoint id $(DOCKER_IMAGE) -u)" = 10001; \
	test "$$($(DOCKER) run --rm --entrypoint id $(DOCKER_IMAGE) -g)" = 10001; \
	test "$$($(DOCKER) image inspect --format '{{ .Config.User }}' $(DOCKER_IMAGE))" = "10001:10001"; \
	$(DOCKER) run --rm $(DOCKER_IMAGE) --help >/dev/null 2>&1; \
	$(DOCKER) run --rm $(DOCKER_IMAGE) --version 2>&1 | grep -F "$(VERSION)" >/dev/null; \
	$(DOCKER) run --rm --entrypoint promtool $(DOCKER_IMAGE) --version >/dev/null; \
	test "$$($(DOCKER) image inspect --format '{{ index .Config.Labels "org.opencontainers.image.source" }}' $(DOCKER_IMAGE))" = "$(IMAGE_SOURCE)"; \
	test "$$($(DOCKER) image inspect --format '{{ index .Config.Labels "org.opencontainers.image.title" }}' $(DOCKER_IMAGE))" = "$(PROJECT_NAME)"

docker-smoke: docker-build ## Build and smoke-test the default Docker image.
	$(MAKE) docker-runtime-smoke

docker-buildx: ## Build a multi-platform image.
	$(DOCKER) buildx build --platform $(DOCKER_PLATFORMS) --build-arg LDFLAGS="$(LDFLAGS)" --build-arg IMAGE_SOURCE="$(IMAGE_SOURCE)" -t $(DOCKER_IMAGE) .

docker-buildx-push: ## Build and push a multi-platform image.
	$(DOCKER) buildx build --push --platform $(DOCKER_PLATFORMS) --build-arg LDFLAGS="$(LDFLAGS)" --build-arg IMAGE_SOURCE="$(IMAGE_SOURCE)" -t $(DOCKER_IMAGE) .

docker-push: ## Push the Docker image.
	$(DOCKER) push $(DOCKER_IMAGE)

compose: ## Run Docker Compose. Override COMPOSE_ARGS as needed.
	$(DOCKER_COMPOSE) $(COMPOSE_ARGS)

compose-up: ## Start the local stack.
	$(MAKE) compose COMPOSE_ARGS="up --build"

compose-down: ## Stop the local stack without deleting volumes.
	$(MAKE) compose COMPOSE_ARGS="down --remove-orphans"

compose-logs: ## Follow local stack logs.
	$(MAKE) compose COMPOSE_ARGS="logs -f"

compose-ps: ## Show local stack services.
	$(MAKE) compose COMPOSE_ARGS="ps"

compose-config: ## Validate the Compose model.
	$(MAKE) compose COMPOSE_ARGS="config --quiet"

http-smoke: ## Smoke-test a running service.
	$(CURL) --fail --silent --show-error "$(SMOKE_BASE_URL)/livez" >/dev/null
	$(CURL) --fail --silent --show-error "$(SMOKE_BASE_URL)/readyz" >/dev/null
	$(CURL) --fail --silent --show-error "$(SMOKE_BASE_URL)/metrics" | grep -F prometheus_config_sync_build_info >/dev/null
	$(CURL) --fail --silent --show-error "$(SMOKE_BASE_URL)/metrics" | grep -E '^prometheus_config_sync_reloads_total [1-9][0-9]*' >/dev/null

compose-smoke: ## Start the local stack and verify service plus Prometheus readiness.
	$(MAKE) smoke-up
	$(MAKE) smoke-test

smoke-fixtures: compose-config prometheus-config-check ## Build the smoke helper and validate deterministic fixtures.
	SMOKE_TEST_IMAGE=$(SMOKE_TEST_IMAGE) $(DOCKER_COMPOSE) --profile smoke build source smoke-test

smoke-up: smoke-fixtures ## Build and start the observable local smoke stack.
	SMOKE_TEST_IMAGE=$(SMOKE_TEST_IMAGE) PROMETHEUS_CONFIG_SYNC_INTERVAL=$(SMOKE_INTERVAL) $(DOCKER_COMPOSE) up -d --build --force-recreate --wait source prometheus config-sync grafana

smoke-test: ## Run baseline health, generated-file, Prometheus, and Grafana assertions.
	SMOKE_TEST_IMAGE=$(SMOKE_TEST_IMAGE) SMOKE_TIMEOUT=$(SMOKE_TIMEOUT) SMOKE_SCENARIO=basic $(DOCKER_COMPOSE) --profile smoke run --rm --no-deps -e SMOKE_REQUIRE_RELOAD=$(SMOKE_REQUIRE_RELOAD) smoke-test

smoke-change-test: ## Verify changed assets reload once and unchanged assets do not reload.
	SMOKE_TEST_IMAGE=$(SMOKE_TEST_IMAGE) SMOKE_TIMEOUT=$(SMOKE_TIMEOUT) SMOKE_SCENARIO=change $(DOCKER_COMPOSE) --profile smoke run --rm --no-deps smoke-test

smoke-failure-test: ## Verify HTTP source outage detection and automatic health recovery.
	SMOKE_TEST_IMAGE=$(SMOKE_TEST_IMAGE) SMOKE_TIMEOUT=$(SMOKE_TIMEOUT) SMOKE_SCENARIO=failure $(DOCKER_COMPOSE) --profile smoke run --rm --no-deps smoke-test

smoke-validation-test: ## Verify invalid assets never replace the last valid generation.
	SMOKE_TEST_IMAGE=$(SMOKE_TEST_IMAGE) SMOKE_TIMEOUT=$(SMOKE_TIMEOUT) SMOKE_SCENARIO=validation $(DOCKER_COMPOSE) --profile smoke run --rm --no-deps smoke-test

smoke-reload-retry-test: ## Verify identical published assets do not trigger redundant reload.
	SMOKE_TEST_IMAGE=$(SMOKE_TEST_IMAGE) SMOKE_TIMEOUT=$(SMOKE_TIMEOUT) SMOKE_SCENARIO=reload-retry $(DOCKER_COMPOSE) --profile smoke run --rm --no-deps smoke-test

smoke-restart-test: ## Require a clean config-sync stop/start and repeat baseline assertions.
	@set -eu; \
	$(DOCKER_COMPOSE) stop --timeout 15 config-sync; \
	container="$$($(DOCKER_COMPOSE) ps -aq config-sync)"; \
	test "$$($(DOCKER) inspect --format '{{.State.ExitCode}}' "$$container")" = 0; \
	$(DOCKER) start "$$container" >/dev/null
	$(MAKE) smoke-test SMOKE_REQUIRE_RELOAD=false

smoke-runtime-compat-test: ## Verify runtime identity, generated-file ownership, and embedded promtool.
	@set -eu; \
	test "$$($(DOCKER_COMPOSE) exec -T config-sync id -u)" = 10001; \
	test "$$($(DOCKER_COMPOSE) exec -T config-sync id -g)" = 10001; \
	test "$$($(DOCKER_COMPOSE) exec -T config-sync stat -c %u /etc/prometheus/generated/scrape-configs.yml)" = 10001; \
	test "$$($(DOCKER_COMPOSE) exec -T config-sync stat -c %g /etc/prometheus/generated/scrape-configs.yml)" = 10001; \
	$(DOCKER_COMPOSE) exec -T config-sync promtool --version >/dev/null

smoke-resource-test: ## Enforce idle process and PID1 file-descriptor bounds.
	@set -eu; \
	processes="$$($(DOCKER_COMPOSE) exec -T config-sync sh -c 'find /proc -maxdepth 1 -type d -name "[0-9]*" | wc -l')"; \
	fds="$$($(DOCKER_COMPOSE) exec -T config-sync sh -c 'find /proc/1/fd -mindepth 1 -maxdepth 1 | wc -l')"; \
	echo "config-sync idle processes=$$processes pid1_fds=$$fds"; \
	container="$$($(DOCKER_COMPOSE) ps -q config-sync)"; \
	$(DOCKER) stats --no-stream --format 'config-sync idle memory={{.MemUsage}} cpu={{.CPUPerc}} pids={{.PIDs}}' "$$container"; \
	test "$$processes" -le "$(SMOKE_MAX_IDLE_PROCESSES)"; \
	test "$$fds" -le "$(SMOKE_MAX_PID1_FDS)"

smoke-runtime-test: smoke-runtime-compat-test smoke-resource-test ## Run all runtime compatibility and resource assertions.

smoke-log-test: ## Require expected lifecycle records and structured logfmt output.
	@logs="$$(mktemp)"; trap 'rm -f "$$logs"' EXIT INT TERM; \
	$(DOCKER_COMPOSE) logs --no-color --no-log-prefix config-sync > "$$logs"; \
	grep -F 'Starting prometheus_config_sync' "$$logs" >/dev/null; \
	grep -F 'applied config update' "$$logs" >/dev/null; \
	awk 'NF && ($$0 !~ /level=/ || $$0 !~ /msg=/) { print; invalid=1 } END { exit invalid }' "$$logs"

smoke-fatal-log-test: ## Require invalid startup to exit non-zero with an actionable fatal log.
	@temporary="$$(mktemp)"; trap 'rm -f "$$temporary"' EXIT INT TERM; \
	if $(DOCKER) run --rm $(SMOKE_APP_IMAGE) --interval=0s > "$$temporary" 2>&1; then \
		echo "invalid container unexpectedly exited successfully" >&2; exit 1; \
	fi; \
	grep -F 'interval must be greater than zero' "$$temporary" >/dev/null

smoke-startup-signal-test: ## Signal PID1 during failed initial sync and require a clean exit.
	@set -eu; \
	container="$$($(DOCKER) run -d $(SMOKE_APP_IMAGE) --interval=1h)"; \
	trap '$(DOCKER) rm -f "$$container" >/dev/null 2>&1 || true' EXIT INT TERM; \
	sleep 1; \
	$(DOCKER) kill --signal TERM "$$container" >/dev/null; \
	test "$$($(DOCKER) wait "$$container")" = 0

smoke-logs: ## Follow logs from the local smoke stack.
	$(DOCKER_COMPOSE) logs -f source prometheus config-sync grafana

smoke-down: ## Stop the smoke stack and delete its local volumes.
	$(DOCKER_COMPOSE) --profile smoke down --volumes --remove-orphans

smoke: smoke-up ## Run the complete local black-box acceptance suite.
	$(MAKE) smoke-test
	$(MAKE) smoke-change-test
	$(MAKE) smoke-failure-test
	$(MAKE) smoke-validation-test
	$(MAKE) smoke-reload-retry-test
	$(MAKE) smoke-restart-test
	$(MAKE) smoke-runtime-test
	$(MAKE) smoke-log-test
	$(MAKE) smoke-fatal-log-test
	$(MAKE) smoke-startup-signal-test

smoke-compatibility: docker-build-comparison ## Smoke-test Alpine, Bookworm, and Trixie runtime images.
	$(MAKE) docker-runtime-smoke DOCKER_IMAGE=$(DOCKER_ALPINE_IMAGE)
	$(MAKE) docker-runtime-smoke DOCKER_IMAGE=$(DOCKER_BOOKWORM_IMAGE)
	$(MAKE) docker-runtime-smoke DOCKER_IMAGE=$(DOCKER_TRIXIE_IMAGE)

prometheus-config-check: ## Validate the local Prometheus configuration.
	@set -eu; \
	tmp="$$(mktemp -d)"; \
	trap 'rm -rf "$$tmp"' EXIT; \
	mkdir -p "$$tmp/generated/rules"; \
	printf '%s\n' \
	  'global:' \
	  '  scrape_interval: 15s' \
	  '  evaluation_interval: 15s' \
	  '' \
	  'scrape_configs:' \
	  '  - job_name: prometheus' \
	  '    static_configs:' \
	  '      - targets: ["prometheus:9090"]' \
	  '  - job_name: prometheus-config-source' \
	  '    static_configs:' \
	  '      - targets: ["source:9876"]' \
	  '  - job_name: prometheus-config-sync' \
	  '    static_configs:' \
	  '      - targets: ["config-sync:9534"]' \
	  '' \
	  'scrape_config_files:' \
	  '  - /etc/prometheus/generated/scrape-configs*.yml' \
	  '' \
	  'rule_files:' \
	  '  - /etc/prometheus/rules/prometheus-config-sync.yml' \
	  '  - /etc/prometheus/generated/rules/*.yml' \
	  >"$$tmp/prometheus.base.yml"; \
	$(DOCKER) run --rm --entrypoint promtool \
		-v "$$tmp/prometheus.base.yml:/etc/prometheus/prometheus.yml:ro" \
		-v "$$tmp/generated:/etc/prometheus/generated:ro" \
		-v "$(CURDIR)/examples/prometheus/alerts:/etc/prometheus/rules:ro" \
		$(PROMTOOL_IMAGE) check config /etc/prometheus/prometheus.yml

prometheus-rules-check: ## Validate standalone Prometheus alert rules.
	$(DOCKER) run --rm --entrypoint promtool -v "$(CURDIR)/examples/prometheus/alerts:/rules:ro" $(PROMTOOL_IMAGE) check rules /rules/prometheus-config-sync.yml
	$(DOCKER) run --rm --entrypoint promtool -v "$(CURDIR)/examples/prometheus/alerts:/rules:ro" $(PROMTOOL_IMAGE) test rules /rules/prometheus-config-sync.test.yml
	@set -eu; rendered="$$(mktemp)"; rules="$$(mktemp)"; trap 'rm -f "$$rendered" "$$rules"' EXIT; \
	$(HELM) template prometheus-config-sync $(CHART_DIR) --show-only templates/prometheusrule.yaml --set prometheusRule.enabled=true > "$$rendered"; \
	awk 'found { sub(/^  /, ""); print } /^spec:/ { found=1 }' "$$rendered" > "$$rules"; \
	$(DOCKER) run --rm -i --entrypoint promtool $(PROMTOOL_IMAGE) check rules /dev/stdin < "$$rules"

helm-template-check: ## Render supported Helm modes and reject invalid configurations.
	scripts/helm-template-check.sh

helm-lint: helm-template-check ## Lint the Helm chart.
	$(HELM) lint $(CHART_DIR)

helm-package: ## Package the Helm chart into dist/charts/.
	mkdir -p $(CHART_DIST_DIR)
	$(HELM) package $(CHART_DIR) --destination $(CHART_DIST_DIR) $(if $(CHART_VERSION),--version $(CHART_VERSION)) $(if $(APP_VERSION),--app-version $(APP_VERSION))

check: fmt-check tidy-check vet lint actionlint shellcheck python-check dashboard-check coverage-check compose-config helm-lint prometheus-config-check prometheus-rules-check ## Run the standard local gate.

full-check: check test-race build release-smoke docker-check ## Run all local checks, build, and release smoke.

ci: full-check security-go docker-smoke smoke ## Run the complete release gate.

clean: ## Remove generated artifacts.
	rm -rf $(DIST_DIR)
	rm -f $(COVERAGE_PROFILE) $(COVERAGE_REPORT)

size:
	@du -h $(BUILD_OUTPUT)* 2>/dev/null || true
