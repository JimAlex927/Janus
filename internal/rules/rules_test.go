package rules

import (
	"net/http"
	"net/url"
	"testing"
)

func TestCompileAndMatchBooleanExpression(t *testing.T) {
	m, err := DefaultRegistry().Compile("Host(`api.example.com`) && (PathPrefix(`/v1`) || Path(`/healthz`)) && (Method(`GET`) || Method(`HEAD`))")
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name string
		facts Facts
		want  bool
	}{
		{name: "prefix", facts: Facts{Host: "api.example.com", Path: "/v1/users", Method: http.MethodGet}, want: true},
		{name: "exact path", facts: Facts{Host: "api.example.com", Path: "/healthz", Method: http.MethodHead}, want: true},
		{name: "wrong method", facts: Facts{Host: "api.example.com", Path: "/v1", Method: http.MethodPost}, want: false},
		{name: "wrong host", facts: Facts{Host: "other.example.com", Path: "/v1", Method: http.MethodGet}, want: false},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := m.Match(&test.facts); got != test.want {
				t.Fatalf("Match() = %v, want %v", got, test.want)
			}
		})
	}
}

func TestIndexHintsOnlyUseSafeConjunctions(t *testing.T) {
	registry := DefaultRegistry()
	for _, test := range []struct {
		expression string
		want       IndexHints
	}{
		{"Host(`api.example.com`) && PathPrefix(`/api`) && Method(`GET`)", IndexHints{Safe: true, Host: "api.example.com", PathPrefix: "/api"}},
		{"Host(`api.example.com`) || Host(`admin.example.com`)", IndexHints{}},
		{"Host(`api.example.com`) && (PathPrefix(`/v1`) || PathPrefix(`/v2`))", IndexHints{}},
	} {
		m, err := registry.Compile(test.expression)
		if err != nil {
			t.Fatal(err)
		}
		if got := m.IndexHints(); got != test.want {
			t.Fatalf("IndexHints() = %#v, want %#v", got, test.want)
		}
	}
}

func TestCustomRuleRegistration(t *testing.T) {
	registry := DefaultRegistry()
	if err := registry.Register("SomeRule", func(args []string) (Predicate, error) {
		if len(args) != 1 {
			t.Fatalf("custom compiler args = %#v", args)
		}
		return customValuePredicate(args[0]), nil
	}); err != nil {
		t.Fatal(err)
	}
	m, err := registry.Compile("SomeRule(`123`) && Method(`GET`)")
	if err != nil {
		t.Fatal(err)
	}
	facts := Facts{Method: http.MethodGet, Query: url.Values{"value": {"123"}}}
	if !m.Match(&facts) {
		t.Fatal("custom rule did not match")
	}
}

type customValuePredicate string

func (p customValuePredicate) Match(facts *Facts) bool {
	return facts.Query.Get("value") == string(p)
}
