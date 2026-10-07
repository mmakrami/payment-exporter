// Package exporter collects a fresh, bounded set of probes on each scrape.
package exporter

import (
	"context"
	"fmt"
	"log/slog"
	"os/exec"
	"strconv"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	"paystar/payment-exporter/internal/config"
	"paystar/payment-exporter/internal/probe"
)

type target struct {
	config config.Provider
	http   *probe.HTTP
}

type Exporter struct {
	ctx     context.Context
	logger  *slog.Logger
	targets []target
	limit   int
	mu      sync.Mutex

	up, status, duration, completed, timeout *prometheus.Desc
	mtrSuccess, mtrLoss, mtrLatency          *prometheus.Desc
	latency                                  *prometheus.HistogramVec
	requests                                 *prometheus.CounterVec
	errors                                   *prometheus.CounterVec
}

func New(ctx context.Context, c *config.Config, logger *slog.Logger) (*Exporter, error) {
	desc := func(name, help string) *prometheus.Desc {
		return prometheus.NewDesc("payment_provider_"+name, help, []string{"provider"}, nil)
	}
	e := &Exporter{
		ctx: ctx, logger: logger, limit: c.Global.MaxConcurrency,
		up:         desc("up", "1 if the current HTTP probe completed and returned an explicitly allowed status code; otherwise 0."),
		status:     desc("http_status_code", "Final HTTP response status code; 0 if no usable response was received."),
		duration:   desc("probe_duration_seconds", "Duration of the current HTTP probe including reading the response body."),
		completed:  desc("last_probe_timestamp_seconds", "Unix timestamp when the current HTTP probe completed."),
		timeout:    desc("probe_timeout_seconds", "Configured HTTP timeout for this provider."),
		mtrSuccess: desc("mtr_success", "1 if the current MTR probe produced valid destination statistics; otherwise 0. Independent of HTTP health."),
		mtrLoss:    desc("mtr_packet_loss_ratio", "Destination packet loss ratio (0 to 1) from the current successful MTR probe."),
		mtrLatency: desc("mtr_avg_latency_seconds", "Destination average round-trip time in seconds from the current successful MTR probe."),
		latency: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name: "payment_provider_latency_seconds", Help: "HTTP probe latency including complete response body reading, for successful and failed probes.",
			Buckets: []float64{0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10, 15, 30, 60, 120},
		}, []string{"provider"}),
		requests: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "payment_provider_request_total", Help: "Completed HTTP probes by response code; code 0 means no usable response.",
		}, []string{"provider", "code"}),
		errors: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "payment_provider_probe_errors_total", Help: "Failed HTTP probes by bounded failure category.",
		}, []string{"provider", "reason"}),
	}
	for _, p := range c.Providers {
		if p.MTR.Enabled {
			if _, err := exec.LookPath("mtr"); err != nil {
				e.Close()
				return nil, fmt.Errorf("provider %q: MTR is enabled but mtr is unavailable; use the MTR image", p.Name)
			}
		}
		h, err := probe.NewHTTP(p, c.Global.MaxResponseBodyBytes)
		if err != nil {
			e.Close()
			return nil, fmt.Errorf("provider %q: %w", p.Name, err)
		}
		e.targets = append(e.targets, target{config: p, http: h})
		e.latency.WithLabelValues(p.Name)
		e.requests.WithLabelValues(p.Name, "0")
		for _, code := range p.ExpectedStatusCodes {
			e.requests.WithLabelValues(p.Name, strconv.Itoa(code))
		}
		for _, reason := range []string{"request", "timeout", "canceled", "dns", "tls", "transport", "body", "body_too_large", "status"} {
			e.errors.WithLabelValues(p.Name, reason)
		}
	}
	return e, nil
}

func (e *Exporter) Describe(ch chan<- *prometheus.Desc) {
	for _, d := range []*prometheus.Desc{e.up, e.status, e.duration, e.completed, e.timeout, e.mtrSuccess, e.mtrLoss, e.mtrLatency} {
		ch <- d
	}
	e.latency.Describe(ch)
	e.requests.Describe(ch)
	e.errors.Describe(ch)
}

func (e *Exporter) Collect(ch chan<- prometheus.Metric) {
	// Also protects callers that gather directly rather than via the HTTP handler.
	e.mu.Lock()
	defer e.mu.Unlock()
	type result struct {
		http      probe.HTTPResult
		mtr       probe.MTRResult
		completed float64
	}
	results := make([]result, len(e.targets))
	sem := make(chan struct{}, e.limit)
	var wg sync.WaitGroup
	for i, t := range e.targets {
		wg.Add(1)
		go func() {
			defer wg.Done()
			select {
			case sem <- struct{}{}:
				defer func() { <-sem }()
			case <-e.ctx.Done():
				results[i].http.Error = "canceled"
				results[i].completed = float64(time.Now().UnixNano()) / 1e9
				return
			}
			// MTR and HTTP have independent timeouts and run in parallel.
			var mtrWG sync.WaitGroup
			if t.config.MTR.Enabled {
				mtrWG.Add(1)
				go func() {
					defer mtrWG.Done()
					results[i].mtr = probe.RunMTR(e.ctx, t.config)
				}()
			}
			results[i].http = t.http.Run(e.ctx)
			results[i].completed = float64(time.Now().UnixNano()) / 1e9
			mtrWG.Wait()
		}()
	}
	wg.Wait()
	for i, t := range e.targets {
		r := results[i]
		name := t.config.Name
		e.latency.WithLabelValues(name).Observe(r.http.Duration)
		e.requests.WithLabelValues(name, strconv.Itoa(r.http.StatusCode)).Inc()
		if r.http.Error != "" {
			e.errors.WithLabelValues(name, r.http.Error).Inc()
			e.logger.Debug("HTTP probe failed", "provider", name, "reason", r.http.Error, "status", r.http.StatusCode)
		}
		ch <- prometheus.MustNewConstMetric(e.up, prometheus.GaugeValue, boolFloat(r.http.Success), name)
		ch <- prometheus.MustNewConstMetric(e.status, prometheus.GaugeValue, float64(r.http.StatusCode), name)
		ch <- prometheus.MustNewConstMetric(e.duration, prometheus.GaugeValue, r.http.Duration, name)
		ch <- prometheus.MustNewConstMetric(e.completed, prometheus.GaugeValue, r.completed, name)
		ch <- prometheus.MustNewConstMetric(e.timeout, prometheus.GaugeValue, time.Duration(t.config.Timeout).Seconds(), name)
		if t.config.MTR.Enabled {
			ch <- prometheus.MustNewConstMetric(e.mtrSuccess, prometheus.GaugeValue, boolFloat(r.mtr.Success), name)
			if r.mtr.Success {
				ch <- prometheus.MustNewConstMetric(e.mtrLoss, prometheus.GaugeValue, r.mtr.LossRatio, name)
				ch <- prometheus.MustNewConstMetric(e.mtrLatency, prometheus.GaugeValue, r.mtr.LatencySeconds, name)
			} else {
				e.logger.Debug("MTR probe failed or destination not reached", "provider", name)
			}
		}
	}
	// Collect these after probing so counters/histograms and current gauges agree
	// within the same scrape. They are not separately registered with the registry.
	e.latency.Collect(ch)
	e.requests.Collect(ch)
	e.errors.Collect(ch)
}

func (e *Exporter) Close() {
	for _, t := range e.targets {
		t.http.Close()
	}
}

func boolFloat(b bool) float64 {
	if b {
		return 1
	}
	return 0
}
