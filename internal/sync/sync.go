package sync

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/prometheus/exporter-toolkit/web"
)

type Options struct {
	ToolkitFlags      *web.FlagConfig
	MetricsPath       string
	SourceURL         string
	SourceToken       string
	ReloadURL         string
	OutputDir         string
	PromtoolPath      string
	Interval          time.Duration
	HTTPTimeout       time.Duration
	ValidationTimeout time.Duration
	MaxConfigBytes    int64
	MaxRulesBytes     int64
}

const appliedDigestFile = ".prometheus-config-sync-applied.sha256"
const initialSyncShutdownTimeout = 5 * time.Second
const (
	failureStageFetch       = "fetch"
	failureStageSnapshot    = "snapshot"
	failureStageState       = "state"
	failureStageValidation  = "validation"
	failureStagePublication = "publication"
	failureStageReload      = "reload"
	failureStageMarker      = "marker"
)

var errSourceSnapshotChanged = errors.New("source snapshot changed during fetch")

type syncState struct {
	lastReloadedDigest string
}

const (
	outputFileMode     = 0o644
	outputDirMode      = 0o755
	validationFileMode = 0o600
	validationDirMode  = 0o750
	appliedDigestMode  = 0o600
)

type synchronizeFunc func(context.Context, *http.Client, Options, *slog.Logger, *Metrics, *syncState) error

func Run(ctx context.Context, opts Options, logger *slog.Logger) error {
	return run(ctx, opts, logger, syncOnce)
}

func run(ctx context.Context, opts Options, logger *slog.Logger, synchronize synchronizeFunc) error {
	runCtx, cancelRun := context.WithCancel(ctx)
	defer cancelRun()

	if opts.SourceURL == "" {
		return errors.New("source.url is required")
	}
	if err := validateEndpointURL("source.url", opts.SourceURL); err != nil {
		return err
	}
	if opts.ReloadURL == "" {
		return errors.New("prometheus.reload-url is required")
	}
	if err := validateEndpointURL("prometheus.reload-url", opts.ReloadURL); err != nil {
		return err
	}
	if opts.OutputDir == "" {
		return errors.New("output.dir is required")
	}
	if opts.Interval <= 0 {
		return errors.New("interval must be greater than zero")
	}
	if opts.HTTPTimeout <= 0 {
		return errors.New("http.timeout must be greater than zero")
	}
	if opts.ValidationTimeout <= 0 {
		return errors.New("validation.timeout must be greater than zero")
	}
	if opts.MaxConfigBytes <= 0 {
		return errors.New("source.max-config-bytes must be greater than zero")
	}
	if opts.MaxRulesBytes <= 0 {
		return errors.New("source.max-rules-bytes must be greater than zero")
	}
	if opts.MetricsPath == "" {
		opts.MetricsPath = "/metrics"
	}
	client := &http.Client{Timeout: opts.HTTPTimeout}
	metrics := NewMetrics()
	metrics.MarkHealthy(false)
	handler, err := NewHandler(opts.MetricsPath, metrics.Registry(), metrics)
	if err != nil {
		return err
	}
	if err := cleanupStaleTempFiles(opts.OutputDir); err != nil {
		logger.Warn("failed to clean stale temporary files", "err", err)
	}

	server := &http.Server{
		Handler:           handler,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       5 * time.Second,
		WriteTimeout:      5 * time.Second,
		IdleTimeout:       60 * time.Second,
	}
	serverErr := make(chan error, 1)
	state := &syncState{}
	go func() {
		<-runCtx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.WithoutCancel(runCtx), 5*time.Second)
		defer cancel()
		_ = server.Shutdown(shutdownCtx)
	}()
	go func() {
		logger.Info("starting web server", "metrics_path", opts.MetricsPath)
		err := web.ListenAndServe(server, opts.ToolkitFlags, logger)
		if errors.Is(err, http.ErrServerClosed) {
			err = nil
		}
		serverErr <- err
	}()

	initialSyncDone := make(chan struct{})
	go func() {
		if err := synchronize(runCtx, client, opts, logger, metrics, state); err != nil {
			if runCtx.Err() == nil {
				logger.Error("initial sync failed", "err", err)
			} else {
				logger.Debug("initial sync stopped", "err", err)
			}
		}
		close(initialSyncDone)
	}()

	select {
	case <-runCtx.Done():
		cancelRun()
		if err := waitForInitialSync(initialSyncDone, initialSyncShutdownTimeout); err != nil {
			metrics.MarkHealthy(false)
			return err
		}
		logger.Info("stopping sync loop")
		return nil
	case err := <-serverErr:
		serverResult := webServerError(runCtx, err, metrics)
		cancelRun()
		waitErr := waitForInitialSync(initialSyncDone, initialSyncShutdownTimeout)
		if waitErr != nil {
			metrics.MarkHealthy(false)
		}
		return errors.Join(serverResult, waitErr)
	case <-initialSyncDone:
	}

	ticker := time.NewTicker(opts.Interval)
	defer ticker.Stop()

	for {
		select {
		case <-runCtx.Done():
			logger.Info("stopping sync loop")
			return nil
		case err := <-serverErr:
			return webServerError(runCtx, err, metrics)
		case <-ticker.C:
			if err := synchronize(runCtx, client, opts, logger, metrics, state); err != nil {
				logger.Error("periodic sync failed", "err", err)
			}
		}
	}
}

