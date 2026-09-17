package telemetry

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	maxMetricLabelBytes = 128
	maxMetricSeries     = 4096
)

var metricBuckets = []float64{0.001, 0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10, 30}

// BackendHealth is a bounded, scrape-time view of one configured target. The
// target is an index, not a URL, so metrics never expose backend hostnames.
type BackendHealth struct {
	Service string
	Target  string
	Healthy bool
}

// Metrics is a process-owned registry for bounded Prometheus text output.
// Series keys are capped and labels are restricted to route/service/error
// identifiers; request paths, hosts and request IDs are never stored.
type Metrics struct {
	mu sync.Mutex

	requests   map[requestMetricKey]uint64
	errors     map[errorMetricKey]uint64
	durations  map[durationMetricKey]*durationMetricValue
	inFlight   map[inFlightMetricKey]int64
	rejections map[rejectionMetricKey]uint64
	reloads    map[string]uint64

	drainSeconds float64
	drainSet     bool
}

type requestMetricKey struct {
	route   string
	service string
	status  int
}

type errorMetricKey struct {
	route   string
	service string
	err     string
}

type durationMetricKey struct {
	route   string
	service string
}

type durationMetricValue struct {
	buckets []uint64
	count   uint64
	sum     float64
}

type inFlightMetricKey struct {
	scope   string
	service string
}

type rejectionMetricKey struct {
	scope   string
	service string
	reason  string
}

// NewMetrics creates an empty process registry.
// 进程级别的指标注册表，会记录请求总数、状态码等等
func NewMetrics() *Metrics {
	return &Metrics{
		requests:   make(map[requestMetricKey]uint64),
		errors:     make(map[errorMetricKey]uint64),
		durations:  make(map[durationMetricKey]*durationMetricValue),
		inFlight:   make(map[inFlightMetricKey]int64),
		rejections: make(map[rejectionMetricKey]uint64),
		reloads:    make(map[string]uint64),
	}
}

// RecordRequest records one completed observed request.
func (m *Metrics) RecordRequest(outcome Outcome, errorClass string, duration time.Duration) {
	if m == nil {
		return
	}
	route, service := boundedLabel(outcome.Route), boundedLabel(outcome.Service)
	status := outcome.Status
	if status == 0 {
		status = 200
	}
	m.mu.Lock()
	if !m.hasSeries(requestMetricKey{route: route, service: service, status: status}) {
		m.requests[requestMetricKey{route: route, service: service, status: status}]++
	}
	if errorClass != "" {
		key := errorMetricKey{route: route, service: service, err: boundedLabel(errorClass)}
		if !m.hasSeries(key) {
			m.errors[key]++
		}
	}
	durationKey := durationMetricKey{route: route, service: service}
	value := m.durations[durationKey]
	if value == nil && m.seriesCount() < maxMetricSeries {
		value = &durationMetricValue{buckets: make([]uint64, len(metricBuckets))}
		m.durations[durationKey] = value
	}
	if value != nil {
		seconds := duration.Seconds()
		value.count++
		value.sum += seconds
		for i, bound := range metricBuckets {
			if seconds <= bound {
				value.buckets[i]++
			}
		}
	}
	m.mu.Unlock()
}

// AddInFlight updates an in-flight gauge. Scope is a bounded fixed identifier
// such as global or service; service is empty for the global scope.
func (m *Metrics) AddInFlight(scope, service string, delta int64) {
	if m == nil || delta == 0 {
		return
	}
	key := inFlightMetricKey{scope: boundedLabel(scope), service: boundedLabel(service)}
	m.mu.Lock()
	if _, ok := m.inFlight[key]; ok || m.seriesCount() < maxMetricSeries {
		m.inFlight[key] += delta
		if m.inFlight[key] < 0 {
			m.inFlight[key] = 0
		}
	}
	m.mu.Unlock()
}

func (m *Metrics) RecordRejection(scope, service, reason string) {
	if m == nil {
		return
	}
	key := rejectionMetricKey{scope: boundedLabel(scope), service: boundedLabel(service), reason: boundedLabel(reason)}
	m.mu.Lock()
	if !m.hasSeries(key) {
		m.rejections[key]++
	}
	m.mu.Unlock()
}

func (m *Metrics) RecordReload(result string) {
	if m == nil {
		return
	}
	if result != "success" && result != "rejected" {
		result = "other"
	}
	m.mu.Lock()
	m.reloads[result]++
	m.mu.Unlock()
}

func (m *Metrics) RecordDrainDuration(duration time.Duration) {
	if m == nil {
		return
	}
	m.mu.Lock()
	m.drainSeconds = duration.Seconds()
	m.drainSet = true
	m.mu.Unlock()
}

