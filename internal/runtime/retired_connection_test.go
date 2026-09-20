package runtime

import (
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"go.uber.org/zap"
	"janus/internal/config"
	"janus/internal/middleware"
)

func TestRealConnectionsRetiredLimitAndInvalidCandidate(t *testing.T) {
	release := make(chan struct{})
	started := make(chan struct{}, 1)
	var requests sync.WaitGroup
	var generations []*testGeneration
	builder := func(c config.Config, _ http.RoundTripper, _ *zap.Logger, _ map[string]*middleware.Limiter) (Generation, error) {
		g := &testGeneration{closed: make(chan struct{}), handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/slow" {
				started <- struct{}{}
				select {
				case <-release:
				case <-r.Context().Done():
					return
				}
			}
			io.WriteString(w, c.Routes[0].Name)
		})}
		generations = append(generations, g)
		if c.Routes[0].Name == "invalid" {
			return g, errors.New("candidate failed")
		}
		return g, nil
	}
	r, err := NewWithBuilder(validRuntimeConfig("initial"), zap.NewNop(), builder)
	if err != nil {
		t.Fatal(err)
	}
	s := httptest.NewServer(r.Handler())
	client := s.Client()
	client.Timeout = 10 * time.Second
	defer func() {
		close(release)
		requests.Wait()
		client.CloseIdleConnections()
		s.Close()
		r.Close()
		for _, g := range generations {
			select {
			case <-g.closed:
			default:
				t.Error("generation leaked")
			}
		}
	}()
	probe := func(want string) {
		resp, err := client.Get(s.URL + "/probe")
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if string(body) != want {
			t.Fatalf("probe=%q want=%s", body, want)
		}
	}
	if err := r.Replace(validRuntimeConfig("invalid")); err == nil {
		t.Fatal("invalid candidate accepted")
	}
	select {
	case <-generations[1].closed:
	default:
		t.Fatal("failed candidate not closed")
	}
	probe("initial")
	for range maxRetiredGenerations {
		requests.Add(1)
		go func() {
			defer requests.Done()
			resp, err := client.Get(s.URL + "/slow")
			if err != nil {
				t.Error(err)
				return
			}
			io.Copy(io.Discard, resp.Body)
			resp.Body.Close()
		}()
		select {
		case <-started:
		case <-time.After(time.Second):
			t.Fatal("request did not start")
		}
		if err := r.Replace(validRuntimeConfig("replacement")); err != nil {
			t.Fatal(err)
		}
	}
	if err := r.Replace(validRuntimeConfig("overflow")); !errors.Is(err, ErrRetiredLimit) {
		t.Fatalf("error=%v", err)
	}
	probe("replacement")
}
