package sync

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/prometheus/exporter-toolkit/web"
)

const (
	baseScrapeConfigText       = "scrape_configs: []\n"
	baseRulesText              = "groups: []\n"
	invalidConfigText          = "invalid: [\n"
	restoredText               = "restored\n"
	oldConfigText              = "old config\n"
	oldRulesText               = "old rules\n"
	newConfigText              = "new config\n"
	newRulesText               = "new rules\n"
	invalidPromtoolExitScript  = "#!/bin/sh\necho invalid >&2\nexit 1\n"
	timeoutPromtoolSleepScript = "#!/bin/sh\nexec sleep 5\n"
)

var (
	baseScrapeConfig           = []byte(baseScrapeConfigText)
	baseRules                  = []byte(baseRulesText)
	restoredTextB              = []byte(restoredText)
	oldConfig                  = []byte(oldConfigText)
	oldRules                   = []byte(oldRulesText)
	newConfig                  = []byte(newConfigText)
	newRules                   = []byte(newRulesText)
	invalidPromtoolExitScriptB = []byte(invalidPromtoolExitScript)
	timeoutPromtoolSleepB      = []byte(timeoutPromtoolSleepScript)
)

type sourcePair struct {
	config string
	rules  string
}

func newSourceTransport(t *testing.T, callCount *atomic.Int32, pairs []sourcePair) roundTripFunc {
	t.Helper()
	return func(r *http.Request) (*http.Response, error) {
		current := int(callCount.Add(1))
		pairIndex := (current - 1) / 2
		if pairIndex >= len(pairs) {
			pairIndex = len(pairs) - 1
		}
		pair := pairs[pairIndex]

		switch r.URL.Path {
		case "/prometheus/config":
			return newResponse(http.StatusOK, pair.config), nil
		case "/prometheus/rules":
			return newResponse(http.StatusOK, pair.rules), nil
		default:
			return newResponse(http.StatusNotFound, "not found\n"), nil
		}
	}
}

func TestFetchAssets(t *testing.T) {
	t.Parallel()

	client := &http.Client{
		Timeout: time.Second,
		Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			switch r.URL.Path {
			case "/prometheus/config":
				return newResponse(http.StatusOK, baseScrapeConfigText), nil
			case "/prometheus/rules":
				return newResponse(http.StatusOK, baseRulesText), nil
			default:
				return newResponse(http.StatusNotFound, "not found\n"), nil
			}
		}),
	}
	opts := Options{
		SourceURL:      "http://example.test",
		HTTPTimeout:    time.Second,
		MaxConfigBytes: 1024,
		MaxRulesBytes:  1024,
	}

	assets, err := fetchAssets(context.Background(), client, opts)
	if err != nil {
		t.Fatalf("fetchAssets() error = %v, want nil", err)
	}
	if !bytes.Equal(assets.ScrapeConfig, baseScrapeConfig) {
		t.Fatalf("fetchAssets() scrape config = %q", assets.ScrapeConfig)
	}
	if !bytes.Equal(assets.RuleFile, baseRules) {
		t.Fatalf("fetchAssets() rule file = %q", assets.RuleFile)
	}
}

func TestFetchAssetsRetriesUntilSnapshotStable(t *testing.T) {
	t.Parallel()

	var callCount atomic.Int32
	pairs := []sourcePair{
		{config: "scrape_configs: [1]\n", rules: "groups: [1]\n"},
		{config: "scrape_configs: [2]\n", rules: "groups: [2]\n"},
	}
	client := &http.Client{
		Timeout:   time.Second,
		Transport: newSourceTransport(t, &callCount, pairs),
	}

	opts := Options{
		SourceURL:      "http://example.test",
		HTTPTimeout:    time.Second,
		MaxConfigBytes: 1024,
		MaxRulesBytes:  1024,
	}

	assets, err := fetchAssets(context.Background(), client, opts)
	if err != nil {
		t.Fatalf("fetchAssets() error = %v, want nil", err)
	}
	if !bytes.Equal(assets.ScrapeConfig, []byte("scrape_configs: [2]\n")) {
		t.Fatalf("fetchAssets() scrape config = %q", assets.ScrapeConfig)
	}
	if !bytes.Equal(assets.RuleFile, []byte("groups: [2]\n")) {
		t.Fatalf("fetchAssets() rule file = %q", assets.RuleFile)
	}
	if got := callCount.Load(); got != 8 {
		t.Fatalf("fetchAssets() source calls = %d, want 8", got)
	}
}