// Render returns Prometheus text format from a scrape-time health snapshot.
func (m *Metrics) Render(health []BackendHealth) []byte {
	if m == nil {
		return nil
	}
	m.mu.Lock()
	requests := cloneRequests(m.requests)
	errors := cloneErrors(m.errors)
	durations := cloneDurations(m.durations)
	inFlight := cloneInFlight(m.inFlight)
	rejections := cloneRejections(m.rejections)
	reloads := cloneStrings(m.reloads)
	drainSeconds, drainSet := m.drainSeconds, m.drainSet
	m.mu.Unlock()

	var b strings.Builder
	b.WriteString("# HELP janus_requests_total Completed HTTP requests.\n# TYPE janus_requests_total counter\n")
	for _, key := range sortedRequestKeys(requests) {
		fmt.Fprintf(&b, "janus_requests_total{route=\"%s\",service=\"%s\",status=\"%d\"} %d\n", esc(key.route), esc(key.service), key.status, requests[key])
	}
	b.WriteString("# HELP janus_request_errors_total Requests with a classified error.\n# TYPE janus_request_errors_total counter\n")
	for _, key := range sortedErrorKeys(errors) {
		fmt.Fprintf(&b, "janus_request_errors_total{route=\"%s\",service=\"%s\",error=\"%s\"} %d\n", esc(key.route), esc(key.service), esc(key.err), errors[key])
	}
	b.WriteString("# HELP janus_request_duration_seconds Request duration histogram.\n# TYPE janus_request_duration_seconds histogram\n")
	for _, key := range sortedDurationKeys(durations) {
		value := durations[key]
		for i, bound := range metricBuckets {
			fmt.Fprintf(&b, "janus_request_duration_seconds_bucket{route=\"%s\",service=\"%s\",le=\"%s\"} %d\n", esc(key.route), esc(key.service), formatFloat(bound), value.buckets[i])
		}
		fmt.Fprintf(&b, "janus_request_duration_seconds_bucket{route=\"%s\",service=\"%s\",le=\"+Inf\"} %d\n", esc(key.route), esc(key.service), value.count)
		fmt.Fprintf(&b, "janus_request_duration_seconds_sum{route=\"%s\",service=\"%s\"} %s\n", esc(key.route), esc(key.service), formatFloat(value.sum))
		fmt.Fprintf(&b, "janus_request_duration_seconds_count{route=\"%s\",service=\"%s\"} %d\n", esc(key.route), esc(key.service), value.count)
	}
	b.WriteString("# HELP janus_in_flight_requests Current requests holding an admission permit.\n# TYPE janus_in_flight_requests gauge\n")
	for _, key := range sortedInFlightKeys(inFlight) {
		fmt.Fprintf(&b, "janus_in_flight_requests{scope=\"%s\",service=\"%s\"} %d\n", esc(key.scope), esc(key.service), inFlight[key])
	}
	b.WriteString("# HELP janus_request_rejections_total Requests rejected by admission.\n# TYPE janus_request_rejections_total counter\n")
	for _, key := range sortedRejectionKeys(rejections) {
		fmt.Fprintf(&b, "janus_request_rejections_total{scope=\"%s\",service=\"%s\",reason=\"%s\"} %d\n", esc(key.scope), esc(key.service), esc(key.reason), rejections[key])
	}
	b.WriteString("# HELP janus_backend_health Current active health state, 1 healthy and 0 unhealthy.\n# TYPE janus_backend_health gauge\n")
	for _, status := range sortedHealth(health) {
		fmt.Fprintf(&b, "janus_backend_health{service=\"%s\",target=\"%s\"} %d\n", esc(boundedLabel(status.Service)), esc(boundedLabel(status.Target)), boolInt(status.Healthy))
	}
	b.WriteString("# HELP janus_config_reload_total Configuration reload outcomes.\n# TYPE janus_config_reload_total counter\n")
	for _, result := range sortedStrings(reloads) {
		fmt.Fprintf(&b, "janus_config_reload_total{result=\"%s\"} %d\n", esc(result), reloads[result])
	}
	if drainSet {
		b.WriteString("# HELP janus_shutdown_drain_duration_seconds Duration of the most recent shutdown drain.\n# TYPE janus_shutdown_drain_duration_seconds gauge\n")
		fmt.Fprintf(&b, "janus_shutdown_drain_duration_seconds %s\n", formatFloat(drainSeconds))
	}
	return []byte(b.String())
}

func (m *Metrics) seriesCount() int {
	return len(m.requests) + len(m.errors) + len(m.durations) + len(m.inFlight) + len(m.rejections)
}

func (m *Metrics) hasSeries(key any) bool {
	switch value := key.(type) {
	case requestMetricKey:
		if _, ok := m.requests[value]; ok {
			return true
		}
	case errorMetricKey:
		if _, ok := m.errors[value]; ok {
			return true
		}
	case rejectionMetricKey:
		if _, ok := m.rejections[value]; ok {
			return true
		}
	}
	return m.seriesCount() >= maxMetricSeries
}

