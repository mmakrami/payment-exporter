package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"runtime"
	"syscall"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"

	"paystar/payment-exporter/internal/config"
	"paystar/payment-exporter/internal/exporter"
)

var (
	version   = "dev"
	revision  = "unknown"
	buildDate = "unknown"
)

func main() { os.Exit(run()) }

func run() int {
	configPath := flag.String("config.file", "config.yml", "Path to the YAML configuration file.")
	address := flag.String("web.listen-address", ":9106", "Address on which to expose metrics and health endpoints.")
	level := flag.String("log.level", "info", "Log level: debug, info, warn, error.")
	format := flag.String("log.format", "text", "Log format: text or json.")
	check := flag.Bool("config.check", false, "Validate configuration, TLS files and optional MTR availability, then exit.")
	showVersion := flag.Bool("version", false, "Print build information, then exit.")
	health := flag.Bool("healthcheck", false, "Check the local exporter health endpoint, then exit (for Docker).")
	flag.Parse()
	if flag.NArg() != 0 {
		fmt.Fprintln(os.Stderr, "unexpected positional arguments")
		return 2
	}
	if *showVersion {
		fmt.Printf("payment-exporter version=%s revision=%s build_date=%s go=%s\n", version, revision, buildDate, runtime.Version())
		return 0
	}
	if *health {
		return healthcheck(*address)
	}
	var logLevel slog.Level
	if err := logLevel.UnmarshalText([]byte(*level)); err != nil || (*format != "text" && *format != "json") {
		fmt.Fprintln(os.Stderr, "invalid log level or format")
		return 2
	}
	opts := &slog.HandlerOptions{Level: logLevel}
	var logHandler slog.Handler = slog.NewTextHandler(os.Stderr, opts)
	if *format == "json" {
		logHandler = slog.NewJSONHandler(os.Stderr, opts)
	}
	logger := slog.New(logHandler)
	c, err := config.Load(*configPath)
	if err != nil {
		logger.Error("configuration rejected", "error", err)
		return 1
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	e, err := exporter.New(ctx, c, logger)
	if err != nil {
		logger.Error("initialization failed", "error", err)
		return 1
	}
	defer e.Close()
	if *check {
		logger.Info("configuration valid", "providers", len(c.Providers))
		return 0
	}
	registry := prometheus.NewRegistry()
	registry.MustRegister(e, collectors.NewGoCollector(), collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}))
	buildInfo := prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "payment_exporter_build_info", Help: "Build information for payment-exporter.",
	}, []string{"version", "revision", "goversion"})
	buildInfo.WithLabelValues(version, revision, runtime.Version()).Set(1)
	registry.MustRegister(buildInfo)
	listener, err := net.Listen("tcp", *address)
	if err != nil {
		logger.Error("listen failed", "error", err)
		return 1
	}
	server := &http.Server{
		Handler:           exporter.Handler(registry, version),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		IdleTimeout:       60 * time.Second,
		MaxHeaderBytes:    16 * 1024,
		// Probe deadlines bound response time; a fixed WriteTimeout would truncate
		// configurations whose total probe time legitimately exceeds that value.
	}
	serveErr := make(chan error, 1)
	go func() { serveErr <- server.Serve(listener) }()
	logger.Info("payment-exporter listening", "address", listener.Addr().String(), "version", version, "providers", len(c.Providers))
	select {
	case <-ctx.Done():
		logger.Info("shutting down")
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := server.Shutdown(shutdownCtx); err != nil {
			server.Close()
			logger.Error("shutdown failed", "error", err)
			return 1
		}
	case err := <-serveErr:
		if !errors.Is(err, http.ErrServerClosed) {
			logger.Error("HTTP server failed", "error", err)
			return 1
		}
	}
	return 0
}

func healthcheck(address string) int {
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		fmt.Fprintln(os.Stderr, "invalid healthcheck listen address")
		return 1
	}
	if host == "" || host == "0.0.0.0" {
		host = "127.0.0.1"
	} else if host == "::" {
		host = "::1"
	}
	client := &http.Client{Timeout: 2 * time.Second, Transport: &http.Transport{Proxy: nil}}
	resp, err := client.Get("http://" + net.JoinHostPort(host, port) + "/-/healthy")
	if err != nil {
		fmt.Fprintln(os.Stderr, "exporter healthcheck failed")
		return 1
	}
	defer resp.Body.Close()
	io.Copy(io.Discard, io.LimitReader(resp.Body, 1024))
	if resp.StatusCode != http.StatusOK {
		return 1
	}
	return 0
}