func waitForInitialSync(done <-chan struct{}, timeout time.Duration) error {
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case <-done:
		return nil
	case <-timer.C:
		return fmt.Errorf("timed out after %s waiting for initial sync to stop", timeout)
	}
}

func webServerError(ctx context.Context, err error, metrics *Metrics) error {
	if err == nil && ctx.Err() != nil {
		return nil
	}
	metrics.MarkHealthy(false)
	if err == nil {
		return errors.New("web server stopped unexpectedly")
	}
	return fmt.Errorf("web server failed: %w", err)
}

func validateEndpointURL(name string, rawURL string) error {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return fmt.Errorf("%s is invalid: %w", name, err)
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return fmt.Errorf("%s must use http or https", name)
	}
	if parsed.Host == "" {
		return fmt.Errorf("%s must include a host", name)
	}
	if parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return fmt.Errorf("%s must not contain credentials, a query, or a fragment", name)
	}
	return nil
}

func syncOnce(ctx context.Context, client *http.Client, opts Options, logger *slog.Logger, metrics *Metrics, state *syncState) error {
	startedAt := time.Now()
	logger.Debug("starting sync", "output_dir", opts.OutputDir)

	assets, err := fetchAssets(ctx, client, opts)
	if err != nil {
		stage := failureStageFetch
		if errors.Is(err, errSourceSnapshotChanged) {
			stage = failureStageSnapshot
		}
		metrics.ObserveSync(time.Since(startedAt), err, stage)
		return err
	}

	digest := assetsDigest(assets)
	currentMatches, err := assetsMatch(opts.OutputDir, assets)
	if err != nil {
		metrics.ObserveSync(time.Since(startedAt), err, failureStageState)
		return err
	}
	appliedDigest, err := readAppliedDigest(opts.OutputDir)
	if err != nil {
		metrics.ObserveSync(time.Since(startedAt), err, failureStageMarker)
		return err
	}
	if currentMatches && appliedDigest == digest {
		logger.Debug("no changes detected")
		metrics.ObserveSync(time.Since(startedAt), nil, "")
		return nil
	}
	if currentMatches && state.lastReloadedDigest == digest {
		if err := persistAppliedDigest(opts.OutputDir, digest); err != nil {
			metrics.ObserveSync(time.Since(startedAt), err, failureStageMarker)
			return err
		}
		logger.Info("persisted applied generation marker", "digest", digest)
		metrics.ObserveSync(time.Since(startedAt), nil, "")
		return nil
	}

	validationCtx, cancel := context.WithTimeout(ctx, opts.ValidationTimeout)
	err = validateAssets(validationCtx, opts, assets)
	cancel()
	if err != nil {
		metrics.ObserveSync(time.Since(startedAt), err, failureStageValidation)
		return err
	}

	if !currentMatches {
		if err := writeAssetsAtomically(opts.OutputDir, assets); err != nil {
			metrics.ObserveSync(time.Since(startedAt), err, failureStagePublication)
			return err
		}
		metrics.MarkPublishedChange()
	}

	if err := reloadPrometheus(ctx, client, opts); err != nil {
		metrics.ObserveSync(time.Since(startedAt), err, failureStageReload)
		return err
	}
	metrics.MarkReload()
	state.lastReloadedDigest = digest
	if err := persistAppliedDigest(opts.OutputDir, digest); err != nil {
		metrics.ObserveSync(time.Since(startedAt), err, failureStageMarker)
		return err
	}

	logger.Info("applied config update",
		"config_sha256", sha256Hex(assets.ScrapeConfig),
		"rules_sha256", sha256Hex(assets.RuleFile),
		"duration", time.Since(startedAt).Round(time.Millisecond),
	)
	metrics.ObserveSync(time.Since(startedAt), nil, "")
	return nil
}

