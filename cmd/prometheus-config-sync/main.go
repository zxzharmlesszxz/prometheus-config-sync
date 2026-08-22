package main

import (
	"context"
	"log/slog"
	"net/url"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/alecthomas/kingpin/v2"
	"github.com/prometheus/common/promslog"
	promflag "github.com/prometheus/common/promslog/flag"
	"github.com/prometheus/common/version"
	webflag "github.com/prometheus/exporter-toolkit/web/kingpinflag"

	syncer "github.com/zxzharmlesszxz/prometheus-config-sync/internal/sync"
)

func main() {
	app := kingpin.New("prometheus-config-sync", "Sync generated Prometheus scrape configs and rules from an HTTP source.")
	app.Version(version.Print("prometheus_config_sync"))
	app.HelpFlag.Short('h')

	promslogCfg := &promslog.Config{}
	promflag.AddFlags(app, promslogCfg)
	toolkitFlags := webflag.AddFlags(app, stringEnv("PROMETHEUS_CONFIG_SYNC_WEB_LISTEN_ADDRESS", ":9534"))

	var sourceURL, sourceToken, reloadURL, outputDir, promtoolPath, metricsPath string
	var interval, httpTimeout, validationTimeout time.Duration
	var maxConfigBytes, maxRulesBytes int64

	app.Flag("source.url", "Base URL of the HTTP source.").
		Envar("PROMETHEUS_CONFIG_SYNC_SOURCE_URL").
		Default("http://127.0.0.1:9876").
		StringVar(&sourceURL)
	app.Flag("source.token", "Bearer token for the HTTP source.").
		Envar("PROMETHEUS_CONFIG_SYNC_SOURCE_TOKEN").
		StringVar(&sourceToken)
	app.Flag("prometheus.reload-url", "Prometheus reload endpoint.").
		Envar("PROMETHEUS_CONFIG_SYNC_PROMETHEUS_RELOAD_URL").
		Default("http://127.0.0.1:9090/-/reload").
		StringVar(&reloadURL)
	app.Flag("output.dir", "Directory for generated scrape configs and rules.").
		Envar("PROMETHEUS_CONFIG_SYNC_OUTPUT_DIR").
		Default("/etc/prometheus/generated").
		StringVar(&outputDir)
	app.Flag("promtool.path", "Optional path to a validation executable named exactly promtool.").
		Envar("PROMETHEUS_CONFIG_SYNC_PROMTOOL_PATH").
		StringVar(&promtoolPath)
	app.Flag("interval", "Sync interval.").
		Envar("PROMETHEUS_CONFIG_SYNC_INTERVAL").
		Default("30s").
		DurationVar(&interval)
	app.Flag("http.timeout", "HTTP timeout for source and reload requests.").
		Envar("PROMETHEUS_CONFIG_SYNC_HTTP_TIMEOUT").
		Default("10s").
		DurationVar(&httpTimeout)
	app.Flag("validation.timeout", "Maximum duration of promtool validation.").
		Envar("PROMETHEUS_CONFIG_SYNC_VALIDATION_TIMEOUT").
		Default("30s").
		DurationVar(&validationTimeout)
	app.Flag("source.max-config-bytes", "Maximum successful HTTP source config response size.").
		Envar("PROMETHEUS_CONFIG_SYNC_MAX_CONFIG_BYTES").
		Default("10485760").
		Int64Var(&maxConfigBytes)
	app.Flag("source.max-rules-bytes", "Maximum successful HTTP source rules response size.").
		Envar("PROMETHEUS_CONFIG_SYNC_MAX_RULES_BYTES").
		Default("10485760").
		Int64Var(&maxRulesBytes)
	app.Flag("web.metrics-path", "Path under which to expose metrics.").
		Envar("PROMETHEUS_CONFIG_SYNC_WEB_METRICS_PATH").
		Default("/metrics").
		StringVar(&metricsPath)

	_, err := app.Parse(os.Args[1:])
	if err != nil {
		kingpin.Fatalf("%s", err)
	}

	logger := promslog.New(promslogCfg)
	slog.SetDefault(logger)
	opts := syncer.Options{
		ToolkitFlags:      toolkitFlags,
		MetricsPath:       metricsPath,
		SourceURL:         sourceURL,
		SourceToken:       sourceToken,
		ReloadURL:         reloadURL,
		OutputDir:         outputDir,
		PromtoolPath:      promtoolPath,
		Interval:          interval,
		HTTPTimeout:       httpTimeout,
		ValidationTimeout: validationTimeout,
		MaxConfigBytes:    maxConfigBytes,
		MaxRulesBytes:     maxRulesBytes,
	}

	logger.Info("Starting prometheus_config_sync", "version", version.Info())
	logger.Info("Build context", "build_context", version.BuildContext())
	logger.Info("Runtime config",
		"source_url", redactURL(opts.SourceURL),
		"source_auth_enabled", opts.SourceToken != "",
		"prometheus_reload_url", redactURL(opts.ReloadURL),
		"output_dir", opts.OutputDir,
		"promtool_enabled", opts.PromtoolPath != "",
		"interval", opts.Interval,
		"http_timeout", opts.HTTPTimeout,
		"validation_timeout", opts.ValidationTimeout,
		"max_config_bytes", opts.MaxConfigBytes,
		"max_rules_bytes", opts.MaxRulesBytes,
		"metrics_path", opts.MetricsPath,
	)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := syncer.Run(ctx, opts, logger); err != nil {
		logger.Error("sync failed", "err", err)
		os.Exit(1)
	}
}

func stringEnv(key string, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}

func redactURL(rawURL string) string {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return "<invalid-url>"
	}
	if parsed.User != nil {
		parsed.User = url.User("redacted")
	}
	parsed.RawQuery = ""
	parsed.Fragment = ""
	return parsed.String()
}
