package probe

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"paystar/payment-exporter/internal/config"
)

func testHTTP(t *testing.T, p config.Provider, maxBody int64) *HTTP {
	t.Helper()
	c := &config.Config{Providers: []config.Provider{p}, Global: config.Global{MaxResponseBodyBytes: maxBody}}
	if err := c.Validate(t.TempDir()); err != nil {
		t.Fatal(err)
	}
	h, err := NewHTTP(c.Providers[0], c.Global.MaxResponseBodyBytes)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(h.Close)
	return h
}

func TestHTTPExactStatusAllowList(t *testing.T) {
	for _, status := range []int{200, 201, 400, 401, 404, 429, 500, 507} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(status) }))
			defer s.Close()
			h := testHTTP(t, config.Provider{Name: "test", URL: s.URL, ExpectedStatusCodes: []int{401, 507}}, 0)
			r := h.Run(context.Background())
			if r.StatusCode != status || r.Success != (status == 401 || status == 507) {
				t.Fatalf("incorrect result: %+v", r)
			}
		})
	}
}

func TestHTTPRequestMethodHeadersBody(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		if r.Method != "POST" || string(body) != `{"action":"probe"}` || r.Header.Get("Authorization") != "Bearer value" || r.Header.Get("Content-Type") != "application/json" || r.Host != "virtual.example" {
			w.WriteHeader(400)
			return
		}
		w.WriteHeader(204)
	}))
	defer s.Close()
	h := testHTTP(t, config.Provider{Name: "test", URL: s.URL, Method: "POST", Headers: map[string]string{"Authorization": "Bearer value", "Content-Type": "application/json", "Host": "virtual.example"}, Body: `{"action":"probe"}`, ExpectedStatusCodes: []int{204}}, 0)
	if r := h.Run(context.Background()); !r.Success {
		t.Fatalf("request configuration not respected: %+v", r)
	}
}

func TestLatencyIncludesBodyAndTimeoutFailsHealth(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
		w.(http.Flusher).Flush()
		time.Sleep(80 * time.Millisecond)
		fmt.Fprint(w, "body")
	}))
	defer s.Close()
	h := testHTTP(t, config.Provider{Name: "full", URL: s.URL, Timeout: config.Duration(time.Second)}, 0)
	if r := h.Run(context.Background()); !r.Success || r.Duration < 0.075 {
		t.Fatalf("body latency omitted: %+v", r)
	}
	h = testHTTP(t, config.Provider{Name: "timeout", URL: s.URL, Timeout: config.Duration(20 * time.Millisecond)}, 0)
	if r := h.Run(context.Background()); r.Success || r.StatusCode != 200 || r.Error != "timeout" {
		t.Fatalf("body timeout incorrectly marked healthy: %+v", r)
	}
}

func TestBodySizeAndTruncatedBody(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, "123456789") }))
	defer s.Close()
	h := testHTTP(t, config.Provider{Name: "limit", URL: s.URL}, 8)
	if r := h.Run(context.Background()); r.Success || r.Error != "body_too_large" {
		t.Fatalf("oversized body accepted: %+v", r)
	}
	s2 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, _, _ := w.(http.Hijacker).Hijack()
		defer conn.Close()
		fmt.Fprint(conn, "HTTP/1.1 200 OK\r\nContent-Length: 10\r\n\r\nx")
	}))
	defer s2.Close()
	h = testHTTP(t, config.Provider{Name: "truncated", URL: s2.URL}, 0)
	if r := h.Run(context.Background()); r.Success || r.Error != "body" {
		t.Fatalf("truncated body accepted: %+v", r)
	}
}

func TestRedirectPolicyAndCredentials(t *testing.T) {
	var destinationCalled bool
	dest := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { destinationCalled = true }))
	defer dest.Close()
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/same" {
			http.Redirect(w, r, "/healthy", 302)
		} else if r.URL.Path == "/cross" {
			http.Redirect(w, r, dest.URL, 302)
		} else {
			w.WriteHeader(200)
		}
	}))
	defer s.Close()
	h := testHTTP(t, config.Provider{Name: "no-follow", URL: s.URL + "/same", ExpectedStatusCodes: []int{302}}, 0)
	if r := h.Run(context.Background()); !r.Success || r.StatusCode != 302 {
		t.Fatalf("redirect unexpectedly followed: %+v", r)
	}
	h = testHTTP(t, config.Provider{Name: "same", URL: s.URL + "/same", FollowRedirects: true}, 0)
	if r := h.Run(context.Background()); !r.Success || r.StatusCode != 200 {
		t.Fatalf("same-origin redirect failed: %+v", r)
	}
	h = testHTTP(t, config.Provider{Name: "cross", URL: s.URL + "/cross", FollowRedirects: true, Headers: map[string]string{"Authorization": "secret"}}, 0)
	if r := h.Run(context.Background()); r.Success || destinationCalled {
		t.Fatal("cross-origin redirect sent credentials")
	}
}