func TestFetchAssetsErrorsOnRepeatedlyUnstableSnapshot(t *testing.T) {
	t.Parallel()

	var callCount atomic.Int32
	// Six snapshot pairs (3 attempts * 2 snapshots each) alternate to guarantee no stable pair forms.
	pairs := []sourcePair{
		{config: "scrape_configs: [1]\n", rules: "groups: [1]\n"},
		{config: "scrape_configs: [2]\n", rules: "groups: [2]\n"},
		{config: "scrape_configs: [1]\n", rules: "groups: [1]\n"},
		{config: "scrape_configs: [2]\n", rules: "groups: [2]\n"},
		{config: "scrape_configs: [1]\n", rules: "groups: [1]\n"},
		{config: "scrape_configs: [2]\n", rules: "groups: [2]\n"},
	}
	client := &http.Client{
		Timeout:   time.Second,
		Transport: newSourceTransport(t, &callCount, pairs),
	}

	_, err := fetchAssets(context.Background(), client, Options{
		SourceURL:      "http://example.test",
		HTTPTimeout:    time.Second,
		MaxConfigBytes: 1024,
		MaxRulesBytes:  1024,
	})
	if err == nil {
		t.Fatal("fetchAssets() error = nil, want error")
	}
	if !strings.Contains(err.Error(), "source snapshot changed during fetch") {
		t.Fatalf("fetchAssets() error = %v, want snapshot-change error", err)
	}
	if got := callCount.Load(); got != 12 {
		t.Fatalf("fetchAssets() source calls = %d, want 12", got)
	}
}

func TestReloadPrometheus(t *testing.T) {
	t.Parallel()

	var called bool
	client := &http.Client{
		Timeout: time.Second,
		Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			if r.Method != http.MethodPost {
				t.Fatalf("reload request method = %s, want POST", r.Method)
			}
			called = true
			return newResponse(http.StatusOK, ""), nil
		}),
	}
	opts := Options{ReloadURL: "http://example.test/-/reload"}

	if err := reloadPrometheus(context.Background(), client, opts); err != nil {
		t.Fatalf("reloadPrometheus() error = %v, want nil", err)
	}
	if !called {
		t.Fatal("reloadPrometheus() did not call reload endpoint")
	}
}

func TestWriteAssetsAtomically(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	content := assets{
		ScrapeConfig: baseScrapeConfig,
		RuleFile:     baseRules,
	}

	if err := writeAssetsAtomically(dir, content); err != nil {
		t.Fatalf("writeAssetsAtomically() error = %v, want nil", err)
	}

	assertFileContent(t, filepath.Join(dir, "scrape-configs.yml"), "scrape_configs: []\n")
	assertFileContent(t, filepath.Join(dir, "rules", "generated-rules.yml"), "groups: []\n")

	changed, err := assetsMatch(dir, content)
	if err != nil {
		t.Fatalf("assetsMatch() error = %v", err)
	}
	if !changed {
		t.Fatal("assetsMatch() = false, want true")
	}
}

func TestWriteAssetsAtomicallyCreatesOutputDir(t *testing.T) {
	t.Parallel()

	base := t.TempDir()
	dir := filepath.Join(base, "nonexistent-output", "child")
	content := assets{
		ScrapeConfig: baseScrapeConfig,
		RuleFile:     baseRules,
	}

	if err := writeAssetsAtomically(dir, content); err != nil {
		t.Fatalf("writeAssetsAtomically() error = %v, want nil", err)
	}

	assertFileContent(t, filepath.Join(dir, "scrape-configs.yml"), "scrape_configs: []\n")
}

