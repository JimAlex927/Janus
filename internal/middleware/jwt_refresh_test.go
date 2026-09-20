package middleware

import (
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func refreshTestVerifier(t *testing.T) *jwtVerifier {
	t.Helper()
	v, err := newJWTVerifier(JWTOptions{JWKSURL: "https://issuer.example/keys", Algorithms: []string{"EdDSA"}, Issuer: "issuer", Audience: []string{"janus"}})
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func TestJWKSRefreshCoalescesAndWaitersCancel(t *testing.T) {
	v := refreshTestVerifier(t)
	started, release := make(chan struct{}), make(chan struct{})
	var releaseOnce sync.Once
	unblock := func() { releaseOnce.Do(func() { close(release) }) }
	t.Cleanup(unblock)
	var calls atomic.Int32
	v.client.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if calls.Add(1) == 1 {
			close(started)
		}
		if deadline, ok := r.Context().Deadline(); !ok || time.Until(deadline) > jwtRefreshTimeout {
			t.Error("shared refresh has no bounded context")
		}
		select {
		case <-release:
		case <-r.Context().Done():
		}
		return nil, errors.New("identity provider unavailable")
	})
	done := make(chan error, 1)
	go func() { done <- v.refresh(context.Background(), false) }()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("refresh did not start")
	}
	var waiters sync.WaitGroup
	for range 64 {
		waiters.Add(1)
		go func() {
			defer waiters.Done()
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
			defer cancel()
			if err := v.refresh(ctx, false); !errors.Is(err, context.DeadlineExceeded) {
				t.Errorf("waiter error = %v", err)
			}
		}()
	}
	waited := make(chan struct{})
	go func() { waiters.Wait(); close(waited) }()
	select {
	case <-waited:
	case <-time.After(time.Second):
		t.Fatal("waiters blocked on refresh lock")
	}
	if calls.Load() != 1 {
		t.Fatalf("concurrent fetches = %d", calls.Load())
	}
	unblock()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("empty cache accepted failure")
		}
	case <-time.After(time.Second):
		t.Fatal("refresh stuck")
	}
	for range 20 {
		if v.refresh(context.Background(), true) == nil {
			t.Fatal("cold outage accepted")
		}
	}
	if calls.Load() != 1 {
		t.Fatalf("failure backoff made %d calls", calls.Load())
	}
}

func TestJWKSRotationUnknownKIDAndStaleDeadline(t *testing.T) {
	v := refreshTestVerifier(t)
	var calls atomic.Int32
	var failing atomic.Bool
	var kid atomic.Value
	kid.Store("old")
	key := base64.RawURLEncoding.EncodeToString(make([]byte, ed25519.PublicKeySize))
	v.client.Transport = roundTripFunc(func(*http.Request) (*http.Response, error) {
		calls.Add(1)
		if failing.Load() {
			return nil, errors.New("offline")
		}
		body := fmt.Sprintf(`{"keys":[{"kty":"OKP","crv":"Ed25519","kid":%q,"alg":"EdDSA","x":%q}]}`, kid.Load().(string), key)
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body))}, nil
	})
	ctx := context.Background()
	if _, err := v.key(ctx, "old", "EdDSA"); err != nil {
		t.Fatal(err)
	}
	for range 20 {
		if _, err := v.key(ctx, "unknown", "EdDSA"); err == nil {
			t.Fatal("unknown kid accepted")
		}
	}
	if calls.Load() != 1 {
		t.Fatalf("unknown kids caused %d fetches", calls.Load())
	}
	kid.Store("new")
	v.cacheMu.Lock()
	v.fetchedAt = time.Now().Add(-time.Minute)
	v.cacheMu.Unlock()
	if _, err := v.key(ctx, "new", "EdDSA"); err != nil {
		t.Fatalf("rotation failed: %v", err)
	}
	if _, err := v.key(ctx, "old", "EdDSA"); err == nil {
		t.Fatal("removed key still accepted")
	}
	failing.Store(true)
	v.cacheMu.Lock()
	v.fetchedAt = time.Now().Add(-jwtKeyTTL)
	stale := v.staleUntil
	v.cacheMu.Unlock()
	if _, err := v.key(ctx, "new", "EdDSA"); err != nil {
		t.Fatalf("stale grace failed: %v", err)
	}
	for range 20 {
		if _, err := v.key(ctx, "new", "EdDSA"); err != nil {
			t.Fatal(err)
		}
	}
	if calls.Load() != 3 {
		t.Fatalf("stale outage fetches = %d", calls.Load())
	}
	v.cacheMu.Lock()
	if v.staleUntil != stale {
		t.Error("failure extended stale deadline")
	}
	v.staleUntil = time.Now().Add(-time.Second)
	v.cacheMu.Unlock()
	if _, err := v.key(ctx, "new", "EdDSA"); err == nil {
		t.Fatal("expired stale key accepted during backoff")
	}
	failing.Store(false)
	v.cacheMu.Lock()
	v.retryAfter = time.Time{}
	v.cacheMu.Unlock()
	if _, err := v.key(ctx, "new", "EdDSA"); err != nil {
		t.Fatalf("recovery failed: %v", err)
	}
}
