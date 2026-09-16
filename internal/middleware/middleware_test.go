package middleware

import (
	"context"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
)

func TestChainOrderAndEmptyChain(t *testing.T) {
	var events []string
	final := http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		events = append(events, "final")
	})
	mark := func(name string) Middleware {
		return func(next http.Handler) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				events = append(events, name+"-enter")
				next.ServeHTTP(w, r)
				events = append(events, name+"-exit")
			})
		}
	}

	Chain(final).ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/", nil))
	if got, want := events, []string{"final"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("empty chain events = %v, want %v", got, want)
	}

	events = nil
	Chain(final, mark("a"), mark("b")).ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/", nil))
	want := []string{"a-enter", "b-enter", "final", "b-exit", "a-exit"}
	if !reflect.DeepEqual(events, want) {
		t.Fatalf("chain events = %v, want %v", events, want)
	}
}

func TestChainSkipsNilMiddleware(t *testing.T) {
	called := false
	final := http.HandlerFunc(func(http.ResponseWriter, *http.Request) { called = true })
	Chain(final, nil).ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/", nil))
	if !called {
		t.Fatal("final handler was not called")
	}
}

func TestChainPreservesParentCancellation(t *testing.T) {
	cancelled := false
	final := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-r.Context().Done():
			cancelled = true
		default:
		}
	})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	r := httptest.NewRequest("GET", "/", nil).WithContext(ctx)
	Chain(final).ServeHTTP(httptest.NewRecorder(), r)
	if !cancelled {
		t.Fatal("parent cancellation was not visible to the final handler")
	}
}
