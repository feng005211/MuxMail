package provider

import (
	"context"
	"fmt"
	"sync"
)

// FakeProvider is a scripted provider adapter for deterministic worker tests.
type FakeProvider struct {
	mu       sync.Mutex
	results  []SendResult
	requests []SendRequest
}

// NewFakeProvider creates a fake provider that returns scripted results in order.
func NewFakeProvider(results ...SendResult) *FakeProvider {
	return &FakeProvider{results: append([]SendResult(nil), results...)}
}

// Send records the request and returns the next scripted result.
func (p *FakeProvider) Send(ctx context.Context, request SendRequest) (SendResult, error) {
	if err := ctx.Err(); err != nil {
		return SendResult{}, err
	}

	p.mu.Lock()
	defer p.mu.Unlock()

	p.requests = append(p.requests, request)
	if len(p.results) == 0 {
		return SendResult{}, fmt.Errorf("fake provider has no scripted result")
	}

	result := p.results[0]
	copy(p.results, p.results[1:])
	p.results[len(p.results)-1] = SendResult{}
	p.results = p.results[:len(p.results)-1]

	return result, nil
}

// Requests returns all requests recorded by the fake provider.
func (p *FakeProvider) Requests() []SendRequest {
	p.mu.Lock()
	defer p.mu.Unlock()

	return append([]SendRequest(nil), p.requests...)
}
