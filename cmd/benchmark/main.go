// Command benchmark runs repeatable HTTP proxy comparisons and writes JSON
// results without requiring a third-party load-generation binary.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strings"
	"sync"
	"time"
)

const maxSamples = 100_000

type target struct{ Name, URL string }

type result struct {
	Name            string  `json:"name"`
	URL             string  `json:"url"`
	Requests        int64   `json:"requests"`
	Successes       int64   `json:"successes"`
	Errors          int64   `json:"errors"`
	Bytes           int64   `json:"bytes"`
	DurationSeconds float64 `json:"duration_seconds"`
	RequestsPerSec  float64 `json:"requests_per_second"`
	P50Millis       float64 `json:"p50_millis"`
	P95Millis       float64 `json:"p95_millis"`
	P99Millis       float64 `json:"p99_millis"`
	latencies       []time.Duration
}

type report struct {
	StartedAt     time.Time `json:"started_at"`
	Concurrency   int       `json:"concurrency"`
	Rounds        int       `json:"rounds"`
	WarmupSeconds float64   `json:"warmup_seconds"`
	DurationSecs  float64   `json:"duration_seconds_per_target_per_round"`
	Results       []result  `json:"results"`
}

func main() {
	var targetsRaw, output string
	var duration, warmup, readyTimeout time.Duration
	var concurrency, rounds int
	flag.StringVar(&targetsRaw, "targets", "", "comma-separated name=url targets")
	flag.DurationVar(&duration, "duration", 15*time.Second, "measurement duration for each target in each round")
	flag.DurationVar(&warmup, "warmup", 3*time.Second, "warmup duration for each target")
	flag.DurationVar(&readyTimeout, "ready-timeout", 30*time.Second, "maximum startup wait for each target")
	flag.IntVar(&concurrency, "concurrency", 64, "parallel requests per target")
	flag.IntVar(&rounds, "rounds", 3, "measurement rounds; target order alternates each round")
	flag.StringVar(&output, "output", "", "optional JSON report path")
	flag.Parse()

	targets, err := parseTargets(targetsRaw)
	if err != nil || duration <= 0 || warmup < 0 || readyTimeout <= 0 || concurrency <= 0 || rounds <= 0 {
		if err == nil {
			err = errors.New("duration, ready-timeout, concurrency, and rounds must be positive; warmup cannot be negative")
		}
		fmt.Fprintln(os.Stderr, "benchmark:", err)
		os.Exit(2)
	}
	client := &http.Client{Transport: &http.Transport{
		MaxConnsPerHost:     concurrency,
		MaxIdleConnsPerHost: concurrency,
		MaxIdleConns:        concurrency * len(targets),
		IdleConnTimeout:     30 * time.Second,
	}}
	for _, target := range targets {
		if err := waitReady(client, target.URL, readyTimeout); err != nil {
			fmt.Fprintf(os.Stderr, "benchmark: %s did not become ready: %v\n", target.Name, err)
			os.Exit(1)
		}
		if warmup > 0 {
			_ = run(client, target, warmup, concurrency)
		}
	}

	aggregate := make(map[string]result, len(targets))
	for round := 0; round < rounds; round++ {
		order := targets
		if round%2 == 1 {
			order = reverse(targets)
		}
		for _, target := range order {
			measurement := run(client, target, duration, concurrency)
			measurement.Name, measurement.URL = target.Name, target.URL
			item := aggregate[target.Name]
			merge(&item, measurement)
			aggregate[target.Name] = item
		}
	}
	report := report{StartedAt: time.Now().UTC(), Concurrency: concurrency, Rounds: rounds, WarmupSeconds: warmup.Seconds(), DurationSecs: duration.Seconds()}
	for _, target := range targets {
		item := aggregate[target.Name]
		item.Name, item.URL = target.Name, target.URL
		finalize(&item)
		report.Results = append(report.Results, item)
	}
	if err := writeReport(report, output); err != nil {
		fmt.Fprintln(os.Stderr, "benchmark:", err)
		os.Exit(1)
	}
	for _, item := range report.Results {
		fmt.Printf("%-12s %9.0f req/s  p50 %7.2f ms  p95 %7.2f ms  p99 %7.2f ms  errors %d/%d\n", item.Name, item.RequestsPerSec, item.P50Millis, item.P95Millis, item.P99Millis, item.Errors, item.Requests)
	}
}