func boundedLabel(value string) string {
	if len(value) > maxMetricLabelBytes {
		return "overflow"
	}
	for _, r := range value {
		if r < 0x20 || r == 0x7f {
			return "overflow"
		}
	}
	return value
}

func esc(value string) string {
	return strings.NewReplacer("\\", "\\\\", "\"", "\\\"", "\n", "\\n", "\r", "\\r").Replace(value)
}

func formatFloat(value float64) string { return strconv.FormatFloat(value, 'g', -1, 64) }
func boolInt(value bool) int {
	if value {
		return 1
	}
	return 0
}

func cloneRequests(source map[requestMetricKey]uint64) map[requestMetricKey]uint64 {
	result := make(map[requestMetricKey]uint64, len(source))
	for k, v := range source {
		result[k] = v
	}
	return result
}
func cloneErrors(source map[errorMetricKey]uint64) map[errorMetricKey]uint64 {
	result := make(map[errorMetricKey]uint64, len(source))
	for k, v := range source {
		result[k] = v
	}
	return result
}
func cloneDurations(source map[durationMetricKey]*durationMetricValue) map[durationMetricKey]*durationMetricValue {
	result := make(map[durationMetricKey]*durationMetricValue, len(source))
	for k, v := range source {
		copyValue := *v
		copyValue.buckets = append([]uint64(nil), v.buckets...)
		result[k] = &copyValue
	}
	return result
}
func cloneInFlight(source map[inFlightMetricKey]int64) map[inFlightMetricKey]int64 {
	result := make(map[inFlightMetricKey]int64, len(source))
	for k, v := range source {
		result[k] = v
	}
	return result
}
func cloneRejections(source map[rejectionMetricKey]uint64) map[rejectionMetricKey]uint64 {
	result := make(map[rejectionMetricKey]uint64, len(source))
	for k, v := range source {
		result[k] = v
	}
	return result
}
func cloneStrings(source map[string]uint64) map[string]uint64 {
	result := make(map[string]uint64, len(source))
	for k, v := range source {
		result[k] = v
	}
	return result
}

func sortedRequestKeys(values map[requestMetricKey]uint64) []requestMetricKey {
	result := make([]requestMetricKey, 0, len(values))
	for k := range values {
		result = append(result, k)
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].route != result[j].route {
			return result[i].route < result[j].route
		}
		if result[i].service != result[j].service {
			return result[i].service < result[j].service
		}
		return result[i].status < result[j].status
	})
	return result
}
func sortedErrorKeys(values map[errorMetricKey]uint64) []errorMetricKey {
	result := make([]errorMetricKey, 0, len(values))
	for k := range values {
		result = append(result, k)
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].route != result[j].route {
			return result[i].route < result[j].route
		}
		if result[i].service != result[j].service {
			return result[i].service < result[j].service
		}
		return result[i].err < result[j].err
	})
	return result
}
func sortedDurationKeys(values map[durationMetricKey]*durationMetricValue) []durationMetricKey {
	result := make([]durationMetricKey, 0, len(values))
	for k := range values {
		result = append(result, k)
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].route != result[j].route {
			return result[i].route < result[j].route
		}
		return result[i].service < result[j].service
	})
	return result
}
func sortedInFlightKeys(values map[inFlightMetricKey]int64) []inFlightMetricKey {
	result := make([]inFlightMetricKey, 0, len(values))
	for k := range values {
		result = append(result, k)
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].scope != result[j].scope {
			return result[i].scope < result[j].scope
		}
		return result[i].service < result[j].service
	})
	return result
}
func sortedRejectionKeys(values map[rejectionMetricKey]uint64) []rejectionMetricKey {
	result := make([]rejectionMetricKey, 0, len(values))
	for k := range values {
		result = append(result, k)
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].scope != result[j].scope {
			return result[i].scope < result[j].scope
		}
		if result[i].service != result[j].service {
			return result[i].service < result[j].service
		}
		return result[i].reason < result[j].reason
	})
	return result
}
func sortedStrings(values map[string]uint64) []string {
	result := make([]string, 0, len(values))
	for k := range values {
		result = append(result, k)
	}
	sort.Strings(result)
	return result
}
func sortedHealth(values []BackendHealth) []BackendHealth {
	unique := make(map[string]BackendHealth, len(values))
	for _, value := range values {
		value.Service = boundedLabel(value.Service)
		value.Target = boundedLabel(value.Target)
		unique[value.Service+"\x00"+value.Target] = value
	}
	result := make([]BackendHealth, 0, len(unique))
	for _, value := range unique {
		result = append(result, value)
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].Service != result[j].Service {
			return result[i].Service < result[j].Service
		}
		return result[i].Target < result[j].Target
	})
	if len(result) > maxMetricSeries {
		result = result[:maxMetricSeries]
	}
	return result
}
