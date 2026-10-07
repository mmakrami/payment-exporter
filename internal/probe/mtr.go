package probe

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"net"
	"net/netip"
	"net/url"
	"os/exec"
	"strconv"
	"time"

	"paystar/payment-exporter/internal/config"
)

type MTRResult struct {
	Success        bool
	LossRatio      float64
	LatencySeconds float64
}

type commandRunner func(context.Context, string, ...string) ([]byte, error)

// RunMTR resolves one explicit destination and matches that IP in structured
// JSON output. A terminal unknown hop is never mistaken for the destination.
func RunMTR(ctx context.Context, p config.Provider) MTRResult {
	return runMTR(ctx, p, func(ctx context.Context, name string, args ...string) ([]byte, error) {
		return exec.CommandContext(ctx, name, args...).Output()
	})
}

func runMTR(ctx context.Context, p config.Provider, run commandRunner) MTRResult {
	ctx, cancel := context.WithTimeout(ctx, time.Duration(p.MTR.Timeout))
	defer cancel()
	u, err := url.Parse(p.URL)
	if err != nil {
		return MTRResult{}
	}
	ip, err := resolveDestination(ctx, u.Hostname(), p.IPProtocol)
	if err != nil {
		return MTRResult{}
	}
	// --json selects report output itself. Adding --report after it switches
	// MTR back to the human-readable format and breaks structured parsing.
	args := []string{"--json", "--no-dns", "--report-cycles", strconv.Itoa(p.MTR.Cycles), "--order", "LSA"}
	if ip.Is4() {
		args = append(args, "-4")
	} else {
		args = append(args, "-6")
	}
	if p.MTR.Protocol == "tcp" {
		port := u.Port()
		if port == "" {
			port = "80"
			if u.Scheme == "https" {
				port = "443"
			}
		}
		args = append(args, "--tcp", "--port", port)
	}
	args = append(args, ip.String())
	out, err := run(ctx, "mtr", args...)
	if err != nil {
		return MTRResult{}
	}
	result, err := parseMTR(out, ip)
	if err != nil {
		return MTRResult{}
	}
	return result
}

func resolveDestination(ctx context.Context, host, protocol string) (netip.Addr, error) {
	network := "ip"
	if protocol == "ip4" || protocol == "ip6" {
		network = protocol
	}
	addrs, err := net.DefaultResolver.LookupNetIP(ctx, network, host)
	if err != nil || len(addrs) == 0 {
		return netip.Addr{}, errors.New("MTR destination resolution failed")
	}
	return addrs[0].Unmap(), nil
}

func parseMTR(data []byte, destination netip.Addr) (MTRResult, error) {
	var report struct {
		Report struct {
			Hubs []struct {
				Host string   `json:"host"`
				Loss *float64 `json:"Loss%"`
				Avg  *float64 `json:"Avg"`
				Sent *int     `json:"Snt"`
			} `json:"hubs"`
		} `json:"report"`
	}
	if err := json.Unmarshal(data, &report); err != nil {
		return MTRResult{}, errors.New("invalid MTR JSON")
	}
	for _, hop := range report.Report.Hubs {
		addr, err := netip.ParseAddr(hop.Host)
		if err != nil || addr.Unmap() != destination.Unmap() {
			continue
		}
		if hop.Loss == nil || hop.Avg == nil || hop.Sent == nil || *hop.Sent < 1 || *hop.Loss < 0 || *hop.Loss > 100 || *hop.Avg < 0 || math.IsNaN(*hop.Avg) || math.IsInf(*hop.Avg, 0) {
			return MTRResult{}, errors.New("invalid MTR destination statistics")
		}
		return MTRResult{Success: true, LossRatio: *hop.Loss / 100, LatencySeconds: *hop.Avg / 1000}, nil
	}
	return MTRResult{}, errors.New("MTR destination not reached")
}
