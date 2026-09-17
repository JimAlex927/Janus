// Command janus-loadtest is a small, dependency-free workload generator for
// release qualification. It runs as a separate process so gateway CPU and
// memory measurements do not include a client library inside Janus.
package main

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

const (
	defaultDuration       = 30 * time.Second
	defaultRequestTimeout = 10 * time.Second
	defaultMaxBodyBytes   = 8 << 20
	maxConcurrency        = 100_000
	maxRate               = 1_000_000
	maxPayloadBytes       = 1 << 30
)

var latencyBounds = []time.Duration{
	1 * time.Millisecond,
	5 * time.Millisecond,
	10 * time.Millisecond,
	25 * time.Millisecond,
	50 * time.Millisecond,
	100 * time.Millisecond,
	250 * time.Millisecond,
	500 * time.Millisecond,
	1 * time.Second,
	2 * time.Second,
	5 * time.Second,
	10 * time.Second,
	30 * time.Second,
	60 * time.Second,
}

type headerFlags []string

func (h *headerFlags) String() string { return strings.Join(*h, ",") }

func (h *headerFlags) Set(value string) error {
	name, _, ok := strings.Cut(value, ":")
	if !ok || strings.TrimSpace(name) == "" {
		return fmt.Errorf("header must use Name:Value syntax")
	}
	*h = append(*h, value)
	return nil
}

type workerStats struct {
	started   uint64
	completed uint64
	success   uint64
	non2xx    uint64
	errors    uint64
	cancelled uint64
	bytes     uint64
	total     time.Duration
	maximum   time.Duration
	buckets   []uint64
}

func newWorkerStats() workerStats {
	return workerStats{buckets: make([]uint64, len(latencyBounds))}
}

func (s *workerStats) record(status int, bodyBytes int64, latency time.Duration, err error, cancelledAtEnd bool) {
	s.completed++
	if cancelledAtEnd {
		s.cancelled++
	} else if err != nil {
		s.errors++
	} else if status >= http.StatusOK && status < http.StatusMultipleChoices {
		s.success++
	} else {
		s.non2xx++
	}
	if bodyBytes > 0 {
		s.bytes += uint64(bodyBytes)
	}
	s.total += latency
	if latency > s.maximum {
		s.maximum = latency
	}
	for i, bound := range latencyBounds {
		if latency <= bound {
			s.buckets[i]++
			break
		}
	}
}

func (s *workerStats) merge(other workerStats) {
	s.started += other.started
	s.completed += other.completed
	s.success += other.success
	s.non2xx += other.non2xx
	s.errors += other.errors
	s.cancelled += other.cancelled
	s.bytes += other.bytes
	s.total += other.total
	if other.maximum > s.maximum {
		s.maximum = other.maximum
	}
	for i := range s.buckets {
		s.buckets[i] += other.buckets[i]
	}
}

type report struct {
	Target            string  `json:"target"`
	DurationSeconds   float64 `json:"duration_seconds"`
	Concurrency       int     `json:"concurrency"`
	Rate              float64 `json:"target_rate_per_second,omitempty"`
	Scheduled         uint64  `json:"scheduled"`
	Started           uint64  `json:"started"`
	Completed         uint64  `json:"completed"`
	Successful2xx     uint64  `json:"successful_2xx"`
	Non2xx            uint64  `json:"non_2xx"`
	Errors            uint64  `json:"errors"`
	CancelledAtEnd    uint64  `json:"cancelled_at_measurement_end"`
	Dropped           uint64  `json:"dropped_before_start"`
	ResponseBytes     uint64  `json:"response_bytes"`
	RequestsPerSecond float64 `json:"completed_per_second"`
	MeanMilliseconds  float64 `json:"mean_latency_ms"`
	P50Milliseconds   float64 `json:"p50_latency_ms"`
	P95Milliseconds   float64 `json:"p95_latency_ms"`
	P99Milliseconds   float64 `json:"p99_latency_ms"`
	MaxMilliseconds   float64 `json:"max_latency_ms"`
}

