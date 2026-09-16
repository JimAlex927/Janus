package health

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync/atomic"
	"testing"
	"time"
)

func TestStoreAppliesFailureAndRecoveryThresholds(t *testing.T) {
	store := NewStore(1)
	if !store.Healthy(0) {
		t.Fatal("target should start eligible")
	}
	if store.Record(0, false, 2, 2) || !store.Healthy(0) {
		t.Fatal("one failure should not remove target")
	}
	if !store.Record(0, false, 2, 2) || store.Healthy(0) {
		t.Fatal("second failure should remove target")
	}
	if store.Record(0, true, 2, 2) || store.Healthy(0) {
		t.Fatal("one recovery should not re-add target")
	}
	if !store.Record(0, true, 2, 2) || !store.Healthy(0) {
		t.Fatal("second recovery should re-add target")
	}
}

func TestCheckerMarksTargetUnhealthyAndRecovers(t *testing.T) {
	var healthy atomic.Bool
	healthy.Store(false)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/healthz" {
			t.Errorf("probe path = %q, want /healthz", r.URL.Path)
		}
		if healthy.Load() {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer server.Close()
	target, err := url.Parse(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	checker, err := New([]url.URL{*target}, Settings{
		Path:               "/healthz",
		Interval:           10 * time.Millisecond,
		Timeout:            100 * time.Millisecond,
		UnhealthyThreshold: 1,
		HealthyThreshold:   2,
	}, http.DefaultTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer checker.Close()

	waitFor(t, 2*time.Second, func() bool { return !checker.Store().Healthy(0) })
	healthy.Store(true)
	waitFor(t, 2*time.Second, func() bool { return checker.Store().Healthy(0) })
}

func TestCheckerCloseCancelsInFlightProbe(t *testing.T) {
	started := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(started)
		<-r.Context().Done()
	}))
	defer server.Close()
	target, err := url.Parse(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	checker, err := New([]url.URL{*target}, Settings{
		Path:               "/healthz",
		Interval:           time.Hour,
		Timeout:            time.Hour,
		UnhealthyThreshold: 1,
		HealthyThreshold:   1,
	}, http.DefaultTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("probe did not start")
	}

	closed := make(chan struct{})
	go func() {
		checker.Close()
		close(closed)
	}()
	select {
	case <-closed:
	case <-time.After(2 * time.Second):
		t.Fatal("checker close did not cancel probe")
	}
}

func waitFor(t *testing.T, timeout time.Duration, condition func() bool) {
	t.Helper()
	deadline := time.NewTimer(timeout)
	ticker := time.NewTicker(5 * time.Millisecond)
	defer deadline.Stop()
	defer ticker.Stop()
	for {
		if condition() {
			return
		}
		select {
		case <-deadline.C:
			t.Fatal("condition did not become true")
		case <-ticker.C:
		}
	}
}