func parseTargets(raw string) ([]target, error) {
	if strings.TrimSpace(raw) == "" {
		return nil, errors.New("-targets is required")
	}
	seen := map[string]struct{}{}
	var targets []target
	for _, item := range strings.Split(raw, ",") {
		name, address, ok := strings.Cut(strings.TrimSpace(item), "=")
		parsed, err := url.Parse(address)
		if !ok || name == "" || err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
			return nil, fmt.Errorf("invalid target %q; use name=http(s)://host/path", item)
		}
		if _, exists := seen[name]; exists {
			return nil, fmt.Errorf("duplicate target name %q", name)
		}
		seen[name] = struct{}{}
		targets = append(targets, target{Name: name, URL: address})
	}
	return targets, nil
}

func waitReady(client *http.Client, address string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		response, err := client.Get(address)
		if err == nil {
			_, _ = io.Copy(io.Discard, response.Body)
			response.Body.Close()
			if response.StatusCode >= 200 && response.StatusCode < 400 {
				return nil
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
	return errors.New("timeout")
}

func run(client *http.Client, target target, duration time.Duration, concurrency int) result {
	ctx, cancel := context.WithTimeout(context.Background(), duration)
	defer cancel()
	workers := make([]result, concurrency)
	var wg sync.WaitGroup
	for worker := range workers {
		wg.Add(1)
		go func(worker int) {
			defer wg.Done()
			for ctx.Err() == nil {
				started := time.Now()
				req, err := http.NewRequestWithContext(ctx, http.MethodGet, target.URL, nil)
				if err != nil {
					return
				}
				response, err := client.Do(req)
				if ctx.Err() != nil {
					return
				}
				workers[worker].Requests++
				if err != nil {
					workers[worker].Errors++
					continue
				}
				bytes, copyErr := io.Copy(io.Discard, response.Body)
				response.Body.Close()
				if response.StatusCode < 200 || response.StatusCode >= 400 || copyErr != nil {
					workers[worker].Errors++
					continue
				}
				workers[worker].Successes++
				workers[worker].Bytes += bytes
				if len(workers[worker].latencies) < maxSamples/concurrency+1 {
					workers[worker].latencies = append(workers[worker].latencies, time.Since(started))
				}
			}
		}(worker)
	}
	wg.Wait()
	var combined result
	combined.DurationSeconds = duration.Seconds()
	for _, worker := range workers {
		merge(&combined, worker)
	}
	return combined
}

func merge(dst *result, src result) {
	dst.Requests += src.Requests
	dst.Successes += src.Successes
	dst.Errors += src.Errors
	dst.Bytes += src.Bytes
	dst.DurationSeconds += src.DurationSeconds
	remaining := maxSamples - len(dst.latencies)
	if remaining > 0 {
		if len(src.latencies) > remaining {
			src.latencies = src.latencies[:remaining]
		}
		dst.latencies = append(dst.latencies, src.latencies...)
	}
}

func finalize(item *result) {
	if item.DurationSeconds > 0 {
		item.RequestsPerSec = float64(item.Successes) / item.DurationSeconds
	}
	sort.Slice(item.latencies, func(i, j int) bool { return item.latencies[i] < item.latencies[j] })
	item.P50Millis = percentile(item.latencies, 0.50).Seconds() * 1000
	item.P95Millis = percentile(item.latencies, 0.95).Seconds() * 1000
	item.P99Millis = percentile(item.latencies, 0.99).Seconds() * 1000
}

func percentile(values []time.Duration, percentile float64) time.Duration {
	if len(values) == 0 {
		return 0
	}
	index := int(float64(len(values)-1) * percentile)
	return values[index]
}

func reverse(items []target) []target {
	reversed := append([]target(nil), items...)
	for i, j := 0, len(reversed)-1; i < j; i, j = i+1, j-1 {
		reversed[i], reversed[j] = reversed[j], reversed[i]
	}
	return reversed
}

func writeReport(report report, output string) error {
	encoded, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return err
	}
	if output == "" {
		return nil
	}
	return os.WriteFile(output, append(encoded, '\n'), 0644)
}
