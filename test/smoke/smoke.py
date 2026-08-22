#!/usr/bin/env python3
import base64
import json
import os
import re
import sys
import urllib.error
import urllib.parse
import urllib.request
from pathlib import Path
from time import monotonic, sleep


TIMEOUT = int(os.getenv("SMOKE_TIMEOUT", "60"))
SCENARIO = os.getenv("SMOKE_SCENARIO", "basic")
REQUIRE_RELOAD = os.getenv("SMOKE_REQUIRE_RELOAD", "true").lower() == "true"
SYNC_URL = os.getenv("SMOKE_SYNC_URL", "http://config-sync:9534")
SOURCE_URL = os.getenv("SMOKE_SOURCE_URL", "http://source:9876")
PROMETHEUS_URL = os.getenv("SMOKE_PROMETHEUS_URL", "http://prometheus:9090")
GRAFANA_URL = os.getenv("SMOKE_GRAFANA_URL", "http://grafana:3000")
GENERATED_DIR = Path(os.getenv("SMOKE_GENERATED_DIR", "/generated"))


def request(url, method="GET", payload=None, auth=None):
    data = None if payload is None else json.dumps(payload).encode()
    headers = {"Content-Type": "application/json"} if data else {}
    if auth:
        headers["Authorization"] = "Basic " + base64.b64encode(auth.encode()).decode()
    req = urllib.request.Request(url, data=data, headers=headers, method=method)
    try:
        with urllib.request.urlopen(req, timeout=5) as response:
            return response.status, response.read(), dict(response.headers)
    except urllib.error.HTTPError as err:
        return err.code, err.read(), dict(err.headers)


def json_get(url, auth=None):
    status, body, _ = request(url, auth=auth)
    if status != 200:
        raise AssertionError(f"GET {url} returned {status}: {body.decode(errors='replace')}")
    return json.loads(body)


def wait(description, assertion):
    deadline = monotonic() + TIMEOUT
    last_error = None
    while monotonic() < deadline:
        try:
            result = assertion()
            if result:
                print(f"[ok] {description}", flush=True)
                return result
        except (AssertionError, OSError, ValueError, urllib.error.URLError) as wait_error:
            last_error = wait_error
        sleep(1)
    raise AssertionError(f"timed out waiting for {description}: {last_error or 'condition remained false'}")


def metric(name):
    status, body, _ = request(SYNC_URL + "/metrics")
    if status != 200:
        raise AssertionError(f"metrics returned {status}")
    match = re.search(rf"^{re.escape(name)}(?:\{{[^}}]*\}})?\s+([0-9.eE+-]+)$", body.decode(), re.MULTILINE)
    if not match:
        raise AssertionError(f"metric {name} is missing")
    return float(match.group(1))


def set_mode(mode):
    status, body, _ = request(SOURCE_URL + "/control/mode", method="POST", payload={"mode": mode})
    if status != 204:
        raise AssertionError(f"failed to set source mode {mode}: {status} {body.decode(errors='replace')}")


def generated_contains(relative_path, marker):
    try:
        return marker in (GENERATED_DIR / relative_path).read_text()
    except FileNotFoundError:
        return False


def ready(expected=200):
    status, body, _ = request(SYNC_URL + "/readyz")
    return status == expected and (expected != 200 or body == b"ok\n")


def target_exists(pool):
    payload = json_get(PROMETHEUS_URL + "/api/v1/targets?state=active")
    return any(target.get("scrapePool") == pool and target.get("health") == "up" for target in payload["data"]["activeTargets"])


def rule_exists(name):
    payload = json_get(PROMETHEUS_URL + "/api/v1/rules")
    return any(rule.get("name") == name for group in payload["data"]["groups"] for rule in group.get("rules", []))


def query_has_data(expression):
    url = PROMETHEUS_URL + "/api/v1/query?" + urllib.parse.urlencode({"query": expression})
    payload = json_get(url)
    return payload.get("status") == "success" and bool(payload.get("data", {}).get("result"))


def basic():
    wait("config-sync liveness", lambda: request(SYNC_URL + "/livez")[0] == 200)
    wait("config-sync readiness", lambda: ready(200))
    wait("config-sync readiness metric", lambda: metric("prometheus_config_sync_healthy") == 1)
    wait("build info metric", lambda: metric("prometheus_config_sync_build_info") == 1)
    wait("successful sync metric", lambda: metric("prometheus_config_sync_syncs_total") >= 1)
    if REQUIRE_RELOAD:
        wait("successful Prometheus reload", lambda: metric("prometheus_config_sync_reloads_total") >= 1)
    elif metric("prometheus_config_sync_reloads_total") != 0:
        raise AssertionError("restart with identical assets unexpectedly reloaded Prometheus")
    else:
        print("[ok] restart with identical assets does not reload Prometheus", flush=True)
    wait("generated base scrape config", lambda: generated_contains("scrape-configs.yml", "generated-config-sync"))
    wait("generated base rule", lambda: generated_contains("rules/generated-rules.yml", "prometheus_config_sync:up"))
    wait("Prometheus readiness", lambda: request(PROMETHEUS_URL + "/-/ready")[0] == 200)
    wait("generated Prometheus target", lambda: target_exists("generated-config-sync"))
    wait("generated Prometheus rule", lambda: rule_exists("prometheus_config_sync:up"))
    wait("dashboard application metric query", lambda: query_has_data("prometheus_config_sync_build_info"))
    wait("dashboard allocation metric query", lambda: query_has_data("go_memstats_alloc_bytes_total{job=\"prometheus-config-sync\"}"))
    wait("Grafana health", lambda: json_get(GRAFANA_URL + "/api/health").get("database") == "ok")
    wait("Grafana datasource", lambda: json_get(GRAFANA_URL + "/api/datasources/uid/prometheus", "admin:admin").get("type") == "prometheus")
    wait("Grafana dashboard", lambda: json_get(GRAFANA_URL + "/api/dashboards/uid/prometheus-config-sync", "admin:admin").get("dashboard", {}).get("uid") == "prometheus-config-sync")


