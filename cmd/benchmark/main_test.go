package main

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestParseTargets(t *testing.T) {
	targets, err := parseTargets("securegate=http://gateway.test/users/hello,nginx=https://nginx.test/users/hello")
	if err != nil || len(targets) != 2 || targets[1].Name != "nginx" {
		t.Fatalf("targets=%#v err=%v", targets, err)
	}
	for _, input := range []string{"", "securegate=ftp://gateway.test", "securegate=http://one,securegate=http://two"} {
		if _, err := parseTargets(input); err == nil {
			t.Errorf("accepted invalid targets %q", input)
		}
	}
}

func TestRunProducesSuccessfulMeasurements(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("ok"))
	}))
	defer server.Close()
	measurement := run(server.Client(), target{Name: "test", URL: server.URL}, 50*time.Millisecond, 2)
	finalize(&measurement)
	if measurement.Successes == 0 || measurement.Errors != 0 || measurement.Requests != measurement.Successes || measurement.P95Millis <= 0 {
		t.Fatalf("unexpected measurement: %+v", measurement)
	}
}

func TestPercentile(t *testing.T) {
	values := []time.Duration{time.Millisecond, 2 * time.Millisecond, 3 * time.Millisecond, 4 * time.Millisecond}
	if got := percentile(values, .95); got != 3*time.Millisecond {
		t.Fatalf("p95=%s", got)
	}
}