func main() {
	target := flag.String("url", "", "target HTTP URL")
	duration := flag.Duration("duration", defaultDuration, "measurement duration")
	concurrency := flag.Int("concurrency", 1, "number of request workers")
	rate := flag.Float64("rate", 0, "target request arrival rate per second; zero means open-loop fixed concurrency")
	timeout := flag.Duration("timeout", defaultRequestTimeout, "per-request timeout")
	method := flag.String("method", http.MethodGet, "HTTP method")
	bodySize := flag.Int("body-size", 0, "request body size in bytes")
	maxBody := flag.Int64("max-body-bytes", defaultMaxBodyBytes, "maximum response bytes read per request; zero is unlimited")
	insecure := flag.Bool("insecure", false, "skip TLS certificate verification for test environments")
	var headers headerFlags
	flag.Var(&headers, "header", "additional request header, repeatable as Name:Value")
	flag.Parse()

	parsed, err := validateFlags(*target, *duration, *concurrency, *rate, *timeout, *bodySize, *maxBody)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	requestHeaders, err := parseHeaders(headers)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}

	payload := bytes.Repeat([]byte{'x'}, *bodySize)
	transport := &http.Transport{
		ForceAttemptHTTP2:   true,
		MaxIdleConns:        *concurrency * 2,
		MaxIdleConnsPerHost: *concurrency,
		MaxConnsPerHost:     *concurrency,
		DisableCompression:  true,
		TLSClientConfig:     &tls.Config{InsecureSkipVerify: *insecure}, //nolint:gosec -- explicit qualification flag
		IdleConnTimeout:     90 * time.Second,
	}
	client := &http.Client{Transport: transport, Timeout: *timeout}
	defer transport.CloseIdleConnections()

	ctx, cancel := contextWithDuration(*duration)
	defer cancel()
	measurementStarted := time.Now()
	var scheduled atomic.Uint64
	var dropped atomic.Uint64
	results := make(chan workerStats, *concurrency)
	var wg sync.WaitGroup
	if *rate == 0 {
		for i := 0; i < *concurrency; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				results <- runWorker(ctx, client, parsed, *method, payload, requestHeaders, *maxBody, nil, &scheduled)
			}()
		}
	} else {
		jobs := make(chan struct{}, *concurrency)
		// Rate-controlled workers receive jobs below. The channel is bounded
		// so overload is visible as dropped work instead of unbounded memory use.
		for i := 0; i < *concurrency; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				results <- runWorker(ctx, client, parsed, *method, payload, requestHeaders, *maxBody, jobs, &scheduled)
			}()
		}
		go schedule(ctx, *rate, jobs, &scheduled, &dropped)
	}
	wg.Wait()
	close(results)

	total := newWorkerStats()
	for stats := range results {
		total.merge(stats)
	}
	output := makeReport(parsed, time.Since(measurementStarted), *concurrency, *rate, scheduled.Load(), dropped.Load(), total)
	encoder := json.NewEncoder(os.Stdout)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(output); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func contextWithDuration(duration time.Duration) (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), duration)
}

func validateFlags(rawTarget string, duration time.Duration, concurrency int, rate float64, timeout time.Duration, bodySize int, maxBody int64) (*url.URL, error) {
	if strings.TrimSpace(rawTarget) == "" {
		return nil, fmt.Errorf("-url is required")
	}
	target, err := url.Parse(rawTarget)
	if err != nil || target.Host == "" || (target.Scheme != "http" && target.Scheme != "https") {
		return nil, fmt.Errorf("-url must be an absolute http or https URL")
	}
	if target.User != nil {
		return nil, fmt.Errorf("-url must not contain credentials")
	}
	if duration <= 0 || timeout <= 0 {
		return nil, fmt.Errorf("-duration and -timeout must be positive")
	}
	if concurrency < 1 || concurrency > maxConcurrency {
		return nil, fmt.Errorf("-concurrency must be between 1 and %d", maxConcurrency)
	}
	if rate < 0 || rate > maxRate || math.IsNaN(rate) || math.IsInf(rate, 0) {
		return nil, fmt.Errorf("-rate must be between 0 and %d", maxRate)
	}
	if bodySize < 0 || bodySize > maxPayloadBytes || maxBody < 0 || maxBody > maxPayloadBytes {
		return nil, fmt.Errorf("-body-size and -max-body-bytes must be between 0 and %d", maxPayloadBytes)
	}
	return target, nil
}

func parseHeaders(values []string) (http.Header, error) {
	result := make(http.Header)
	for _, value := range values {
		name, content, ok := strings.Cut(value, ":")
		if !ok || strings.TrimSpace(name) == "" {
			return nil, fmt.Errorf("invalid header %q", value)
		}
		result.Add(strings.TrimSpace(name), strings.TrimSpace(content))
	}
	return result, nil
}

