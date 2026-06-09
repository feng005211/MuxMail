package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/muxmail/muxmail/internal/domain"
	"github.com/muxmail/muxmail/internal/lite"
)

const (
	defaultProviderEventListLimit = 50
	maxProviderEventListLimit     = 200
)

type providerEventListResponse struct {
	App    string                   `json:"app"`
	Limit  int                      `json:"limit"`
	Events []providerEventListEntry `json:"events"`
}

type providerEventListEntry struct {
	MessageID         string                   `json:"message_id"`
	LoggedAt          string                   `json:"logged_at"`
	Provider          domain.Provider          `json:"provider"`
	ProviderAccount   string                   `json:"provider_account"`
	ProviderChannel   string                   `json:"provider_channel"`
	ProviderMessageID string                   `json:"provider_message_id"`
	EventType         domain.ProviderEventType `json:"event_type"`
	OccurredAt        string                   `json:"occurred_at"`
}

func (r *Runtime) handleProviderEventList(w http.ResponseWriter, httpRequest *http.Request) {
	requestID, err := domain.NewRequestID()
	if err != nil {
		writeAPIError(w, "", fmt.Errorf("generate request id: %w", err))
		return
	}
	if httpRequest.Method != http.MethodGet {
		http.NotFound(w, httpRequest)
		return
	}

	response, err := r.processProviderEventList(httpRequest)
	if err != nil {
		writeAPIError(w, requestID, err)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(response)
}

func (r *Runtime) processProviderEventList(httpRequest *http.Request) (providerEventListResponse, error) {
	auth, err := r.auth.AuthenticateHeader(httpRequest.Header.Get("Authorization"))
	if err != nil {
		return providerEventListResponse{}, err
	}

	filter, err := parseProviderEventListFilter(httpRequest)
	if err != nil {
		return providerEventListResponse{}, err
	}
	events, err := r.messageLog.ListRecentProviderEvents(auth.App.Code, filter)
	if err != nil {
		return providerEventListResponse{}, fmt.Errorf("list recent provider events: %w", err)
	}

	response := providerEventListResponse{
		App:    auth.App.Code,
		Limit:  filter.Limit,
		Events: make([]providerEventListEntry, 0, len(events)),
	}
	for _, event := range events {
		response.Events = append(response.Events, providerEventListEntry{
			MessageID:         event.MessageID,
			LoggedAt:          event.Timestamp.UTC().Format(time.RFC3339Nano),
			Provider:          event.Provider,
			ProviderAccount:   event.ProviderAccountCode,
			ProviderChannel:   event.ProviderChannelCode,
			ProviderMessageID: event.ProviderMessageID,
			EventType:         event.EventType,
			OccurredAt:        event.OccurredAt,
		})
	}

	return response, nil
}

func parseProviderEventListFilter(httpRequest *http.Request) (lite.ProviderEventListFilter, error) {
	query := httpRequest.URL.Query()
	limit := defaultProviderEventListLimit
	if rawLimit := strings.TrimSpace(query.Get("limit")); rawLimit != "" {
		parsedLimit, err := strconv.Atoi(rawLimit)
		if err != nil || parsedLimit <= 0 || parsedLimit > maxProviderEventListLimit {
			return lite.ProviderEventListFilter{}, domain.RequestValidationError{Code: domain.ErrorCodeInvalidQuery, Message: "limit must be between 1 and 200"}
		}
		limit = parsedLimit
	}

	var provider domain.Provider
	if rawProvider := strings.TrimSpace(query.Get("provider")); rawProvider != "" {
		provider = domain.Provider(rawProvider)
		if !provider.IsValid() {
			return lite.ProviderEventListFilter{}, domain.RequestValidationError{Code: domain.ErrorCodeInvalidQuery, Message: "provider is invalid"}
		}
	}

	var eventType domain.ProviderEventType
	if rawEventType := strings.TrimSpace(query.Get("event_type")); rawEventType != "" {
		eventType = domain.ProviderEventType(rawEventType)
		if !eventType.IsValid() {
			return lite.ProviderEventListFilter{}, domain.RequestValidationError{Code: domain.ErrorCodeInvalidQuery, Message: "event_type is invalid"}
		}
	}

	return lite.ProviderEventListFilter{
		Limit:     limit,
		Provider:  provider,
		EventType: eventType,
	}, nil
}
