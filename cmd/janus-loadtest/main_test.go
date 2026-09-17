package main

import (
	"context"
	"net/http"
	"net/url"
	"testing"
	"time"
)

func TestValidateFlagsRejectsCredentialsAndInvalidBounds(t *testing.T) {
	tests := []struct {
		name string
		raw  string
	}{
		{name: "credentials", raw: "https://user:pass@example.test/api"},
		{name: "scheme", raw: "ftp://example.test/api"},
		{name: "missing", raw: "example.test/api"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := validateFlags(test.raw, time.Second, 1, 0, time.Second, 0, 1); err == nil {
				t.Fatal("expected validation error")
			}
		})
	}
	if _, err := validateFlags("http://example.test", time.Second, 0, 0, time.Second, 0, 1); err == nil {
		t.Fatal("expected concurrency validation error")
	}
	if _, err := validateFlags("http://example.test", time.Second, 1, 0, time.Second, maxPayloadBytes+1, 1); err == nil {
		t.Fatal("expected payload validation error")
	}
}

func TestParseHeadersAndRedactTarget(t *testing.T) {
	headers, err := parseHeaders([]string{"Accept: application/json", "X-Test: value:with:colons"})
	if err != nil {
		t.Fatal(err)
	}
	if got := headers.Get("Accept"); got != "application/json" {
		t.Fatalf("Accept = %q", got)
	}
	if got := headers.Get("X-Test"); got != "value:with:colons" {
		t.Fatalf("X-Test = %q", got)
	}
	if _, err := parseHeaders([]string{"missing separator"}); err == nil {
		t.Fatal("expected malformed header error")
	}
	target, err := url.Parse("https://example.test:8443/api?q=secret")
	if err != nil {
		t.Fatal(err)
	}
	if got := redactedTarget(target); got != "https://example.test:8443/api" {
		t.Fatalf("redacted target = %q", got)
	}
}

func TestWorkerStatsPercentilesAreBoundedByBuckets(t *testing.T) {
	stats := newWorkerStats()
	stats.record(http.StatusOK, 10, 2*time.Millisecond, nil, false)
	stats.record(http.StatusOK, 20, 20*time.Millisecond, nil, false)
	stats.record(http.StatusBadGateway, 0, 200*time.Millisecond, nil, false)
	if stats.completed != 3 || stats.bytes != 30 {
		t.Fatalf("stats = %#v", stats)
	}
	if got := percentile(stats, 0.50); got != 25*time.Millisecond {
		t.Fatalf("p50 = %s, want bucket 25ms", got)
	}
	if got := percentile(stats, 0.99); got != 250*time.Millisecond {
		t.Fatalf("p99 = %s, want bucket 250ms", got)
	}
	if got := meanMilliseconds(stats.total, stats.completed); got != 74.0 {
		t.Fatalf("mean latency = %vms, want 74ms", got)
	}
	stats.record(0, 0, time.Millisecond, context.Canceled, true)
	if stats.cancelled != 1 || stats.errors != 0 {
		t.Fatalf("end-of-window cancellation classification = %#v", stats)
	}
}
