// Package telemetry contains request-local outcome state shared by the fixed
// observer and the route/proxy stages of a request.
package telemetry

import (
	"context"
	"sync"
	"time"
)

type contextKey struct{}

// Observation is safe for the small amount of state that may be updated by
// response callbacks while the access observer is waiting for the handler.
type Observation struct {
	mu sync.Mutex

	requestID string
	started   time.Time
	route     string
	service   string
	status    int
	committed bool
	requestN  int64
	responseN int64
	error     string
}

type Outcome struct {
	RequestID     string
	Started       time.Time
	Route         string
	Service       string
	Status        int
	Committed     bool
	RequestBytes  int64
	ResponseBytes int64
	ErrorClass    string
}

func New(requestID string, started time.Time) *Observation {
	return &Observation{requestID: requestID, started: started}
}

func WithObservation(ctx context.Context, observation *Observation) context.Context {
	return context.WithValue(ctx, contextKey{}, observation)
}

func FromContext(ctx context.Context) *Observation {
	if ctx == nil {
		return nil
	}
	observation, _ := ctx.Value(contextKey{}).(*Observation)
	return observation
}

func (o *Observation) SetRoute(route, service string) {
	if o == nil {
		return
	}
	o.mu.Lock()
	o.route, o.service = route, service
	o.mu.Unlock()
}

func (o *Observation) AddRequestBytes(n int) {
	if o == nil || n <= 0 {
		return
	}
	o.mu.Lock()
	o.requestN += int64(n)
	o.mu.Unlock()
}

func (o *Observation) RecordResponse(status, n int) {
	if o == nil {
		return
	}
	o.mu.Lock()
	if status >= 100 && status < 200 && status != 101 {
		o.mu.Unlock()
		return
	}
	if !o.committed {
		o.status = status
		o.committed = true
	}
	if n > 0 {
		o.responseN += int64(n)
	}
	o.mu.Unlock()
}

func (o *Observation) AddResponseBytes(n int) {
	if o == nil || n <= 0 {
		return
	}
	o.mu.Lock()
	o.responseN += int64(n)
	o.mu.Unlock()
}

func (o *Observation) MarkError(class string) {
	if o == nil || class == "" {
		return
	}
	o.mu.Lock()
	o.error = class
	o.mu.Unlock()
}

func (o *Observation) Outcome() Outcome {
	if o == nil {
		return Outcome{}
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	status := o.status
	if status == 0 {
		status = 200
	}
	return Outcome{
		RequestID:     o.requestID,
		Started:       o.started,
		Route:         o.route,
		Service:       o.service,
		Status:        status,
		Committed:     o.committed,
		RequestBytes:  o.requestN,
		ResponseBytes: o.responseN,
		ErrorClass:    o.error,
	}
}

func MarkError(ctx context.Context, class string) {
	if observation := FromContext(ctx); observation != nil {
		observation.MarkError(class)
	}
}
