// Package probe implements bounded HTTP and optional MTR probes.
package probe

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"strings"
	"time"

	"paystar/payment-exporter/internal/config"
)

type HTTPResult struct {
	Success    bool
	StatusCode int
	Duration   float64
	Error      string // bounded category; never contains URLs, headers or body
}

type HTTP struct {
	provider config.Provider
	client   *http.Client
	maxBody  int64
}

func NewHTTP(p config.Provider, maxBody int64) (*HTTP, error) {
	tlsConfig := &tls.Config{
		MinVersion: tls.VersionTLS12,
		// An explicit per-provider opt-in, required by some legacy services.
		InsecureSkipVerify: p.TLS.InsecureSkipVerify, //nolint:gosec
		ServerName:         p.TLS.ServerName,
	}
	if p.TLS.CAFile != "" {
		pem, err := os.ReadFile(p.TLS.CAFile)
		if err != nil {
			return nil, fmt.Errorf("read CA file: %w", err)
		}
		pool, err := x509.SystemCertPool()
		if err != nil || pool == nil {
			pool = x509.NewCertPool()
		}
		if !pool.AppendCertsFromPEM(pem) {
			return nil, errors.New("CA file contains no valid certificate")
		}
		tlsConfig.RootCAs = pool
	}
	if p.TLS.CertFile != "" {
		cert, err := tls.LoadX509KeyPair(p.TLS.CertFile, p.TLS.KeyFile)
		if err != nil {
			return nil, fmt.Errorf("load client certificate/key: %w", err)
		}
		tlsConfig.Certificates = []tls.Certificate{cert}
	}
	dialer := &net.Dialer{Timeout: time.Duration(p.Timeout), KeepAlive: 30 * time.Second}
	transport := &http.Transport{
		// Direct probes are intentional: proxy environment variables are ignored.
		DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
			if p.IPProtocol == "ip4" {
				network = "tcp4"
			} else if p.IPProtocol == "ip6" {
				network = "tcp6"
			}
			return dialer.DialContext(ctx, network, address)
		},
		TLSClientConfig:       tlsConfig,
		TLSHandshakeTimeout:   time.Duration(p.Timeout),
		ResponseHeaderTimeout: time.Duration(p.Timeout),
		ExpectContinueTimeout: time.Second,
		MaxIdleConns:          2,
		MaxIdleConnsPerHost:   2,
		MaxConnsPerHost:       2,
		IdleConnTimeout:       90 * time.Second,
		ForceAttemptHTTP2:     true,
	}
	client := &http.Client{Transport: transport, Timeout: time.Duration(p.Timeout)}
	client.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if !p.FollowRedirects {
			return http.ErrUseLastResponse
		}
		if len(via) >= 10 {
			return errors.New("too many redirects")
		}
		// Never forward credentials or an mTLS identity to another origin.
		// An explicit port difference is conservatively treated as a different origin.
		if req.URL.Scheme != via[0].URL.Scheme || !strings.EqualFold(req.URL.Host, via[0].URL.Host) {
			return errors.New("cross-origin redirect blocked")
		}
		return nil
	}
	return &HTTP{provider: p, client: client, maxBody: maxBody}, nil
}

func (h *HTTP) Run(ctx context.Context) (result HTTPResult) {
	start := time.Now()
	defer func() { result.Duration = time.Since(start).Seconds() }()
	ctx, cancel := context.WithTimeout(ctx, time.Duration(h.provider.Timeout))
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, h.provider.Method, h.provider.URL, strings.NewReader(h.provider.Body))
	if err != nil {
		result.Error = "request"
		return
	}
	req.Header.Set("User-Agent", "payment-exporter")
	for k, v := range h.provider.Headers {
		if strings.EqualFold(k, "Host") {
			req.Host = v
		} else {
			req.Header.Set(k, v)
		}
	}
	resp, err := h.client.Do(req)
	if err != nil {
		if resp != nil && resp.Body != nil {
			resp.Body.Close()
		}
		result.Error = classifyError(err)
		return
	}
	defer resp.Body.Close()
	result.StatusCode = resp.StatusCode
	// Include the complete body transfer in latency and fail on body read errors.
	n, err := io.Copy(io.Discard, io.LimitReader(resp.Body, h.maxBody+1))
	if err != nil {
		result.Error = classifyError(err)
		if result.Error == "transport" {
			result.Error = "body"
		}
		return
	}
	if n > h.maxBody {
		result.Error = "body_too_large"
		return
	}
	for _, code := range h.provider.ExpectedStatusCodes {
		if result.StatusCode == code {
			result.Success = true
			return
		}
	}
	result.Error = "status"
	return
}

func (h *HTTP) Close() { h.client.CloseIdleConnections() }

func classifyError(err error) string {
	var netErr net.Error
	if errors.Is(err, context.DeadlineExceeded) || (errors.As(err, &netErr) && netErr.Timeout()) {
		return "timeout"
	}
	if errors.Is(err, context.Canceled) {
		return "canceled"
	}
	var dnsErr *net.DNSError
	if errors.As(err, &dnsErr) {
		return "dns"
	}
	var certErr *tls.CertificateVerificationError
	if errors.As(err, &certErr) {
		return "tls"
	}
	return "transport"
}
