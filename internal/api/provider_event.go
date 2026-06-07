package api

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/muxmail/muxmail/internal/domain"
)

type providerEventRequest struct {
	App                 string                   `json:"app"`
	MessageID           string                   `json:"message_id"`
	Provider            domain.Provider          `json:"provider"`
	ProviderAccountCode string                   `json:"provider_account"`
	ProviderChannelCode string                   `json:"provider_channel"`
	ProviderMessageID   string                   `json:"provider_message_id"`
	RecipientEmail      string                   `json:"recipient_email"`
	EventType           domain.ProviderEventType `json:"event_type"`
	EventPayload        string                   `json:"event_payload"`
	OccurredAt          string                   `json:"occurred_at"`
}

type providerEventResponse struct {
	MessageID string               `json:"message_id"`
	App       string               `json:"app"`
	Status    domain.MessageStatus `json:"status"`
}

func (r *Runtime) handleProviderEvent(w http.ResponseWriter, httpRequest *http.Request) {
	if httpRequest.Method == http.MethodGet {
		r.handleProviderEventList(w, httpRequest)
		return
	}

	requestID, err := domain.NewRequestID()
	if err != nil {
		writeAPIError(w, "", fmt.Errorf("generate request id: %w", err))
		return
	}
	if httpRequest.Method != http.MethodPost {
		http.NotFound(w, httpRequest)
		return
	}

	response, err := r.processProviderEvent(httpRequest)
	if err != nil {
		writeAPIError(w, requestID, err)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusAccepted)
	_ = json.NewEncoder(w).Encode(response)
}

func (r *Runtime) processProviderEvent(httpRequest *http.Request) (providerEventResponse, error) {
	if err := r.webhook.authenticate(httpRequest.Header.Get("Authorization")); err != nil {
		return providerEventResponse{}, err
	}
	if err := validateSendContentType(httpRequest.Header.Get("Content-Type")); err != nil {
		return providerEventResponse{}, err
	}

	body, err := io.ReadAll(io.LimitReader(httpRequest.Body, int64(r.defaults.MaxRequestBodyBytes)+1))
	if err != nil || len(body) > r.defaults.MaxRequestBodyBytes {
		return providerEventResponse{}, domain.RequestValidationError{Code: domain.ErrorCodeRequestTooLarge, Message: "request body is too large"}
	}
	event, err := decodeProviderEventRequest(body)
	if err != nil {
		return providerEventResponse{}, err
	}

	return r.applyProviderEvent(event)
}

func (r *Runtime) applyProviderEvent(event domain.ProviderEvent) (providerEventResponse, error) {
	snapshot, found, err := r.messageLog.FindLatestMessage(event.AppCode, event.MessageID)
	if err != nil {
		return providerEventResponse{}, fmt.Errorf("find message for provider event: %w", err)
	}
	if !found {
		return providerEventResponse{}, APIError{Code: domain.ErrorCodeMessageNotFound, Message: "message not found"}
	}
	duplicate, err := r.messageLog.HasProviderEvent(event)
	if err != nil {
		return providerEventResponse{}, fmt.Errorf("check duplicate provider event: %w", err)
	}
	if duplicate {
		return providerEventResponse{
			MessageID: event.MessageID,
			App:       event.AppCode,
			Status:    snapshot.Status,
		}, nil
	}

	if err := r.messageLog.AppendProviderEvent(event); err != nil {
		return providerEventResponse{}, fmt.Errorf("append provider event: %w", err)
	}
	if err := r.applySuppressionForEvent(event); err != nil {
		return providerEventResponse{}, fmt.Errorf("apply suppression for provider event: %w", err)
	}
	if err := r.messageLog.AppendWebhookMessageStatus(snapshot, event); err != nil {
		return providerEventResponse{}, fmt.Errorf("append provider event message status: %w", err)
	}

	return providerEventResponse{
		MessageID: event.MessageID,
		App:       event.AppCode,
		Status:    event.EventType.MessageStatus(),
	}, nil
}

func decodeProviderEventRequest(body []byte) (domain.ProviderEvent, error) {
	var request providerEventRequest
	decoder := json.NewDecoder(strings.NewReader(string(body)))
	if err := decoder.Decode(&request); err != nil {
		return domain.ProviderEvent{}, domain.RequestValidationError{Code: domain.ErrorCodeInvalidJSON, Message: "request body must be a JSON object"}
	}
	var trailing any
	if err := decoder.Decode(&trailing); err == nil {
		return domain.ProviderEvent{}, domain.RequestValidationError{Code: domain.ErrorCodeInvalidJSON, Message: "request body must contain a single JSON object"}
	} else if err != io.EOF {
		return domain.ProviderEvent{}, domain.RequestValidationError{Code: domain.ErrorCodeInvalidJSON, Message: "request body must contain a single JSON object"}
	}

	event := domain.ProviderEvent{
		MessageID:           strings.TrimSpace(request.MessageID),
		AppCode:             strings.TrimSpace(request.App),
		Provider:            request.Provider,
		ProviderAccountCode: strings.TrimSpace(request.ProviderAccountCode),
		ProviderChannelCode: strings.TrimSpace(request.ProviderChannelCode),
		ProviderMessageID:   strings.TrimSpace(request.ProviderMessageID),
		RecipientEmail:      strings.TrimSpace(request.RecipientEmail),
		EventType:           request.EventType,
		EventPayload:        request.EventPayload,
		OccurredAt:          strings.TrimSpace(request.OccurredAt),
	}
	if event.AppCode == "" || event.MessageID == "" {
		return domain.ProviderEvent{}, domain.RequestValidationError{Code: domain.ErrorCodeInvalidJSON, Message: "app and message_id are required"}
	}
	if !event.Provider.IsValid() {
		return domain.ProviderEvent{}, domain.RequestValidationError{Code: domain.ErrorCodeInvalidJSON, Message: "provider is invalid"}
	}
	if !event.EventType.IsValid() {
		return domain.ProviderEvent{}, domain.RequestValidationError{Code: domain.ErrorCodeInvalidJSON, Message: "event_type is invalid"}
	}
	if requiresSuppression(event.EventType) && domain.NormalizeEmail(event.RecipientEmail) == "" {
		return domain.ProviderEvent{}, domain.RequestValidationError{Code: domain.ErrorCodeInvalidJSON, Message: "recipient_email is required for bounce and complaint events"}
	}

	return event, nil
}

func (r *Runtime) applySuppressionForEvent(event domain.ProviderEvent) error {
	reason, ok := suppressionReasonForEvent(event.EventType)
	if !ok {
		return nil
	}

	_, err := r.suppressed.Add(domain.SuppressionEntry{
		AppCode: event.AppCode,
		Email:   event.RecipientEmail,
		Reason:  reason,
	})
	return err
}

func suppressionReasonForEvent(eventType domain.ProviderEventType) (domain.SuppressionReason, bool) {
	switch eventType {
	case domain.ProviderEventBounced:
		return domain.SuppressionReasonHardBounce, true
	case domain.ProviderEventComplained:
		return domain.SuppressionReasonComplaint, true
	default:
		return "", false
	}
}

func requiresSuppression(eventType domain.ProviderEventType) bool {
	_, ok := suppressionReasonForEvent(eventType)
	return ok
}