func schedule(ctx context.Context, rate float64, jobs chan<- struct{}, scheduled, dropped *atomic.Uint64) {
	interval := time.Duration(float64(time.Second) / rate)
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	defer close(jobs)
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if scheduled != nil {
				scheduled.Add(1)
			}
			select {
			case jobs <- struct{}{}:
			default:
				dropped.Add(1)
			}
		}
	}
}

func runWorker(ctx context.Context, client *http.Client, target *url.URL, method string, payload []byte, headers http.Header, maxBody int64, jobs <-chan struct{}, scheduled *atomic.Uint64) workerStats {
	stats := newWorkerStats()
	for {
		if jobs != nil {
			select {
			case <-ctx.Done():
				return stats
			case _, ok := <-jobs:
				if !ok {
					return stats
				}
			}
		} else {
			select {
			case <-ctx.Done():
				return stats
			default:
			}
			scheduled.Add(1)
		}
		stats.started++
		started := time.Now()
		request, err := http.NewRequestWithContext(ctx, method, target.String(), bytes.NewReader(payload))
		if err == nil {
			request.Header = headers.Clone()
			if len(payload) > 0 && request.Header.Get("Content-Type") == "" {
				request.Header.Set("Content-Type", "application/octet-stream")
			}
		}
		status := 0
		var responseBytes int64
		if err == nil {
			var response *http.Response
			response, err = client.Do(request)
			if response != nil {
				if response.Body != nil {
					if maxBody > 0 {
						responseBytes, err = io.Copy(io.Discard, io.LimitReader(response.Body, maxBody+1))
						if err == nil && responseBytes > maxBody {
							err = fmt.Errorf("response exceeded %d bytes", maxBody)
						}
					} else {
						responseBytes, err = io.Copy(io.Discard, response.Body)
					}
				}
				status = response.StatusCode
				if response.Body != nil {
					response.Body.Close()
				}
			}
		}
		stats.record(status, responseBytes, time.Since(started), err, err != nil && ctx.Err() != nil)
	}
}

func makeReport(target *url.URL, duration time.Duration, concurrency int, rate float64, scheduled, dropped uint64, stats workerStats) report {
	result := report{
		Target:           redactedTarget(target),
		DurationSeconds:  duration.Seconds(),
		Concurrency:      concurrency,
		Rate:             rate,
		Scheduled:        scheduled,
		Started:          stats.started,
		Completed:        stats.completed,
		Successful2xx:    stats.success,
		Non2xx:           stats.non2xx,
		Errors:           stats.errors,
		CancelledAtEnd:   stats.cancelled,
		Dropped:          dropped,
		ResponseBytes:    stats.bytes,
		MeanMilliseconds: meanMilliseconds(stats.total, stats.completed),
		P50Milliseconds:  durationMilliseconds(percentile(stats, 0.50)),
		P95Milliseconds:  durationMilliseconds(percentile(stats, 0.95)),
		P99Milliseconds:  durationMilliseconds(percentile(stats, 0.99)),
		MaxMilliseconds:  durationMilliseconds(stats.maximum),
	}
	if duration > 0 {
		result.RequestsPerSecond = float64(stats.completed) / duration.Seconds()
	}
	return result
}

func durationMilliseconds(value time.Duration) float64 {
	return float64(value) / float64(time.Millisecond)
}

func meanMilliseconds(total time.Duration, completed uint64) float64 {
	if completed == 0 {
		return 0
	}
	return durationMilliseconds(total) / float64(completed)
}

func percentile(stats workerStats, fraction float64) time.Duration {
	if stats.completed == 0 {
		return 0
	}
	rank := uint64(math.Ceil(float64(stats.completed) * fraction))
	if rank < 1 {
		rank = 1
	}
	var seen uint64
	for i, count := range stats.buckets {
		seen += count
		if seen >= rank {
			return latencyBounds[i]
		}
	}
	return latencyBounds[len(latencyBounds)-1] + time.Nanosecond
}

func redactedTarget(target *url.URL) string {
	path := target.EscapedPath()
	if path == "" {
		path = "/"
	}
	return target.Scheme + "://" + target.Host + path
}