func TestSyncOnceNoChangesSkipsReloadAndRewrite(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	content := assets{ScrapeConfig: baseScrapeConfig, RuleFile: baseRules}
	if err := writeAssetsAtomically(dir, content); err != nil {
		t.Fatalf("writeAssetsAtomically() error = %v", err)
	}
	if err := writeFileAtomically(filepath.Join(dir, appliedDigestFile), []byte(assetsDigest(content)+"\n"), appliedDigestMode); err != nil {
		t.Fatalf("writeFileAtomically(applied digest) error = %v", err)
	}

	var calls atomic.Int32
	var reloads atomic.Int32
	client := &http.Client{
		Timeout: time.Second,
		Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			calls.Add(1)
			switch r.URL.Path {
			case "/prometheus/config":
				return newResponse(http.StatusOK, baseScrapeConfigText), nil
			case "/prometheus/rules":
				return newResponse(http.StatusOK, baseRulesText), nil
			case "/-/reload":
				reloads.Add(1)
				return newResponse(http.StatusOK, ""), nil
			default:
				return newResponse(http.StatusNotFound, ""), nil
			}
		}),
	}

	metrics := NewMetrics()
	opts := Options{
		SourceURL:      "http://example.test",
		ReloadURL:      "http://example.test/-/reload",
		OutputDir:      dir,
		HTTPTimeout:    time.Second,
		Interval:       time.Second,
		MaxConfigBytes: 1024,
		MaxRulesBytes:  1024,
	}
	if err := syncOnce(context.Background(), client, opts, discardLogger(), metrics, &syncState{}); err != nil {
		t.Fatalf("syncOnce() error = %v, want nil", err)
	}
	if reloads.Load() != 0 {
		t.Fatalf("reload calls = %d, want 0", reloads.Load())
	}
	if calls.Load() != 4 {
		t.Fatalf("fetch calls = %d, want 4", calls.Load())
	}
	if !metrics.healthy.Load() {
		t.Fatal("syncOnce() did not keep service healthy")
	}
	if got, err := readAppliedDigest(dir); err != nil {
		t.Fatalf("readAppliedDigest() error = %v", err)
	} else if got != assetsDigest(content) {
		t.Fatalf("applied digest = %q, want %q", got, assetsDigest(content))
	}
}

func TestSyncOnceMarksMetricsAndWritesFiles(t *testing.T) {
	t.Parallel()

	client := &http.Client{
		Timeout: time.Second,
		Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			switch r.URL.Path {
			case "/prometheus/config":
				return newResponse(http.StatusOK, baseScrapeConfigText), nil
			case "/prometheus/rules":
				return newResponse(http.StatusOK, baseRulesText), nil
			case "/-/reload":
				return newResponse(http.StatusOK, ""), nil
			default:
				return newResponse(http.StatusNotFound, "not found\n"), nil
			}
		}),
	}
	metrics := NewMetrics()
	logger := discardLogger()
	opts := Options{
		SourceURL:      "http://example.test",
		ReloadURL:      "http://example.test/-/reload",
		OutputDir:      t.TempDir(),
		HTTPTimeout:    time.Second,
		Interval:       time.Second,
		MetricsPath:    "/metrics",
		MaxConfigBytes: 1024,
		MaxRulesBytes:  1024,
	}

	if err := syncOnce(context.Background(), client, opts, logger, metrics, &syncState{}); err != nil {
		t.Fatalf("syncOnce() error = %v, want nil", err)
	}
	if !metrics.healthy.Load() {
		t.Fatal("syncOnce() did not mark metrics healthy")
	}
}

func TestFetchReturnsErrorOnNon200(t *testing.T) {
	t.Parallel()

	client := &http.Client{
		Timeout: time.Second,
		Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			return newResponse(http.StatusInternalServerError, "boom\n"), nil
		}),
	}
	_, err := fetch(context.Background(), client, "http://example.test", "", 1024)
	if err == nil {
		t.Fatal("fetch() error = nil, want error")
	}
	if !strings.Contains(err.Error(), "unexpected status 500") {
		t.Fatalf("fetch() error = %v, want status error", err)
	}
}