func persistAppliedDigest(outputDir string, digest string) error {
	if err := writeFileAtomically(filepath.Join(outputDir, appliedDigestFile), []byte(digest+"\n"), appliedDigestMode); err != nil {
		return fmt.Errorf("persist applied generation: %w", err)
	}
	return nil
}

type assets struct {
	ScrapeConfig []byte
	RuleFile     []byte
}

func fetchAssets(ctx context.Context, client *http.Client, opts Options) (assets, error) {
	const snapshotAttempts = 3
	const snapshotDelay = 75 * time.Millisecond

	attempt := 0
	for {
		attempt++
		snapshot, err := fetchAssetsOnce(ctx, client, opts.SourceURL, opts.SourceToken, opts.MaxConfigBytes, opts.MaxRulesBytes)
		if err != nil {
			return assets{}, err
		}

		nextSnapshot, fetchErr := fetchAssetsOnce(ctx, client, opts.SourceURL, opts.SourceToken, opts.MaxConfigBytes, opts.MaxRulesBytes)
		if fetchErr != nil {
			return assets{}, fetchErr
		}

		if bytes.Equal(snapshot.ScrapeConfig, nextSnapshot.ScrapeConfig) && bytes.Equal(snapshot.RuleFile, nextSnapshot.RuleFile) {
			return nextSnapshot, nil
		}

		if attempt >= snapshotAttempts {
			return assets{}, fmt.Errorf("%w (attempt %d)", errSourceSnapshotChanged, snapshotAttempts)
		}

		select {
		case <-time.After(snapshotDelay):
		case <-ctx.Done():
			return assets{}, ctx.Err()
		}
	}
}

func fetchAssetsOnce(ctx context.Context, client *http.Client, sourceURL string, token string, maxConfigBytes int64, maxRulesBytes int64) (assets, error) {
	configURL := strings.TrimRight(sourceURL, "/") + "/prometheus/config"
	rulesURL := strings.TrimRight(sourceURL, "/") + "/prometheus/rules"

	configBody, err := fetch(ctx, client, configURL, token, maxConfigBytes)
	if err != nil {
		return assets{}, fmt.Errorf("fetch scrape config: %w", err)
	}
	rulesBody, err := fetch(ctx, client, rulesURL, token, maxRulesBytes)
	if err != nil {
		return assets{}, fmt.Errorf("fetch rules: %w", err)
	}

	return assets{
		ScrapeConfig: configBody,
		RuleFile:     rulesBody,
	}, nil
}

func fetch(ctx context.Context, client *http.Client, url string, token string, maxBytes int64) (_ []byte, retErr error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}

	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() {
		if err := resp.Body.Close(); err != nil {
			retErr = errors.Join(retErr, fmt.Errorf("close response body: %w", err))
		}
	}()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return nil, fmt.Errorf("unexpected status %d from %s: %s", resp.StatusCode, url, strings.TrimSpace(string(body)))
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBytes+1))
	if err != nil {
		return nil, err
	}
	if int64(len(body)) > maxBytes {
		return nil, fmt.Errorf("response from %s exceeds %d bytes", url, maxBytes)
	}
	return body, nil
}

func assetsMatch(outputDir string, content assets) (bool, error) {
	scrapePath := filepath.Join(outputDir, "scrape-configs.yml")
	rulePath := filepath.Join(outputDir, "rules", "generated-rules.yml")

	currentScrape, scrapeExists, err := readOptionalFile(outputDir, scrapePath)
	if err != nil {
		return false, fmt.Errorf("read current scrape config: %w", err)
	}
	currentRules, rulesExist, err := readOptionalFile(outputDir, rulePath)
	if err != nil {
		return false, fmt.Errorf("read current rules: %w", err)
	}
	return scrapeExists && rulesExist && bytes.Equal(currentScrape, content.ScrapeConfig) && bytes.Equal(currentRules, content.RuleFile), nil
}

func writeAssetsAtomically(outputDir string, content assets) error {
	return writeAssets(outputDir, content, writeFileAtomically)
}

type atomicWriteFunc func(string, []byte, os.FileMode) error