func TestTLSVerificationAndCustomCA(t *testing.T) {
	s := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200) }))
	defer s.Close()
	h := testHTTP(t, config.Provider{Name: "verify", URL: s.URL}, 0)
	if r := h.Run(context.Background()); r.Success || r.Error != "tls" {
		t.Fatalf("untrusted certificate accepted: %+v", r)
	}
	ca := filepath.Join(t.TempDir(), "ca.pem")
	os.WriteFile(ca, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: s.Certificate().Raw}), 0600)
	h = testHTTP(t, config.Provider{Name: "custom-ca", URL: s.URL, TLS: config.TLS{CAFile: ca}}, 0)
	if r := h.Run(context.Background()); !r.Success {
		t.Fatalf("custom CA rejected: %+v", r)
	}
	h = testHTTP(t, config.Provider{Name: "insecure", URL: s.URL, TLS: config.TLS{InsecureSkipVerify: true}}, 0)
	if r := h.Run(context.Background()); !r.Success {
		t.Fatalf("explicit insecure opt-in failed: %+v", r)
	}
}

func TestMutualTLSCombinedPEM(t *testing.T) {
	clientPEM, pool := clientCertificate(t)
	path := filepath.Join(t.TempDir(), "combined.pem")
	if err := os.WriteFile(path, clientPEM, 0600); err != nil {
		t.Fatal(err)
	}
	s := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(401) }))
	s.TLS = &tls.Config{ClientAuth: tls.RequireAndVerifyClientCert, ClientCAs: pool, MinVersion: tls.VersionTLS12}
	s.StartTLS()
	defer s.Close()
	h := testHTTP(t, config.Provider{Name: "mtls", URL: s.URL, ExpectedStatusCodes: []int{401}, TLS: config.TLS{InsecureSkipVerify: true, CertFile: path, KeyFile: path}}, 0)
	if r := h.Run(context.Background()); !r.Success || r.StatusCode != 401 {
		t.Fatalf("mTLS combined PEM failed: %+v", r)
	}
	h = testHTTP(t, config.Provider{Name: "no-client-cert", URL: s.URL, TLS: config.TLS{InsecureSkipVerify: true}}, 0)
	if r := h.Run(context.Background()); r.Success {
		t.Fatal("mTLS unexpectedly succeeded without client certificate")
	}
	if _, err := NewHTTP(config.Provider{TLS: config.TLS{CertFile: "/missing/cert", KeyFile: "/missing/key"}}, 1024); err == nil {
		t.Fatal("missing certificate silently accepted")
	}
}

func clientCertificate(t *testing.T) ([]byte, *x509.CertPool) {
	t.Helper()
	caKey, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	ca := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "test CA"}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour), IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign}
	caDER, err := x509.CreateCertificate(rand.Reader, ca, ca, &caKey.PublicKey, caKey)
	if err != nil {
		t.Fatal(err)
	}
	pool := x509.NewCertPool()
	pool.AppendCertsFromPEM(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: caDER}))
	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	cert := &x509.Certificate{SerialNumber: big.NewInt(2), Subject: pkix.Name{CommonName: "test client"}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour), KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}}
	der, err := x509.CreateCertificate(rand.Reader, cert, ca, &key.PublicKey, caKey)
	if err != nil {
		t.Fatal(err)
	}
	keyDER, _ := x509.MarshalECPrivateKey(key)
	result := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	result = append(result, pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})...)
	return result, pool
}

func TestIPFamilyIsEnforced(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200) }))
	defer s.Close()
	h := testHTTP(t, config.Provider{Name: "ipv4", URL: s.URL, IPProtocol: "ip4"}, 0)
	if r := h.Run(context.Background()); !r.Success {
		t.Fatalf("IPv4 probe failed: %+v", r)
	}
	h = testHTTP(t, config.Provider{Name: "ipv6-to-ipv4", URL: s.URL, IPProtocol: "ip6"}, 0)
	if r := h.Run(context.Background()); r.Success {
		t.Fatal("IPv6 selection silently fell back to IPv4")
	}
	listener, err := net.Listen("tcp6", "[::1]:0")
	if err != nil {
		t.Skip("IPv6 loopback unavailable")
	}
	v6 := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200) }))
	v6.Listener.Close()
	v6.Listener = listener
	v6.Start()
	defer v6.Close()
	h = testHTTP(t, config.Provider{Name: "ipv6", URL: v6.URL, IPProtocol: "ip6"}, 0)
	if r := h.Run(context.Background()); !r.Success {
		t.Fatalf("IPv6 probe failed: %+v", r)
	}
}
