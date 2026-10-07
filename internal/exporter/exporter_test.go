package exporter

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"

	"paystar/payment-exporter/internal/config"
)

func testExporter(t *testing.T, providers []config.Provider, concurrency int) (*Exporter, *prometheus.Registry) {
	t.Helper()
	c := &config.Config{Providers: providers, Global: config.Global{MaxConcurrency: concurrency}}
	if err := c.Validate(t.TempDir()); err != nil {
		t.Fatal(err)
	}
	e, err := New(context.Background(), c, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(e.Close)
	reg := prometheus.NewPedanticRegistry()
	reg.MustRegister(e)
	return e, reg
}

func TestScrapeFreshMetricsAndCounters(t *testing.T) {
	var calls atomic.Int32
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls.Add(1); w.WriteHeader(401) }))
	defer s.Close()
	_, reg := testExporter(t, []config.Provider{{Name: "Shahin", URL: s.URL, ExpectedStatusCodes: []int{401}}}, 1)
	if calls.Load() != 0 {
		t.Fatal("exporter sent an unexpected background probe")
	}
	for i := 1; i <= 2; i++ {
		want := fmt.Sprintf(`# HELP payment_provider_up 1 if the current HTTP probe completed and returned an explicitly allowed status code; otherwise 0.
# TYPE payment_provider_up gauge
payment_provider_up{provider="Shahin"} 1
# HELP payment_provider_request_total Completed HTTP probes by response code; code 0 means no usable response.
# TYPE payment_provider_request_total counter
payment_provider_request_total{code="0",provider="Shahin"} 0
payment_provider_request_total{code="401",provider="Shahin"} %d
`, i)
		if err := testutil.GatherAndCompare(reg, strings.NewReader(want), "payment_provider_up", "payment_provider_request_total"); err != nil {
			t.Fatal(err)
		}
	}
	if calls.Load() != 2 {
		t.Fatalf("expected one probe per scrape, got %d", calls.Load())
	}
}

func TestConcurrencyBoundAndParallelism(t *testing.T) {
	var active, maximum, calls atomic.Int32
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		n := active.Add(1)
		defer active.Add(-1)
		for old := maximum.Load(); n > old; old = maximum.Load() {
			if maximum.CompareAndSwap(old, n) {
				break
			}
		}
		time.Sleep(20 * time.Millisecond)
		w.WriteHeader(200)
	}))
	defer s.Close()
	var providers []config.Provider
	for i := 0; i < 6; i++ {
		providers = append(providers, config.Provider{Name: fmt.Sprint(i), URL: s.URL})
	}
	_, reg := testExporter(t, providers, 2)
	if _, err := reg.Gather(); err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 6 || maximum.Load() != 2 {
		t.Fatalf("concurrency incorrect: calls=%d max=%d", calls.Load(), maximum.Load())
	}
}

func TestHealthDoesNotProbeAndOverlappingScrapesAreBounded(t *testing.T) {
	entered := make(chan struct{}, 1)
	release := make(chan struct{})
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		entered <- struct{}{}
		<-release
		w.WriteHeader(200)
	}))
	defer s.Close()
	_, reg := testExporter(t, []config.Provider{{Name: "test", URL: s.URL}}, 1)
	h := httptest.NewServer(Handler(reg, "test"))
	defer h.Close()
	for _, path := range []string{"/", "/-/healthy", "/-/ready"} {
		resp, err := http.Get(h.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != 200 || len(entered) != 0 {
			t.Fatalf("health or landing page triggered probe: %s", path)
		}
	}
	done := make(chan int, 1)
	go func() {
		resp, err := http.Get(h.URL + "/metrics")
		if err != nil {
			done <- 0
			return
		}
		io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
		done <- resp.StatusCode
	}()
	<-entered
	resp, err := http.Get(h.URL + "/metrics")
	if err != nil {
		close(release)
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 503 {
		close(release)
		t.Fatalf("overlapping scrape should be rejected, got %d", resp.StatusCode)
	}
	close(release)
	if status := <-done; status != 200 {
		t.Fatalf("first scrape failed: %d", status)
	}
}

func TestShutdownCancelsInflightProbe(t *testing.T) {
	entered := make(chan struct{})
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(entered)
		<-r.Context().Done()
	}))
	defer s.Close()
	c := &config.Config{Providers: []config.Provider{{Name: "test", URL: s.URL}}}
	if err := c.Validate(t.TempDir()); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	e, err := New(ctx, c, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	defer e.Close()
	reg := prometheus.NewPedanticRegistry()
	reg.MustRegister(e)
	done := make(chan error, 1)
	go func() { _, err := reg.Gather(); done <- err }()
	<-entered
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("in-flight probe did not stop")
	}
}