def change():
    set_mode("base")
    basic()
    before = metric("prometheus_config_sync_reloads_total")
    before_changes = metric("prometheus_config_sync_changes_total")
    set_mode("changed")
    wait("changed scrape config publication", lambda: generated_contains("scrape-configs.yml", "generated-config-sync-v2"))
    wait("reload after changed assets", lambda: metric("prometheus_config_sync_reloads_total") > before)
    wait("published change metric", lambda: metric("prometheus_config_sync_changes_total") > before_changes)
    wait("changed Prometheus target", lambda: target_exists("generated-config-sync-v2"))
    wait("changed Prometheus rule", lambda: rule_exists("prometheus_config_sync:fixture_v2"))
    stable = metric("prometheus_config_sync_reloads_total")
    sleep(5)
    if metric("prometheus_config_sync_reloads_total") != stable:
        raise AssertionError("unchanged assets triggered another Prometheus reload")
    print("[ok] unchanged assets do not reload Prometheus", flush=True)
    set_mode("base")
    wait("base config restored", lambda: generated_contains("scrape-configs.yml", "generated-config-sync\n"))
    wait("base target restored", lambda: target_exists("generated-config-sync"))


def failure():
    set_mode("base")
    wait("ready baseline", lambda: ready(200))
    before = metric("prometheus_config_sync_sync_failures_total")
    set_mode("error")
    wait("HTTP source failure metric", lambda: metric("prometheus_config_sync_sync_failures_total") > before)
    wait("unready state during HTTP source outage", lambda: ready(503))
    wait("unhealthy metric during HTTP source outage", lambda: metric("prometheus_config_sync_healthy") == 0)
    set_mode("base")
    wait("readiness recovery after HTTP source restore", lambda: ready(200))
    wait("readiness metric recovery after HTTP source restore", lambda: metric("prometheus_config_sync_healthy") == 1)
    wait("base generated target after recovery", lambda: target_exists("generated-config-sync"))


def validation_failure():
    set_mode("base")
    wait("ready baseline", lambda: ready(200))
    before_config = (GENERATED_DIR / "scrape-configs.yml").read_bytes()
    before_rules = (GENERATED_DIR / "rules/generated-rules.yml").read_bytes()
    before_failures = metric("prometheus_config_sync_sync_failures_total")
    set_mode("invalid")
    wait("invalid payload failure", lambda: metric("prometheus_config_sync_sync_failures_total") > before_failures)
    wait("unready state after invalid payload", lambda: ready(503))
    if (GENERATED_DIR / "scrape-configs.yml").read_bytes() != before_config:
        raise AssertionError("invalid config replaced the last valid scrape config")
    if (GENERATED_DIR / "rules/generated-rules.yml").read_bytes() != before_rules:
        raise AssertionError("invalid config changed the last valid rules")
    print("[ok] invalid assets were not published", flush=True)
    set_mode("base")
    wait("readiness recovery after valid payload", lambda: ready(200))


def reload_retry():
    set_mode("base")
    wait("ready baseline", lambda: ready(200))
    before_failures = metric("prometheus_config_sync_sync_failures_total")
    before_reloads = metric("prometheus_config_sync_reloads_total")
    set_mode("changed")
    wait("reload after changed assets", lambda: metric("prometheus_config_sync_reloads_total") > before_reloads)
    wait("changed Prometheus target", lambda: target_exists("generated-config-sync-v2"))
    stable = metric("prometheus_config_sync_reloads_total")
    set_mode("changed")
    sleep(2)
    if metric("prometheus_config_sync_reloads_total") != stable:
        raise AssertionError("identical published assets triggered an unnecessary reload")
    if metric("prometheus_config_sync_sync_failures_total") != before_failures:
        raise AssertionError("sync failures appeared during unchanged published assets")
    wait("no unnecessary reload for identical assets", lambda: metric("prometheus_config_sync_reloads_total") == stable)
    wait("changed target after reload retry", lambda: target_exists("generated-config-sync-v2"))
    set_mode("base")
    wait("base target restored after reload retry", lambda: target_exists("generated-config-sync"))


SCENARIOS = {
    "basic": basic,
    "change": change,
    "failure": failure,
    "validation": validation_failure,
    "reload-retry": reload_retry,
}


if __name__ == "__main__":
    try:
        SCENARIOS[SCENARIO]()
        print(f"smoke scenario {SCENARIO!r} passed", flush=True)
    except (AssertionError, KeyError, urllib.error.URLError) as scenario_error:
        print(f"smoke scenario {SCENARIO!r} failed: {scenario_error}", file=sys.stderr, flush=True)
        sys.exit(1)