func TestSyncOnceValidatesBeforePublishing(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	old := assets{ScrapeConfig: baseScrapeConfig, RuleFile: baseRules}
	if err := writeAssetsAtomically(dir, old); err != nil {
		t.Fatal(err)
	}
	promtool := filepath.Join(t.TempDir(), "promtool")
	if err := os.WriteFile(promtool, invalidPromtoolExitScriptB, 0o755); err != nil {
		t.Fatal(err)
	}

	metrics := NewMetrics()
	err := syncOnce(context.Background(), clientForAssets(invalidConfigText, baseRulesText, http.StatusOK, nil), Options{
		SourceURL: "http://example.test", ReloadURL: "http://example.test/-/reload",
		OutputDir: dir, PromtoolPath: promtool, ValidationTimeout: time.Second,
		MaxConfigBytes: 1024, MaxRulesBytes: 1024,
	}, discardLogger(), metrics, &syncState{})
	if err == nil {
		t.Fatal("syncOnce() error = nil, want validation error")
	}
	assertAssetsEqual(t, dir, old)
	if metrics.healthy.Load() {
		t.Fatal("validation failure marked service healthy")
	}
}

func TestSyncOnceRetriesFailedReloadForIdenticalAssets(t *testing.T) {
	t.Parallel()

	var reloads atomic.Int32
	client := clientForAssets("scrape_configs: []\n", "groups: []\n", 0, func() int {
		if reloads.Add(1) == 1 {
			return http.StatusInternalServerError
		}
		return http.StatusOK
	})
	metrics := NewMetrics()
	state := &syncState{}
	opts := Options{
		SourceURL: "http://example.test", ReloadURL: "http://example.test/-/reload",
		OutputDir: t.TempDir(), ValidationTimeout: time.Second,
		MaxConfigBytes: 1024, MaxRulesBytes: 1024,
	}
	if err := syncOnce(context.Background(), client, opts, discardLogger(), metrics, state); err == nil {
		t.Fatal("first syncOnce() error = nil, want reload error")
	}
	if metrics.healthy.Load() {
		t.Fatal("failed reload marked service healthy")
	}
	if err := syncOnce(context.Background(), client, opts, discardLogger(), metrics, state); err != nil {
		t.Fatalf("second syncOnce() error = %v", err)
	}
	if got := reloads.Load(); got != 2 {
		t.Fatalf("reload requests = %d, want 2", got)
	}
	if !metrics.healthy.Load() {
		t.Fatal("successful retry did not mark service healthy")
	}
	if _, err := os.Stat(filepath.Join(opts.OutputDir, appliedDigestFile)); err != nil {
		t.Fatalf("applied digest marker: %v", err)
	}
}

func TestSyncOncePersistsKnownReloadWithoutReloadingAgain(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	content := assets{ScrapeConfig: baseScrapeConfig, RuleFile: baseRules}
	if err := writeAssetsAtomically(dir, content); err != nil {
		t.Fatal(err)
	}
	var reloads atomic.Int32
	client := clientForAssets(baseScrapeConfigText, baseRulesText, 0, func() int {
		reloads.Add(1)
		return http.StatusOK
	})
	state := &syncState{lastReloadedDigest: assetsDigest(content)}
	err := syncOnce(context.Background(), client, Options{
		SourceURL: "http://example.test", ReloadURL: "http://example.test/-/reload",
		OutputDir: dir, ValidationTimeout: time.Second, MaxConfigBytes: 1024, MaxRulesBytes: 1024,
	}, discardLogger(), NewMetrics(), state)
	if err != nil {
		t.Fatalf("syncOnce() error = %v, want nil", err)
	}
	if reloads.Load() != 0 {
		t.Fatalf("reload requests = %d, want 0", reloads.Load())
	}
	if got, err := readAppliedDigest(dir); err != nil || got != state.lastReloadedDigest {
		t.Fatalf("applied digest = %q, err = %v", got, err)
	}
}

func TestFetchRejectsOversizedResponseAndSendsBearerToken(t *testing.T) {
	t.Parallel()

	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if got := r.Header.Get("Authorization"); got != "Bearer secret" {
			t.Fatalf("Authorization = %q", got)
		}
		return newResponse(http.StatusOK, "12345"), nil
	})}
	if _, err := fetch(context.Background(), client, "http://example.test", "secret", 4); err == nil {
		t.Fatal("fetch() error = nil, want size error")
	}
}

func TestAssetsMatchRequiresBothFiles(t *testing.T) {
	t.Parallel()

	matched, err := assetsMatch(t.TempDir(), assets{})
	if err != nil {
		t.Fatal(err)
	}
	if matched {
		t.Fatal("missing empty files matched empty assets")
	}
}

