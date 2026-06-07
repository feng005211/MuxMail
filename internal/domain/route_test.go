package domain

import (
	"errors"
	"reflect"
	"testing"
)

func TestSelectRouteExactMatchTakesPriority(t *testing.T) {
	scene := routeScene()

	selection, err := SelectRoute(scene, "user@gmail.com", 3)
	if err != nil {
		t.Fatalf("expected exact route: %v", err)
	}

	if selection.RecipientDomain != "gmail.com" {
		t.Fatalf("expected gmail.com domain, got %q", selection.RecipientDomain)
	}
	want := []string{"resend_auth_api", "resend_auth_smtp_backup"}
	if !reflect.DeepEqual(selection.Channels, want) {
		t.Fatalf("expected channels %v, got %v", want, selection.Channels)
	}
}

func TestSelectRouteFallbackMatch(t *testing.T) {
	scene := routeScene()

	selection, err := SelectRoute(scene, "user@example.com", 3)
	if err != nil {
		t.Fatalf("expected fallback route: %v", err)
	}

	want := []string{"brevo_auth_api", "resend_auth_api"}
	if !reflect.DeepEqual(selection.Channels, want) {
		t.Fatalf("expected channels %v, got %v", want, selection.Channels)
	}
}

func TestSelectRouteMissingRoute(t *testing.T) {
	scene := routeScene()
	delete(scene.RoutePolicy, RoutePolicyWildcard)

	_, err := SelectRoute(scene, "user@example.com", 3)
	assertRouteErrorCode(t, err, ErrorCodeRouteNotFound)
}

func TestSelectRouteTruncatesCandidates(t *testing.T) {
	scene := routeScene()
	scene.RoutePolicy["gmail.com"] = []string{"a", "b", "c", "d"}

	selection, err := SelectRoute(scene, "user@gmail.com", 3)
	if err != nil {
		t.Fatalf("expected route: %v", err)
	}

	want := []string{"a", "b", "c"}
	if !reflect.DeepEqual(selection.Channels, want) {
		t.Fatalf("expected truncated channels %v, got %v", want, selection.Channels)
	}
}

func TestSelectRouteDeduplicatesCandidates(t *testing.T) {
	scene := routeScene()
	scene.RoutePolicy["gmail.com"] = []string{"a", "a", "b", "a", "c"}

	selection, err := SelectRoute(scene, "user@gmail.com", 3)
	if err != nil {
		t.Fatalf("expected route: %v", err)
	}

	want := []string{"a", "b", "c"}
	if !reflect.DeepEqual(selection.Channels, want) {
		t.Fatalf("expected deduplicated channels %v, got %v", want, selection.Channels)
	}
}

func TestSelectRouteInvalidRecipientDomain(t *testing.T) {
	_, err := SelectRoute(routeScene(), "invalid", 3)
	assertRouteErrorCode(t, err, ErrorCodeRouteNotFound)
}

func TestRecipientDomainLowercasesDomain(t *testing.T) {
	domain, ok := RecipientDomain("user@Gmail.COM")
	if !ok {
		t.Fatal("expected recipient domain")
	}
	if domain != "gmail.com" {
		t.Fatalf("expected lowercase domain, got %q", domain)
	}
}

func routeScene() Scene {
	return Scene{
		Code: "register_code",
		RoutePolicy: RoutePolicy{
			"gmail.com": []string{"resend_auth_api", "resend_auth_smtp_backup"},
			"*":         []string{"brevo_auth_api", "resend_auth_api"},
		},
	}
}

func assertRouteErrorCode(t *testing.T, err error, code ErrorCode) {
	t.Helper()

	var routeErr RouteSelectionError
	if !errors.As(err, &routeErr) {
		t.Fatalf("expected route selection error, got %v", err)
	}
	if routeErr.Code != code {
		t.Fatalf("expected route error code %s, got %s", code, routeErr.Code)
	}
}