func writeAssets(outputDir string, content assets, write atomicWriteFunc) error {
	scrapePath := filepath.Join(outputDir, "scrape-configs.yml")
	rulesDir := filepath.Join(outputDir, "rules")
	rulePath := filepath.Join(rulesDir, "generated-rules.yml")

	if err := os.MkdirAll(outputDir, outputDirMode); err != nil {
		return err
	}
	oldScrape, scrapeExists, err := readOptionalFile(outputDir, scrapePath)
	if err != nil {
		return err
	}
	oldRules, rulesExist, err := readOptionalFile(outputDir, rulePath)
	if err != nil {
		return err
	}

	if err := os.MkdirAll(rulesDir, outputDirMode); err != nil {
		return err
	}

	if err := write(scrapePath, content.ScrapeConfig, outputFileMode); err != nil {
		rollbackErr := restoreFile(scrapePath, oldScrape, scrapeExists)
		return errors.Join(err, wrapError("rollback scrape config", rollbackErr))
	}
	if err := write(rulePath, content.RuleFile, outputFileMode); err != nil {
		rulesRollbackErr := restoreFile(rulePath, oldRules, rulesExist)
		scrapeRollbackErr := restoreFile(scrapePath, oldScrape, scrapeExists)
		return errors.Join(
			err,
			wrapError("rollback rules", rulesRollbackErr),
			wrapError("rollback scrape config", scrapeRollbackErr),
		)
	}
	return nil
}

func wrapError(operation string, err error) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("%s: %w", operation, err)
}

func cleanupStaleTempFiles(outputDir string) error {
	root, err := os.OpenRoot(outputDir)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	defer func() {
		_ = root.Close()
	}()
	targets := []struct {
		dir      string
		prefixes []string
	}{
		{".", []string{"scrape-configs.yml.tmp-", appliedDigestFile + ".tmp-"}},
		{"rules", []string{"generated-rules.yml.tmp-"}},
	}
	var cleanupErr error
	for _, target := range targets {
		dir, err := root.Open(target.dir)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			cleanupErr = errors.Join(cleanupErr, fmt.Errorf("open %s: %w", target.dir, err))
			continue
		}
		entries, readErr := dir.ReadDir(-1)
		closeErr := dir.Close()
		if readErr != nil || closeErr != nil {
			cleanupErr = errors.Join(cleanupErr, wrapError("read "+target.dir, readErr), wrapError("close "+target.dir, closeErr))
			continue
		}
		for _, entry := range entries {
			if !entry.Type().IsRegular() || !hasAnyPrefix(entry.Name(), target.prefixes) {
				continue
			}
			if err := root.Remove(filepath.Join(target.dir, entry.Name())); err != nil {
				cleanupErr = errors.Join(cleanupErr, fmt.Errorf("remove %s: %w", entry.Name(), err))
			}
		}
	}
	return cleanupErr
}

func hasAnyPrefix(value string, prefixes []string) bool {
	for _, prefix := range prefixes {
		if strings.HasPrefix(value, prefix) {
			return true
		}
	}
	return false
}

func readOptionalFile(root string, path string) ([]byte, bool, error) {
	rel, err := pathWithinRoot(root, path)
	if err != nil {
		return nil, false, err
	}

	rootFS, err := os.OpenRoot(root)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, false, nil
		}
		return nil, false, err
	}
	defer func() {
		_ = rootFS.Close()
	}()

	body, err := rootFS.ReadFile(rel)
	if errors.Is(err, os.ErrNotExist) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	return body, true, nil
}