func TestRestoreFile(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "asset.yml")
	if err := restoreFile(path, restoredTextB, true); err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(body, restoredTextB) {
		t.Fatalf("restored file = %q, err=%v", body, err)
	}
	if err := restoreFile(path, nil, false); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("removed file stat error = %v", err)
	}
}

func TestWriteAssetsRollsBackFirstFileWhenSecondWriteFails(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	old := assets{ScrapeConfig: oldConfig, RuleFile: oldRules}
	if err := writeAssetsAtomically(dir, old); err != nil {
		t.Fatal(err)
	}
	writes := 0
	err := writeAssets(dir, assets{ScrapeConfig: newConfig, RuleFile: newRules}, func(path string, body []byte, mode os.FileMode) error {
		writes++
		if writes == 2 {
			if err := writeFileAtomically(path, body, mode); err != nil {
				return err
			}
			return errors.New("injected rules write failure")
		}
		return writeFileAtomically(path, body, mode)
	})
	if err == nil {
		t.Fatal("writeAssets() error = nil, want injected failure")
	}
	assertAssetsEqual(t, dir, old)
}

func TestWriteAssetsPreservesFilesWhenFirstWriteFails(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	old := assets{ScrapeConfig: oldConfig, RuleFile: oldRules}
	if err := writeAssetsAtomically(dir, old); err != nil {
		t.Fatal(err)
	}
	err := writeAssets(dir, assets{ScrapeConfig: newConfig, RuleFile: newRules}, func(path string, body []byte, mode os.FileMode) error {
		if err := writeFileAtomically(path, body, mode); err != nil {
			return err
		}
		return errors.New("injected scrape write failure")
	})
	if err == nil {
		t.Fatal("writeAssets() error = nil, want injected failure")
	}
	assertAssetsEqual(t, dir, old)
}

func TestCleanupStaleTempFiles(t *testing.T) {
	t.Parallel()

	outputDir := t.TempDir()
	rulesDir := filepath.Join(outputDir, "rules")
	if err := os.Mkdir(rulesDir, outputDirMode); err != nil {
		t.Fatal(err)
	}
	stalePaths := []string{
		filepath.Join(outputDir, "scrape-configs.yml.tmp-stale"),
		filepath.Join(outputDir, appliedDigestFile+".tmp-stale"),
		filepath.Join(rulesDir, "generated-rules.yml.tmp-stale"),
	}
	for _, path := range stalePaths {
		if err := os.WriteFile(path, []byte("stale"), validationFileMode); err != nil {
			t.Fatal(err)
		}
	}
	keepPath := filepath.Join(outputDir, "unrelated.tmp-keep")
	if err := os.WriteFile(keepPath, []byte("keep"), validationFileMode); err != nil {
		t.Fatal(err)
	}
	keepDir := filepath.Join(outputDir, "scrape-configs.yml.tmp-directory")
	if err := os.Mkdir(keepDir, validationDirMode); err != nil {
		t.Fatal(err)
	}

	if err := cleanupStaleTempFiles(outputDir); err != nil {
		t.Fatalf("cleanupStaleTempFiles() error = %v", err)
	}
	for _, path := range stalePaths {
		if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("stale path %s still exists: %v", path, err)
		}
	}
	for _, path := range []string{keepPath, keepDir} {
		if _, err := os.Stat(path); err != nil {
			t.Errorf("preserved path %s: %v", path, err)
		}
	}
	if err := cleanupStaleTempFiles(filepath.Join(outputDir, "missing")); err != nil {
		t.Fatalf("cleanupStaleTempFiles(missing) error = %v", err)
	}
}

func TestCleanupStaleTempFilesDoesNotEscapeOutputRoot(t *testing.T) {
	t.Parallel()

	outputDir := t.TempDir()
	outsideDir := t.TempDir()
	outsidePath := filepath.Join(outsideDir, "generated-rules.yml.tmp-stale")
	if err := os.WriteFile(outsidePath, []byte("outside"), validationFileMode); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outsideDir, filepath.Join(outputDir, "rules")); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	if err := cleanupStaleTempFiles(outputDir); err == nil {
		t.Fatal("cleanupStaleTempFiles() error = nil, want escaping symlink error")
	}
	if _, err := os.Stat(outsidePath); err != nil {
		t.Fatalf("outside file was changed: %v", err)
	}
}

