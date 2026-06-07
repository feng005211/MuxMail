package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/muxmail/muxmail/internal/domain"
	"github.com/muxmail/muxmail/internal/lite"
)

const messageEventsPathSuffix = "/events"

type messageEventsResponse struct {
	MessageID string                      `json:"message_id"`
	App       string                      `json:"app"`
	Events    []messageEventResponseEntry `json:"events"`
}

type messageEventResponseEntry struct {
	LoggedAt          string                   `json:"logged_at"`
	Provider          domain.Provider          `json:"provider"`
	ProviderAccount   string                   `json:"provider_account"`
	ProviderChannel   string                   `json:"provider_channel"`
	ProviderMessageID string                   `json:"provider_message_id"`
	EventType         domain.ProviderEventType `json:"event_type"`
	OccurredAt        string                   `json:"occurred_at,omitempty"`
}

func (r *Runtime) handleMessageRoutes(w http.ResponseWriter, httpRequest *http.Request) {
	if strings.HasSuffix(httpRequest.URL.Path, messageEventsPathSuffix) {
		r.handleMessageEvents(w, httpRequest)
		return
	}
	if strings.HasSuffix(httpRequest.URL.Path, messageAttemptsPathSuffix) {
		r.handleMessageAttempts(w, httpRequest)
		return
	}

	r.handleMessageStatus(w, httpRequest)
}

func (r *Runtime) handleMessageEvents(w http.ResponseWriter, httpRequest *http.Request) {
	requestID, err := domain.NewRequestID()
	if err != nil {
		writeAPIError(w, "", fmt.Errorf("generate request id: %w", err))
		return
	}
	if httpRequest.Method != http.MethodGet {
		http.NotFound(w, httpRequest)
		return
	}

	response, err := r.processMessageEvents(httpRequest)
	if err != nil {
		writeAPIError(w, requestID, err)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(response)
}

func (r *Runtime) processMessageEvents(httpRequest *http.Request) (messageEventsResponse, error) {
	auth, err := r.auth.AuthenticateHeader(httpRequest.Header.Get("Authorization"))
	if err != nil {
		return messageEventsResponse{}, err
	}

	messageID, ok := messageIDFromEventsPath(httpRequest.URL.Path)
	if !ok {
		return messageEventsResponse{}, APIError{Code: domain.ErrorCodeMessageNotFound, Message: "message not found"}
	}

	snapshot, found, err := r.messageLog.FindLatestMessage(auth.App.Code, messageID)
	if err != nil {
		return messageEventsResponse{}, fmt.Errorf("find message for event query: %w", err)
	}
	if !found {
		return messageEventsResponse{}, APIError{Code: domain.ErrorCodeMessageNotFound, Message: "message not found"}
	}

	events, err := r.messageLog.ListProviderEvents(auth.App.Code, messageID)
	if err != nil {
		return messageEventsResponse{}, fmt.Errorf("list message provider events: %w", err)
	}

	return messageEventsFromSnapshots(snapshot, events), nil
}

func messageIDFromEventsPath(path string) (string, bool) {
	if !strings.HasSuffix(path, messageEventsPathSuffix) {
		return "", false
	}
	messagePath := strings.TrimSuffix(path, messageEventsPathSuffix)
	return messageIDFromPath(messagePath)
}

func messageEventsFromSnapshots(snapshot lite.MessageSnapshot, events []lite.ProviderEventSnapshot) messageEventsResponse {
	response := messageEventsResponse{
		MessageID: snapshot.MessageID,
		App:       snapshot.AppCode,
		Events:    make([]messageEventResponseEntry, 0, len(events)),
	}
	for _, event := range events {
		response.Events = append(response.Events, messageEventResponseEntry{
			LoggedAt:          event.Timestamp.UTC().Format(time.RFC3339Nano),
			Provider:          event.Provider,
			ProviderAccount:   event.ProviderAccountCode,
			ProviderChannel:   event.ProviderChannelCode,
			ProviderMessageID: event.ProviderMessageID,
			EventType:         event.EventType,
			OccurredAt:        event.OccurredAt,
		})
	}

	return response
}