func restoreFile(path string, content []byte, existed bool) error {
	if existed {
		return writeFileAtomically(path, content, outputFileMode)
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

func readAppliedDigest(outputDir string) (string, error) {
	body, exists, err := readOptionalFile(outputDir, filepath.Join(outputDir, appliedDigestFile))
	if err != nil {
		return "", fmt.Errorf("read applied generation: %w", err)
	}
	if !exists {
		return "", nil
	}
	return strings.TrimSpace(string(body)), nil
}

func writeFileAtomically(path string, content []byte, perm os.FileMode) error {
	dir := filepath.Dir(path)
	tmpFile, err := os.CreateTemp(dir, filepath.Base(path)+".tmp-*")
	if err != nil {
		return err
	}

	tmpName := tmpFile.Name()
	defer func() {
		_ = os.Remove(tmpName)
	}()

	if _, err := tmpFile.Write(content); err != nil {
		_ = tmpFile.Close()
		return err
	}
	if err := tmpFile.Chmod(perm); err != nil {
		_ = tmpFile.Close()
		return err
	}
	if err := tmpFile.Sync(); err != nil {
		_ = tmpFile.Close()
		return err
	}
	if err := tmpFile.Close(); err != nil {
		return err
	}

	if err := os.Rename(tmpName, path); err != nil {
		return err
	}
	return syncDirectory(dir)
}

func syncDirectory(path string) (retErr error) {
	root, err := os.OpenRoot(path)
	if err != nil {
		return err
	}
	defer func() {
		retErr = errors.Join(retErr, root.Close())
	}()

	dir, err := root.Open(".")
	if err != nil {
		return err
	}
	defer func() {
		retErr = errors.Join(retErr, dir.Close())
	}()
	return dir.Sync()
}

func validateAssets(ctx context.Context, opts Options, content assets) error {
	if opts.PromtoolPath == "" {
		return nil
	}

	tempDir, err := os.MkdirTemp("", "prometheus-config-sync-validate-*")
	if err != nil {
		return err
	}
	defer func() {
		_ = os.RemoveAll(tempDir)
	}()

	scrapePath := filepath.Join(tempDir, "scrape-configs.yml")
	rulesDir := filepath.Join(tempDir, "rules")
	rulePath := filepath.Join(rulesDir, "generated-rules.yml")
	rootConfigPath := filepath.Join(tempDir, "prometheus.yml")

	if err := os.MkdirAll(rulesDir, validationDirMode); err != nil {
		return err
	}
	if err := os.WriteFile(scrapePath, content.ScrapeConfig, validationFileMode); err != nil {
		return err
	}
	if err := os.WriteFile(rulePath, content.RuleFile, validationFileMode); err != nil {
		return err
	}
	rootConfig := []byte("global:\n  scrape_interval: 15s\n  evaluation_interval: 15s\n\nscrape_config_files:\n  - " + scrapePath + "\n\nrule_files:\n  - " + filepath.Join(rulesDir, "*.yml") + "\n")
	if err := os.WriteFile(rootConfigPath, rootConfig, validationFileMode); err != nil {
		return err
	}

	promtoolPath, err := resolvePromtoolPath(opts.PromtoolPath)
	if err != nil {
		return fmt.Errorf("promtool.path: %w", err)
	}

	if err := runCommand(ctx, promtoolPath, "config", rootConfigPath); err != nil {
		return fmt.Errorf("promtool check config: %w", err)
	}
	if err := runCommand(ctx, promtoolPath, "rules", rulePath); err != nil {
		return fmt.Errorf("promtool check rules: %w", err)
	}

	return nil
}

func runCommand(ctx context.Context, promtoolPath string, mode string, configPath string) error {
	// #nosec G204 -- promtoolPath is validated by resolvePromtoolPath; configPath is derived from tempDir.
	cmd := exec.CommandContext(ctx, promtoolPath, "check", mode, configPath)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%w: %s", err, strings.TrimSpace(string(output)))
	}
	return nil
}

func resolvePromtoolPath(raw string) (string, error) {
	p := raw
	if p == "" {
		return "", errors.New("promtool.path is required for validation")
	}

	p = filepath.Clean(p)
	if strings.Contains(p, "\x00") {
		return "", errors.New("promtool.path contains invalid NUL byte")
	}

	if !filepath.IsAbs(p) {
		var err error
		p, err = exec.LookPath(p)
		if err != nil {
			return "", err
		}
	}
	p = filepath.Clean(p)

	if filepath.Base(p) != "promtool" {
		return "", errors.New("promtool.path executable must be named exactly promtool")
	}

	info, err := os.Stat(p)
	if err != nil {
		return "", err
	}
	if info.IsDir() {
		return "", errors.New("promtool.path must be an executable file")
	}
	if info.Mode()&0o111 == 0 {
		return "", errors.New("promtool.path is not executable")
	}
	return p, nil
}

func pathWithinRoot(root string, target string) (string, error) {
	root = filepath.Clean(root)
	target = filepath.Clean(target)

	rootAbs, err := filepath.Abs(root)
	if err != nil {
		return "", err
	}
	targetAbs, err := filepath.Abs(target)
	if err != nil {
		return "", err
	}

	rel, err := filepath.Rel(rootAbs, targetAbs)
	if err != nil {
		return "", err
	}

	if rel == "." {
		return "", errors.New("path must reference a file, not the root directory")
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("path %q escapes root %q", target, root)
	}
	return rel, nil
}

func reloadPrometheus(ctx context.Context, client *http.Client, opts Options) (retErr error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, opts.ReloadURL, nil)
	if err != nil {
		return err
	}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer func() {
		if err := resp.Body.Close(); err != nil {
			retErr = errors.Join(retErr, fmt.Errorf("close reload response body: %w", err))
		}
	}()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("prometheus reload returned %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	return nil
}

func assetsDigest(content assets) string {
	configHash := sha256.Sum256(content.ScrapeConfig)
	rulesHash := sha256.Sum256(content.RuleFile)
	pairHash := sha256.Sum256(append(configHash[:], rulesHash[:]...))
	return fmt.Sprintf("%x", pairHash[:])
}

func sha256Hex(data []byte) string {
	sum := sha256.Sum256(data)
	return fmt.Sprintf("%x", sum[:])
}