func TestFetchAssetsStopsDuringSnapshotDelay(t *testing.T) {
	t.Parallel()

	var calls atomic.Int32
	client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		if calls.Add(1)%2 == 0 {
			return newResponse(http.StatusOK, "groups: []\n"), nil
		}
		return newResponse(http.StatusOK, fmt.Sprintf("scrape_configs: [%d]\n", calls.Load())), nil
	})}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	_, err := fetchAssets(ctx, client, Options{
		SourceURL: "http://example.test", MaxConfigBytes: 1024, MaxRulesBytes: 1024,
	})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("fetchAssets() error = %v, want context deadline", err)
	}
}

func TestValidateEndpointURL(t *testing.T) {
	t.Parallel()

	for _, rawURL := range []string{
		"ftp://example.test/config",
		"http" + ":///missing-host",
		"https://user:secret@example.test",
		"https://example.test?token=secret",
		"https://example.test#fragment",
	} {
		if err := validateEndpointURL("test.url", rawURL); err == nil {
			t.Errorf("validateEndpointURL(%q) error = nil", rawURL)
		}
	}
	if err := validateEndpointURL("test.url", "https://example.test/base/path"); err != nil {
		t.Fatalf("valid endpoint error = %v", err)
	}
}

func TestValidateAssetsHonorsTimeout(t *testing.T) {
	t.Parallel()

	promtool := filepath.Join(t.TempDir(), "promtool")
	if err := os.WriteFile(promtool, timeoutPromtoolSleepB, 0o755); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	started := time.Now()
	err := validateAssets(ctx, Options{PromtoolPath: promtool}, assets{
		ScrapeConfig: baseScrapeConfig, RuleFile: baseRules,
	})
	if err == nil {
		t.Fatal("validateAssets() error = nil, want timeout")
	}
	if elapsed := time.Since(started); elapsed > time.Second {
		t.Fatalf("validateAssets() took %s after timeout", elapsed)
	}
}

