package probe

import (
	"context"
	"errors"
	"net/netip"
	"strings"
	"testing"
	"time"

	"paystar/payment-exporter/internal/config"
)

func TestMTRUsesDestinationAndAverage(t *testing.T) {
	data := []byte(`{"report":{"hubs":[{"host":"192.0.2.1","Loss%":0,"Snt":3,"Avg":1},{"host":"192.0.2.2","Loss%":33.3,"Snt":3,"Last":99,"Avg":20},{"host":"???","Loss%":100,"Snt":3,"Avg":0}]}}`)
	r, err := parseMTR(data, netip.MustParseAddr("192.0.2.2"))
	if err != nil || !r.Success || r.LatencySeconds != 0.02 || r.LossRatio < 0.332 || r.LossRatio > 0.334 {
		t.Fatalf("wrong MTR destination or column: %+v %v", r, err)
	}
}

func TestMTRRejectsMissingDestinationOrStatistics(t *testing.T) {
	for _, data := range []string{
		`{"report":{"hubs":[{"host":"???","Loss%":100,"Snt":3,"Avg":0}]}}`,
		`{"report":{"hubs":[{"host":"192.0.2.2","Loss%":0,"Snt":3}]}}`,
		`{"report":{"hubs":[{"host":"192.0.2.2","Loss%":0,"Snt":0,"Avg":0}]}}`,
		`{"report":{"hubs":[{"host":"192.0.2.2","Loss%":101,"Snt":3,"Avg":2}]}}`,
		`not JSON`,
	} {
		if _, err := parseMTR([]byte(data), netip.MustParseAddr("192.0.2.2")); err == nil {
			t.Fatalf("invalid MTR result accepted: %s", data)
		}
	}
}

func TestMTRTCPArgumentsAndTimeout(t *testing.T) {
	p := config.Provider{URL: "https://127.0.0.1:8443/token", IPProtocol: "ip4", MTR: config.MTR{Protocol: "tcp", Cycles: 3, Timeout: config.Duration(20 * time.Millisecond)}}
	r := runMTR(context.Background(), p, func(ctx context.Context, name string, args ...string) ([]byte, error) {
		joined := strings.Join(args, " ")
		if name != "mtr" || !strings.Contains(joined, "--tcp --port 8443") || !strings.Contains(joined, "-4") || !strings.HasSuffix(joined, "127.0.0.1") {
			t.Fatalf("wrong command: %s %s", name, joined)
		}
		return []byte(`{"report":{"hubs":[{"host":"127.0.0.1","Loss%":0,"Snt":3,"Avg":1}]}}`), nil
	})
	if !r.Success {
		t.Fatal("structured MTR probe failed")
	}
	start := time.Now()
	r = runMTR(context.Background(), p, func(ctx context.Context, name string, args ...string) ([]byte, error) {
		<-ctx.Done()
		return nil, errors.New("timeout")
	})
	if r.Success || time.Since(start) > 200*time.Millisecond {
		t.Fatal("MTR timeout not respected")
	}
}
