// Package config loads and validates the exporter configuration.
package config

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/textproto"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"go.yaml.in/yaml/v3"
)

// Duration accepts Go duration strings such as 500ms or 10s in YAML.
type Duration time.Duration

func (d *Duration) UnmarshalYAML(n *yaml.Node) error {
	if n.Kind != yaml.ScalarNode || n.Tag != "!!str" {
		return errors.New("duration must be a string with a unit, e.g. 10s")
	}
	v, err := time.ParseDuration(n.Value)
	if err != nil {
		return errors.New("invalid duration; use a value such as 500ms or 10s")
	}
	if v <= 0 {
		return errors.New("duration must be positive")
	}
	*d = Duration(v)
	return nil
}

type Config struct {
	Global    Global     `yaml:"global"`
	Providers []Provider `yaml:"providers"`
}

type Global struct {
	Timeout              Duration `yaml:"timeout"`
	MaxConcurrency       int      `yaml:"max_concurrency"`
	MaxResponseBodyBytes int64    `yaml:"max_response_body_bytes"`
}

type Provider struct {
	Name                string            `yaml:"name"`
	URL                 string            `yaml:"url"`
	Method              string            `yaml:"method"`
	Headers             map[string]string `yaml:"headers"`
	HeadersFromEnv      map[string]string `yaml:"headers_from_env"`
	Body                string            `yaml:"body"`
	Timeout             Duration          `yaml:"timeout"`
	ExpectedStatusCodes []int             `yaml:"expected_status_codes"`
	IPProtocol          string            `yaml:"ip_protocol"`
	FollowRedirects     bool              `yaml:"follow_redirects"`
	TLS                 TLS               `yaml:"tls"`
	MTR                 MTR               `yaml:"mtr"`
}

type TLS struct {
	InsecureSkipVerify bool   `yaml:"insecure_skip_verify"`
	CAFile             string `yaml:"ca_file"`
	CertFile           string `yaml:"cert_file"`
	KeyFile            string `yaml:"key_file"`
	ServerName         string `yaml:"server_name"`
}

type MTR struct {
	Enabled  bool     `yaml:"enabled"`
	Protocol string   `yaml:"protocol"`
	Cycles   int      `yaml:"cycles"`
	Timeout  Duration `yaml:"timeout"`
}

// Load rejects unknown fields, multiple documents and ambiguous or unsafe values.
// TLS paths are relative to the configuration file, not the working directory.
func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config: %w", err)
	}
	if len(data) > 1024*1024 {
		return nil, errors.New("config exceeds 1 MiB")
	}
	var c Config
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	if err := dec.Decode(&c); err != nil {
		// YAML type errors can include values containing secrets.
		return nil, errors.New("invalid YAML or unknown configuration field; check field names and types")
	}
	var extra any
	if err := dec.Decode(&extra); err != io.EOF {
		return nil, errors.New("config must contain exactly one YAML document")
	}
	if err := c.Validate(filepath.Dir(path)); err != nil {
		return nil, err
	}
	return &c, nil
}