func TestResolvePromtoolPath(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	promtoolPath := filepath.Join(root, "promtool")
	if err := os.WriteFile(promtoolPath, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	execPath, err := resolvePromtoolPath(promtoolPath)
	if err != nil {
		t.Fatalf("resolvePromtoolPath(abs) error = %v", err)
	}
	if got, expected := execPath, filepath.Clean(promtoolPath); got != expected {
		t.Fatalf("resolvePromtoolPath(abs) = %q, want %q", got, expected)
	}

	if _, err := resolvePromtoolPath(""); err == nil {
		t.Fatal("resolvePromtoolPath(\"\") error = nil, want err")
	}
	if _, err := resolvePromtoolPath(filepath.Join(root, "missing")); err == nil {
		t.Fatal("resolvePromtoolPath(missing) error = nil, want err")
	}
	if _, err := resolvePromtoolPath(promtoolPath + "\x00x"); err == nil {
		t.Fatal("resolvePromtoolPath(nul) error = nil, want err")
	}
	versionedPromtool := filepath.Join(root, "promtool-2.53")
	if err := os.WriteFile(versionedPromtool, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := resolvePromtoolPath(versionedPromtool); err == nil || !strings.Contains(err.Error(), "named exactly promtool") {
		t.Fatalf("resolvePromtoolPath(versioned) error = %v", err)
	}

	nonExecPath := filepath.Join(root, "nonexec")
	if err := os.WriteFile(nonExecPath, []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := resolvePromtoolPath(nonExecPath); err == nil {
		t.Fatal("resolvePromtoolPath(nonexec) error = nil, want err")
	}
	dirPath := filepath.Join(root, "bin")
	if err := os.Mkdir(dirPath, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := resolvePromtoolPath(dirPath); err == nil {
		t.Fatal("resolvePromtoolPath(dir) error = nil, want err")
	}
}

func TestPathWithinRoot(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	allowed := filepath.Join(root, "allowed.txt")
	if err := os.WriteFile(allowed, []byte("ok"), 0o600); err != nil {
		t.Fatal(err)
	}
	rel, err := pathWithinRoot(root, allowed)
	if err != nil {
		t.Fatalf("pathWithinRoot(valid) error = %v", err)
	}
	if rel != "allowed.txt" {
		t.Fatalf("pathWithinRoot(valid) = %q, want %q", rel, "allowed.txt")
	}
	if _, err := pathWithinRoot(root, root); err == nil {
		t.Fatal("pathWithinRoot(root, root) error = nil, want err")
	}
	if _, err := pathWithinRoot(root, filepath.Join(root, "..", "escape")); err == nil {
		t.Fatal("pathWithinRoot(root, outside) error = nil, want err")
	}
}

func TestWriteFileAtomically(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "asset.yml")
	content := []byte("payload\n")

	if err := writeFileAtomically(path, content, 0o600); err != nil {
		t.Fatalf("writeFileAtomically() error = %v", err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(content) {
		t.Fatalf("content = %q, want %q", got, content)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("mode = %v, want 0600", info.Mode().Perm())
	}
}

func TestReadAppliedDigest(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	if got, err := readAppliedDigest(dir); err != nil {
		t.Fatalf("readAppliedDigest(empty) error = %v", err)
	} else if got != "" {
		t.Fatalf("readAppliedDigest(empty) = %q, want empty", got)
	}

	if err := writeFileAtomically(filepath.Join(dir, appliedDigestFile), []byte("abc\n"), appliedDigestMode); err != nil {
		t.Fatal(err)
	}
	got, err := readAppliedDigest(dir)
	if err != nil {
		t.Fatalf("readAppliedDigest() error = %v", err)
	}
	if got != "abc" {
		t.Fatalf("readAppliedDigest() = %q, want %q", got, "abc")
	}
	info, err := os.Stat(filepath.Join(dir, appliedDigestFile))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != appliedDigestMode {
		t.Fatalf("applied digest mode = %v, want %v", info.Mode().Perm(), appliedDigestMode)
	}
}

func TestRunReturnsWebListenerFailure(t *testing.T) {
	t.Parallel()

	address := "invalid-listen-address"
	webConfig := ""
	systemdSocket := false

	err := Run(context.Background(), Options{
		ToolkitFlags: &web.FlagConfig{
			WebListenAddresses: &[]string{address}, WebSystemdSocket: &systemdSocket, WebConfigFile: &webConfig,
		},
		MetricsPath: "/metrics", SourceURL: "http://127.0.0.1:1", ReloadURL: "http://127.0.0.1:1/-/reload",
		OutputDir: t.TempDir(), Interval: time.Hour, HTTPTimeout: time.Second,
		ValidationTimeout: time.Second, MaxConfigBytes: 1024, MaxRulesBytes: 1024,
	}, discardLogger())
	if err == nil || !strings.Contains(err.Error(), "web server failed") {
		t.Fatalf("Run() error = %v, want web server failure", err)
	}
}

func TestRunReturnsWebListenerFailureDuringInitialSync(t *testing.T) {
	t.Parallel()

	source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
	}))
	defer source.Close()
	address := "invalid-listen-address"
	webConfig := ""
	systemdSocket := false
	startedAt := time.Now()
	err := Run(context.Background(), Options{
		ToolkitFlags: &web.FlagConfig{
			WebListenAddresses: &[]string{address}, WebSystemdSocket: &systemdSocket, WebConfigFile: &webConfig,
		},
		MetricsPath: "/metrics", SourceURL: source.URL, ReloadURL: "http://127.0.0.1:1/-/reload",
		OutputDir: t.TempDir(), Interval: time.Hour, HTTPTimeout: time.Hour,
		ValidationTimeout: time.Second, MaxConfigBytes: 1024, MaxRulesBytes: 1024,
	}, discardLogger())
	if err == nil || !strings.Contains(err.Error(), "web server failed") {
		t.Fatalf("Run() error = %v, want web server failure", err)
	}
	if elapsed := time.Since(startedAt); elapsed > time.Second {
		t.Fatalf("listener failure detection took %v", elapsed)
	}
}

func TestRunStopsWhenContextCanceled(t *testing.T) {
	t.Parallel()

	address := "127.0.0.1:0"
	webConfig := ""
	systemdSocket := false

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	opts := Options{
		ToolkitFlags: &web.FlagConfig{
			WebListenAddresses: &[]string{address},
			WebSystemdSocket:   &systemdSocket,
			WebConfigFile:      &webConfig,
		},
		MetricsPath:       "/metrics",
		SourceURL:         "http://127.0.0.1:1",
		ReloadURL:         "http://127.0.0.1:1/-/reload",
		OutputDir:         t.TempDir(),
		Interval:          50 * time.Millisecond,
		HTTPTimeout:       10 * time.Millisecond,
		ValidationTimeout: 10 * time.Millisecond,
		MaxConfigBytes:    1024,
		MaxRulesBytes:     1024,
	}

	go func() {
		done <- Run(ctx, opts, discardLogger())
	}()
	cancel()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Run() error = %v, want nil", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Run() did not stop on context cancel")
	}
}

