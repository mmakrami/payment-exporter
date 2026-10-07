package exporter

import (
	"fmt"
	"html"
	"net/http"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

func Handler(registry *prometheus.Registry, version string) http.Handler {
	mux := http.NewServeMux()
	mux.Handle("/metrics", promhttp.HandlerFor(registry, promhttp.HandlerOpts{
		// A second overlapping scrape gets 503 rather than launching duplicate
		// payment requests or building an unbounded queue behind a slow provider.
		MaxRequestsInFlight: 1,
		EnableOpenMetrics:   true,
	}))
	for _, path := range []string{"/-/healthy", "/-/ready"} {
		mux.HandleFunc(path, func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodGet && r.Method != http.MethodHead {
				w.Header().Set("Allow", "GET, HEAD")
				http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
				return
			}
			w.Header().Set("Content-Type", "text/plain; charset=utf-8")
			fmt.Fprintln(w, "OK")
		})
	}
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		fmt.Fprintf(w, landingPage, html.EscapeString(version))
	})
	return mux
}

const landingPage = `<!doctype html>
<html lang="en"><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1">
<title>Payment Exporter</title><style>
body{font:16px system-ui,sans-serif;background:#0f172a;color:#e2e8f0;max-width:640px;margin:12vh auto;padding:24px}
h1{font-size:36px;letter-spacing:-1px}p{line-height:1.7;color:#cbd5e1}a{color:#67e8f9}code{color:#a5b4fc}
.badge{display:inline-block;padding:5px 10px;border:1px solid #334155;border-radius:8px;font-size:13px}
</style><main><span class="badge">Prometheus exporter · %s</span><h1>Payment Exporter</h1>
<p>HTTP availability monitoring with explicit status rules, TLS / mTLS, and optional network diagnostics.</p>
<p><a href="/metrics">Metrics</a> · <a href="/-/healthy">Health</a> · <a href="/-/ready">Readiness</a></p>
<p>Each metrics scrape performs fresh probes. Health and readiness describe the exporter process.</p></main></html>`