func (c *Config) Validate(baseDir string) error {
	if c.Global.Timeout == 0 {
		c.Global.Timeout = Duration(10 * time.Second)
	}
	if c.Global.MaxConcurrency == 0 {
		c.Global.MaxConcurrency = 16
	}
	if c.Global.MaxResponseBodyBytes == 0 {
		c.Global.MaxResponseBodyBytes = 1024 * 1024
	}
	if time.Duration(c.Global.Timeout) <= 0 || time.Duration(c.Global.Timeout) > 2*time.Minute {
		return errors.New("global.timeout must be positive and at most 2m")
	}
	if c.Global.MaxConcurrency < 1 || c.Global.MaxConcurrency > 256 {
		return errors.New("global.max_concurrency must be between 1 and 256")
	}
	if c.Global.MaxResponseBodyBytes < 1 || c.Global.MaxResponseBodyBytes > 64*1024*1024 {
		return errors.New("global.max_response_body_bytes must be between 1 and 67108864")
	}
	if len(c.Providers) == 0 || len(c.Providers) > 256 {
		return errors.New("providers must contain between 1 and 256 entries")
	}
	names := make(map[string]bool)
	for i := range c.Providers {
		p := &c.Providers[i]
		fail := func(msg string) error { return fmt.Errorf("providers[%d]: %s", i, msg) }
		if p.Name == "" || len(p.Name) > 128 || strings.TrimSpace(p.Name) != p.Name || strings.ContainsAny(p.Name, "\r\n\t") {
			return fail("name must be nonempty, unique, at most 128 bytes and contain no surrounding whitespace or control characters")
		}
		if names[p.Name] {
			return fail("duplicate provider name")
		}
		names[p.Name] = true
		u, err := url.Parse(p.URL)
		if err != nil || u.Hostname() == "" || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil || u.Fragment != "" {
			return fail("url must be an absolute HTTP(S) URL without userinfo or fragment")
		}
		if _, err := http.NewRequest(http.MethodGet, p.URL, nil); err != nil {
			return fail("url cannot be used as an HTTP request URL")
		}
		if p.Method == "" {
			p.Method = http.MethodGet
		}
		if !validToken(p.Method) {
			return fail("method must be a valid HTTP token")
		}
		if p.Timeout == 0 {
			p.Timeout = c.Global.Timeout
		}
		if time.Duration(p.Timeout) <= 0 || time.Duration(p.Timeout) > 2*time.Minute {
			return fail("timeout must be positive and at most 2m")
		}
		if p.ExpectedStatusCodes == nil {
			p.ExpectedStatusCodes = []int{200}
		}
		if len(p.ExpectedStatusCodes) == 0 {
			return fail("expected_status_codes must not be empty")
		}
		codes := make(map[int]bool)
		for _, code := range p.ExpectedStatusCodes {
			if code < 100 || code > 599 || codes[code] {
				return fail("expected_status_codes must contain unique HTTP codes from 100 to 599")
			}
			codes[code] = true
		}
		if p.IPProtocol == "" {
			p.IPProtocol = "ip4"
		}
		if p.IPProtocol != "ip4" && p.IPProtocol != "ip6" && p.IPProtocol != "auto" {
			return fail("ip_protocol must be ip4, ip6 or auto")
		}
		headers := make(map[string]string)
		addHeader := func(k, v string) error {
			if !validToken(k) || strings.ContainsAny(v, "\r\n\x00") {
				return fail("invalid HTTP header name or value")
			}
			k = textproto.CanonicalMIMEHeaderKey(k)
			if _, exists := headers[k]; exists {
				return fail("duplicate HTTP header (header names are case insensitive)")
			}
			headers[k] = v
			return nil
		}
		for k, v := range p.Headers {
			if err := addHeader(k, v); err != nil {
				return err
			}
		}
		for k, env := range p.HeadersFromEnv {
			v, ok := os.LookupEnv(env)
			if !ok || v == "" {
				return fail("a headers_from_env variable is missing or empty")
			}
			if err := addHeader(k, v); err != nil {
				return err
			}
		}
		p.Headers = headers
		if (p.TLS.CertFile == "") != (p.TLS.KeyFile == "") {
			return fail("tls.cert_file and tls.key_file must be specified together (same path is allowed for combined PEM)")
		}
		for _, path := range []*string{&p.TLS.CAFile, &p.TLS.CertFile, &p.TLS.KeyFile} {
			if *path != "" && !filepath.IsAbs(*path) {
				*path = filepath.Join(baseDir, *path)
			}
		}
		if p.MTR.Protocol == "" {
			p.MTR.Protocol = "icmp"
		}
		if p.MTR.Protocol != "icmp" && p.MTR.Protocol != "tcp" {
			return fail("mtr.protocol must be icmp or tcp")
		}
		if p.MTR.Cycles == 0 {
			p.MTR.Cycles = 3
		}
		if p.MTR.Cycles < 1 || p.MTR.Cycles > 20 {
			return fail("mtr.cycles must be between 1 and 20")
		}
		if p.MTR.Timeout == 0 {
			p.MTR.Timeout = Duration(15 * time.Second)
		}
		if time.Duration(p.MTR.Timeout) <= 0 || time.Duration(p.MTR.Timeout) > time.Minute {
			return fail("mtr.timeout must be positive and at most 1m")
		}
	}
	return nil
}

func validToken(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if !((r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || strings.ContainsRune("!#$%&'*+-.^_`|~", r)) {
			return false
		}
	}
	return true
}
