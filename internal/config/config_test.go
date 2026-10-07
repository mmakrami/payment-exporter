package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func loadYAML(t *testing.T, body string) (*Config, error) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.yml")
	if err := os.WriteFile(path, []byte(body), 0600); err != nil {
		t.Fatal(err)
	}
	return Load(path)
}

func TestConfigDefaultsAndExactCodes(t *testing.T) {
	c, err := loadYAML(t, "providers:\n  - name: test\n    url: https://example.com/\n")
	if err != nil {
		t.Fatal(err)
	}
	p := c.Providers[0]
	if p.Method != "GET" || p.IPProtocol != "ip4" || p.FollowRedirects || p.MTR.Enabled || time.Duration(p.Timeout) != 10*time.Second || len(p.ExpectedStatusCodes) != 1 || p.ExpectedStatusCodes[0] != 200 {
		t.Fatalf("unexpected defaults: %+v", p)
	}
}

func TestRejectInvalidConfiguration(t *testing.T) {
	for name, yaml := range map[string]string{
		"unknown field":       "providers:\n  - name: test\n    url: https://example.com\n    expected_status_code: [200]\n",
		"unknown global":      "global:\n  timout: 2s\nproviders:\n  - name: test\n    url: https://example.com\n",
		"empty":               "providers: []\n",
		"duplicate":           "providers:\n  - {name: test, url: https://example.com}\n  - {name: test, url: https://example.org}\n",
		"bad scheme":          "providers:\n  - {name: test, url: file:///etc/passwd}\n",
		"userinfo":            "providers:\n  - {name: test, url: 'https://secret:password@example.com'}\n",
		"invalid URL":         "providers:\n  - {name: test, url: 'https://example.com:bad'}\n",
		"status out of range": "providers:\n  - {name: test, url: https://example.com, expected_status_codes: [600]}\n",
		"duplicate status":    "providers:\n  - {name: test, url: https://example.com, expected_status_codes: [200, 200]}\n",
		"no status":           "providers:\n  - {name: test, url: https://example.com, expected_status_codes: []}\n",
		"negative timeout":    "global: {timeout: -1s}\nproviders:\n  - {name: test, url: https://example.com}\n",
		"zero timeout":        "global: {timeout: 0s}\nproviders:\n  - {name: test, url: https://example.com}\n",
		"unitless timeout":    "global: {timeout: 10}\nproviders:\n  - {name: test, url: https://example.com}\n",
		"missing key":         "providers:\n  - name: test\n    url: https://example.com\n    tls: {cert_file: cert.pem}\n",
		"bad IP":              "providers:\n  - {name: test, url: https://example.com, ip_protocol: ip5}\n",
		"bad mtr":             "providers:\n  - name: test\n    url: https://example.com\n    mtr: {protocol: udp}\n",
		"duplicate headers":   "providers:\n  - name: test\n    url: https://example.com\n    headers: {accept: a, Accept: b}\n",
		"multiple documents":  "providers:\n  - {name: test, url: https://example.com}\n---\nproviders: []\n",
		"duplicate YAML key":  "providers:\n  - {name: test, name: other, url: https://example.com}\n",
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := loadYAML(t, yaml); err == nil {
				t.Fatal("invalid configuration accepted")
			}
		})
	}
}

func TestEnvironmentHeadersAndRelativeTLSPaths(t *testing.T) {
	t.Setenv("TEST_PROVIDER_AUTH", "Bearer private-value")
	c, err := loadYAML(t, "providers:\n  - name: test\n    url: https://example.com\n    headers_from_env: {Authorization: TEST_PROVIDER_AUTH}\n    tls: {cert_file: secrets/combined.pem, key_file: secrets/combined.pem}\n")
	if err != nil {
		t.Fatal(err)
	}
	p := c.Providers[0]
	if p.Headers["Authorization"] != "Bearer private-value" || !filepath.IsAbs(p.TLS.CertFile) || p.TLS.CertFile != p.TLS.KeyFile {
		t.Fatal("environment header or combined PEM path incorrect")
	}
	_, err = loadYAML(t, "providers:\n  - name: test\n    url: https://example.com\n    headers_from_env: {Authorization: MISSING_PAYMENT_EXPORTER_TEST_VARIABLE}\n")
	if err == nil || strings.Contains(err.Error(), "private-value") {
		t.Fatal("missing env variable accepted or secret leaked")
	}
}