func TestRunWaitsForInitialSyncAfterCancellation(t *testing.T) {
	t.Parallel()

	address := "127.0.0.1:0"
	webConfig := ""
	systemdSocket := false
	started := make(chan struct{})
	canceled := make(chan struct{})
	release := make(chan struct{})
	outputDir := t.TempDir()
	synchronize := func(ctx context.Context, _ *http.Client, _ Options, _ *slog.Logger, _ *Metrics, _ *syncState) error {
		close(started)
		<-ctx.Done()
		close(canceled)
		<-release
		return ctx.Err()
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- run(ctx, Options{
			ToolkitFlags: &web.FlagConfig{
				WebListenAddresses: &[]string{address}, WebSystemdSocket: &systemdSocket, WebConfigFile: &webConfig,
			},
			MetricsPath: "/metrics", SourceURL: "http://127.0.0.1:1", ReloadURL: "http://127.0.0.1:1/-/reload",
			OutputDir: outputDir, Interval: time.Hour, HTTPTimeout: time.Second,
			ValidationTimeout: time.Second, MaxConfigBytes: 1024, MaxRulesBytes: 1024,
		}, discardLogger(), synchronize)
	}()
	<-started
	cancel()
	<-canceled
	select {
	case err := <-done:
		close(release)
		t.Fatalf("run() returned before initial sync completed: %v", err)
	case <-time.After(20 * time.Millisecond):
	}
	close(release)
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("run() error = %v, want nil", err)
		}
	case <-time.After(time.Second):
		t.Fatal("run() did not return after initial sync completed")
	}
}

func TestWaitForInitialSyncReportsTimeout(t *testing.T) {
	t.Parallel()

	err := waitForInitialSync(make(chan struct{}), time.Millisecond)
	if err == nil || !strings.Contains(err.Error(), "1ms") {
		t.Fatalf("waitForInitialSync() error = %v, want timeout duration", err)
	}
}

func clientForAssets(config, rules string, fixedReloadStatus int, nextReloadStatus func() int) *http.Client {
	return &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		switch r.URL.Path {
		case "/prometheus/config":
			return newResponse(http.StatusOK, config), nil
		case "/prometheus/rules":
			return newResponse(http.StatusOK, rules), nil
		case "/-/reload":
			status := fixedReloadStatus
			if nextReloadStatus != nil {
				status = nextReloadStatus()
			}
			return newResponse(status, ""), nil
		default:
			return newResponse(http.StatusNotFound, ""), nil
		}
	})}
}

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func assertAssetsEqual(t *testing.T, dir string, want assets) {
	t.Helper()
	gotScrape, err := os.ReadFile(filepath.Join(dir, "scrape-configs.yml"))
	if err != nil {
		t.Fatal(err)
	}
	gotRules, err := os.ReadFile(filepath.Join(dir, "rules", "generated-rules.yml"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(gotScrape, want.ScrapeConfig) || !bytes.Equal(gotRules, want.RuleFile) {
		t.Fatalf("assets changed: scrape=%q rules=%q", gotScrape, gotRules)
	}
}

func assertFileContent(t *testing.T, path string, expected string) {
	t.Helper()

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	if string(got) != expected {
		t.Fatalf("%s content = %q, want %q", path, got, expected)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) {
	return f(r)
}

func newResponse(status int, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Status:     http.StatusText(status),
		Header:     make(http.Header),
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}
