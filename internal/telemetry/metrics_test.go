package telemetry

import (
	"strings"
	"testing"
	"time"
)

func TestMetricsRenderOutcomeAndLifecycleSignals(t *testing.T) {
	m := NewMetrics()
	m.RecordRequest(Outcome{Route: "api", Service: "orders", Status: 502}, "upstream", 12*time.Millisecond)
	m.AddInFlight("global", "", 1)
	m.RecordRejection("service", "orders", "capacity")
	m.RecordReload("success")
	m.RecordReload("rejected")
	m.RecordDrainDuration(250 * time.Millisecond)

	output := string(m.Render([]BackendHealth{{Service: "orders", Target: "0", Healthy: false}}))
	for _, expected := range []string{
		`janus_requests_total{route="api",service="orders",status="502"} 1`,
		`janus_request_errors_total{route="api",service="orders",error="upstream"} 1`,
		`janus_request_duration_seconds_count{route="api",service="orders"} 1`,
		`janus_in_flight_requests{scope="global",service=""} 1`,
		`janus_request_rejections_total{scope="service",service="orders",reason="capacity"} 1`,
		`janus_backend_health{service="orders",target="0"} 0`,
		`janus_config_reload_total{result="success"} 1`,
		`janus_config_reload_total{result="rejected"} 1`,
		`janus_shutdown_drain_duration_seconds 0.25`,
	} {
		if !strings.Contains(output, expected) {
			t.Fatalf("metrics missing %q in:\n%s", expected, output)
		}
	}
	if strings.Contains(output, "request_id") || strings.Contains(output, "127.0.0.1") || strings.Contains(output, "/api/") {
		t.Fatalf("metrics exposed an unbounded/raw identity: %s", output)
	}
}

func TestMetricsBoundsLabelSeries(t *testing.T) {
	m := NewMetrics()
	for i := 0; i < maxMetricSeries+100; i++ {
		m.RecordRequest(Outcome{Route: string(rune(i)) + "-route", Service: "service", Status: 200}, "", time.Millisecond)
	}
	if got := len(m.requests) + len(m.durations); got > maxMetricSeries {
		t.Fatalf("metric series = %d, want <= %d", got, maxMetricSeries)
	}
}

func TestMetricsCountersAccumulateExistingSeries(t *testing.T) {
	m := NewMetrics()
	for i := 0; i < 3; i++ {
		m.RecordRequest(Outcome{Route: "api", Service: "orders", Status: 502}, "upstream", time.Millisecond)
		m.RecordRejection("service", "orders", "capacity")
	}

	output := string(m.Render(nil))
	for _, expected := range []string{
		`janus_requests_total{route="api",service="orders",status="502"} 3`,
		`janus_request_errors_total{route="api",service="orders",error="upstream"} 3`,
		`janus_request_rejections_total{scope="service",service="orders",reason="capacity"} 3`,
		`janus_request_duration_seconds_count{route="api",service="orders"} 3`,
	} {
		if !strings.Contains(output, expected) {
			t.Fatalf("metrics missing accumulated value %q in:\n%s", expected, output)
		}
	}
}
