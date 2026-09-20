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

func TestMetricsSummaryClassifiesResponseFamilies(t *testing.T) {
	m := NewMetrics()
	m.RecordRequest(Outcome{Status: 200}, "", time.Millisecond)
	m.RecordRequest(Outcome{Status: 404}, "route_not_found", time.Millisecond)
	m.RecordRequest(Outcome{Status: 400}, "http_error", time.Millisecond)
	m.RecordRequest(Outcome{Status: 502}, "upstream", time.Millisecond)
	m.RecordRequest(Outcome{Status: 503}, "admission_rejected", time.Millisecond)

	got := m.Summary()
	if got.Requests != 5 || got.Errors != 4 || got.FailedRequests != 4 {
		t.Fatalf("summary totals = %+v", got)
	}
	if got.ClientErrors != 1 || got.ServerErrors != 2 || got.NotFound != 1 {
		t.Fatalf("summary classifications = %+v", got)
	}
	output := string(m.Render(nil))
	for _, expected := range []string{
		"janus_request_failures_total 4",
		"janus_request_client_errors_total 1",
		"janus_request_server_errors_total 2",
		"janus_request_not_found_total 1",
	} {
		if !strings.Contains(output, expected) {
			t.Fatalf("metrics missing %q in:\n%s", expected, output)
		}
	}
}

func TestMetricsSummaryCountsEachRequestOnceAcrossAdmissionScopes(t *testing.T) {
	m := NewMetrics()
	// One request holds a permit at each of these nested admission gates.
	m.AddInFlight("global", "", 1)
	m.AddInFlight("route", "api", 1)
	m.AddInFlight("service", "orders", 1)
	if got := m.Summary().InFlight; got != 1 {
		t.Fatalf("in-flight requests = %d, want 1", got)
	}
	// Scoped metrics remain independently available to operators.
	output := string(m.Render(nil))
	for _, expected := range []string{
		`janus_in_flight_requests{scope="global",service=""} 1`,
		`janus_in_flight_requests{scope="route",service="api"} 1`,
		`janus_in_flight_requests{scope="service",service="orders"} 1`,
	} {
		if !strings.Contains(output, expected) {
			t.Fatalf("missing scope gauge: %s", expected)
		}
	}
	m.AddInFlight("service", "orders", -1)
	m.AddInFlight("route", "api", -1)
	m.AddInFlight("global", "", -1)
	if got := m.Summary().InFlight; got != 0 {
		t.Fatalf("after release = %d, want 0", got)
	}
}
