package domain

import (
	"strings"
)

// RouteSelection contains the recipient domain and ordered provider channel candidates.
type RouteSelection struct {
	RecipientDomain string
	Channels        []string
}

// RouteSelectionError is a stable route selection failure.
type RouteSelectionError struct {
	Code    ErrorCode
	Message string
}

// Error returns the stable error code as an error string.
func (e RouteSelectionError) Error() string {
	return string(e.Code)
}

// SelectRoute chooses the ordered provider channel candidates for one message.
func SelectRoute(scene Scene, normalizedToEmail string, maxAttemptsPerMessage int) (RouteSelection, error) {
	recipientDomain, ok := RecipientDomain(normalizedToEmail)
	if !ok {
		return RouteSelection{}, routeSelectionError("recipient domain is invalid")
	}

	channels, ok := scene.RoutePolicy[recipientDomain]
	if !ok {
		channels, ok = scene.RoutePolicy[RoutePolicyWildcard]
	}
	if !ok {
		return RouteSelection{}, routeSelectionError("route not found")
	}

	candidates := uniqueAndTruncateChannels(channels, maxAttemptsPerMessage)
	if len(candidates) == 0 {
		return RouteSelection{}, routeSelectionError("route not found")
	}

	return RouteSelection{
		RecipientDomain: recipientDomain,
		Channels:        candidates,
	}, nil
}

// RecipientDomain extracts the lowercase domain from a normalized email address.
func RecipientDomain(normalizedToEmail string) (string, bool) {
	trimmed := strings.TrimSpace(normalizedToEmail)
	parts := strings.Split(trimmed, "@")
	if len(parts) != 2 || parts[1] == "" {
		return "", false
	}

	return strings.ToLower(parts[1]), true
}

func uniqueAndTruncateChannels(channels []string, maxAttemptsPerMessage int) []string {
	if maxAttemptsPerMessage <= 0 {
		return nil
	}

	seen := make(map[string]struct{}, len(channels))
	result := make([]string, 0, minInt(len(channels), maxAttemptsPerMessage))
	for _, channel := range channels {
		if channel == "" {
			continue
		}
		if _, exists := seen[channel]; exists {
			continue
		}
		seen[channel] = struct{}{}
		result = append(result, channel)
		if len(result) == maxAttemptsPerMessage {
			break
		}
	}

	return result
}

func routeSelectionError(message string) RouteSelectionError {
	return RouteSelectionError{Code: ErrorCodeRouteNotFound, Message: message}
}

func minInt(left int, right int) int {
	if left < right {
		return left
	}

	return right
}
