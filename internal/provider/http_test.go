package provider

import "net/http"

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return f(request)
}

func (f roundTripFunc) client() *http.Client {
	return &http.Client{Transport: f}
}
