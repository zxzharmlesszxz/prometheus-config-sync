package main

import "testing"

func TestRedactURL(t *testing.T) {
	t.Parallel()

	got := redactURL("https://user:secret@example.test/path?token=secret#fragment")
	if got != "https://redacted@example.test/path" {
		t.Fatalf("redactURL() = %q", got)
	}
}

func TestRedactURLRejectsMalformedURL(t *testing.T) {
	t.Parallel()

	if got := redactURL("https://example.test/%zz"); got != "<invalid-url>" {
		t.Fatalf("redactURL() = %q", got)
	}
}

func TestStringEnv(t *testing.T) {
	t.Setenv("PROMETHEUS_CONFIG_SYNC_TEST_VALUE", "configured")
	if got := stringEnv("PROMETHEUS_CONFIG_SYNC_TEST_VALUE", "fallback"); got != "configured" {
		t.Fatalf("stringEnv() = %q", got)
	}
	t.Setenv("PROMETHEUS_CONFIG_SYNC_TEST_VALUE", "")
	if got := stringEnv("PROMETHEUS_CONFIG_SYNC_TEST_VALUE", "fallback"); got != "fallback" {
		t.Fatalf("stringEnv() fallback = %q", got)
	}
}
