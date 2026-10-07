// A deterministic local HTTP/mTLS fixture used only by container integration tests.
package main

import (
	"crypto/tls"
	"crypto/x509"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strings"
	"time"
)

func main() {
	listen := flag.String("listen", ":8080", "HTTP address")
	cert := flag.String("cert", "", "Server TLS certificate")
	key := flag.String("key", "", "Server TLS private key")
	ca := flag.String("ca", "", "Client certificate CA")
	flag.Parse()
	mux := http.NewServeMux()
	mux.HandleFunc("/healthy", func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, "healthy") })
	mux.HandleFunc("/auth", func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		if r.Method != "POST" || string(body) != "{}" || r.Header.Get("Content-Type") != "application/json" || r.Header.Get("Authorization") != "Bearer integration-only" {
			w.WriteHeader(400)
			return
		}
		w.WriteHeader(401)
	})
	mux.HandleFunc("/notfound", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(404) })
	mux.HandleFunc("/rate-limit", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(429) })
	mux.HandleFunc("/slow-body", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
		w.(http.Flusher).Flush()
		time.Sleep(500 * time.Millisecond)
		fmt.Fprint(w, "late body")
	})
	mux.HandleFunc("/oversized", func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, strings.Repeat("x", 8192)) })
	mux.HandleFunc("/redirect", func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, "/healthy", 302) })
	mux.HandleFunc("/mtls", func(w http.ResponseWriter, r *http.Request) {
		if r.TLS == nil || len(r.TLS.VerifiedChains) == 0 {
			w.WriteHeader(403)
			return
		}
		w.WriteHeader(401)
	})
	if *cert != "" {
		data, err := os.ReadFile(*ca)
		if err != nil {
			log.Fatal(err)
		}
		pool := x509.NewCertPool()
		if !pool.AppendCertsFromPEM(data) {
			log.Fatal("invalid fixture CA")
		}
		srv := &http.Server{Addr: ":8443", Handler: mux, ReadHeaderTimeout: 5 * time.Second, TLSConfig: &tls.Config{ClientAuth: tls.RequireAndVerifyClientCert, ClientCAs: pool, MinVersion: tls.VersionTLS12}}
		go func() { log.Fatal(srv.ListenAndServeTLS(*cert, *key)) }()
	}
	server := &http.Server{Addr: *listen, Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	log.Fatal(server.ListenAndServe())
}
