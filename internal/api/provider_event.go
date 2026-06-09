package api

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/muxmail/muxmail/internal/domain"
	"github.com/muxmail/muxmail/internal/lite"
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

const normalizedProviderEventPayload = `{"source":"generic"}`

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
	r.eventMu.Lock()
	defer r.eventMu.Unlock()

	snapshot, found, err := r.messageLog.FindLatestMessage(event.AppCode, event.MessageID)
	if err != nil {
		return providerEventResponse{}, fmt.Errorf("find message for provider event: %w", err)
	}
	if !found {
		return providerEventResponse{}, APIError{Code: domain.ErrorCodeMessageNotFound, Message: "message not found"}
	}
	if err := r.validateProviderEventAttempt(event); err != nil {
		return providerEventResponse{}, err
	}
	if err := validateProviderEventRecipientMatchesMessage(snapshot, event); err != nil {
		return providerEventResponse{}, err
	}
	appended, err := r.messageLog.AppendProviderEventOnce(event)
	if err != nil {
		return providerEventResponse{}, fmt.Errorf("append provider event: %w", err)
	}
	if !appended {
		if err := r.applySuppressionForEvent(event); err != nil {
			return providerEventResponse{}, fmt.Errorf("apply suppression for duplicate provider event: %w", err)
		}
		if nextStatus, ok := nextProviderEventMessageStatus(snapshot.Status, event.EventType); ok {
			if err := r.appendProviderEventMessageStatus(snapshot, nextStatus); err != nil {
				return providerEventResponse{}, fmt.Errorf("append duplicate provider event message status: %w", err)
			}
			snapshot.Status = nextStatus
		}
		return providerEventResponse{
			MessageID: event.MessageID,
			App:       event.AppCode,
			Status:    snapshot.Status,
		}, nil
	}

	r.recordProviderEventStat(snapshot, event)
	if err := r.applySuppressionForEvent(event); err != nil {
		return providerEventResponse{}, fmt.Errorf("apply suppression for provider event: %w", err)
	}
	if nextStatus, ok := nextProviderEventMessageStatus(snapshot.Status, event.EventType); ok {
		if err := r.appendProviderEventMessageStatus(snapshot, nextStatus); err != nil {
			return providerEventResponse{}, fmt.Errorf("append provider event message status: %w", err)
		}
		snapshot.Status = nextStatus
	}

	return providerEventResponse{
		MessageID: event.MessageID,
		App:       event.AppCode,
		Status:    snapshot.Status,
	}, nil
}

func (r *Runtime) recordProviderEventStat(snapshot lite.MessageSnapshot, event domain.ProviderEvent) {
	metric, ok := providerEventMetric(event.EventType)
	if !ok {
		return
	}

	_ = r.stats.Record(lite.StatsRecord{
		AppCode:   snapshot.AppCode,
		SceneCode: snapshot.SceneCode,
		Metric:    metric,
		Value:     1,
	})
}

func validateProviderEventRecipientMatchesMessage(snapshot lite.MessageSnapshot, event domain.ProviderEvent) error {
	if !requiresSuppression(event.EventType) {
		return nil
	}
	normalizedEmail, ok := domain.NormalizeAddrSpecEmail(event.RecipientEmail)
	if !ok {
		return domain.RequestValidationError{Code: domain.ErrorCodeInvalidJSON, Message: "recipient email is invalid"}
	}
	if snapshot.ToHash == "" || domain.ToHash(snapshot.AppCode, normalizedEmail) != snapshot.ToHash {
		return domain.RequestValidationError{Code: domain.ErrorCodeInvalidJSON, Message: "provider event recipient does not match message"}
	}

	return nil
}

func (r *Runtime) validateProviderEventAttempt(event domain.ProviderEvent) error {
	attempts, err := r.messageLog.ListAttempts(event.AppCode, event.MessageID)
	if err != nil {
		return fmt.Errorf("list attempts for provider event: %w", err)
	}
	for _, attempt := range latestAttemptsByNumber(attempts) {
		if attempt.Status != domain.AttemptStatusSent {
			continue
		}
		if attempt.Provider == event.Provider &&
			attempt.ProviderAccountCode == event.ProviderAccountCode &&
			attempt.ProviderChannelCode == event.ProviderChannelCode {
			if attempt.ProviderMessageID == event.ProviderMessageID {
				return nil
			}
			if attempt.ProviderMessageID == "" {
				// Some accepted API responses can omit the provider id; the authenticated webhook
				// still carries the MuxMail message tags and concrete provider channel identity.
				return nil
			}
		}
	}

	return domain.RequestValidationError{
		Code:    domain.ErrorCodeInvalidJSON,
		Message: "provider event does not match a sent attempt",
	}
}

func latestAttemptsByNumber(attempts []lite.AttemptSnapshot) map[int]lite.AttemptSnapshot {
	latest := make(map[int]lite.AttemptSnapshot)
	for _, attempt := range attempts {
		latest[attempt.AttemptNo] = attempt
	}

	return latest
}

func (r *Runtime) appendProviderEventMessageStatus(snapshot lite.MessageSnapshot, status domain.MessageStatus) error {
	event := domain.ProviderEvent{EventType: eventTypeForMessageStatus(status)}
	return r.messageLog.AppendWebhookMessageStatus(snapshot, event)
}

func nextProviderEventMessageStatus(current domain.MessageStatus, eventType domain.ProviderEventType) (domain.MessageStatus, bool) {
	next := eventType.MessageStatus()
	if next == "" || current == next {
		return "", false
	}
	switch current {
	case domain.MessageStatusFailed, domain.MessageStatusComplained:
		return "", false
	case domain.MessageStatusBounced:
		return next, next == domain.MessageStatusComplained
	case domain.MessageStatusDelivered:
		if next == domain.MessageStatusDelivered {
			return "", false
		}
		return next, next == domain.MessageStatusBounced || next == domain.MessageStatusComplained
	default:
		return next, true
	}
}

func eventTypeForMessageStatus(status domain.MessageStatus) domain.ProviderEventType {
	switch status {
	case domain.MessageStatusDelivered:
		return domain.ProviderEventDelivered
	case domain.MessageStatusBounced:
		return domain.ProviderEventBounced
	case domain.MessageStatusComplained:
		return domain.ProviderEventComplained
	default:
		return ""
	}
}

func providerEventMetric(eventType domain.ProviderEventType) (string, bool) {
	switch eventType {
	case domain.ProviderEventDelivered:
		return lite.MetricProviderEventsDelivered, true
	case domain.ProviderEventBounced:
		return lite.MetricProviderEventsBounced, true
	case domain.ProviderEventComplained:
		return lite.MetricProviderEventsComplained, true
	default:
		return "", false
	}
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
		EventPayload:        normalizedProviderEventPayload,
		OccurredAt:          strings.TrimSpace(request.OccurredAt),
	}
	if event.AppCode == "" || event.MessageID == "" {
		return domain.ProviderEvent{}, domain.RequestValidationError{Code: domain.ErrorCodeInvalidJSON, Message: "app and message_id are required"}
	}
	if !isValidIdentifierFilter(event.AppCode) {
		return domain.ProviderEvent{}, domain.RequestValidationError{Code: domain.ErrorCodeInvalidJSON, Message: "app is invalid"}
	}
	if !isValidMessageIDValue(event.MessageID) {
		return domain.ProviderEvent{}, domain.RequestValidationError{Code: domain.ErrorCodeInvalidJSON, Message: "message_id is invalid"}
	}
	if !event.Provider.IsValid() {
		return domain.ProviderEvent{}, domain.RequestValidationError{Code: domain.ErrorCodeInvalidJSON, Message: "provider is invalid"}
	}
	if !event.EventType.IsValid() {
		return domain.ProviderEvent{}, domain.RequestValidationError{Code: domain.ErrorCodeInvalidJSON, Message: "event_type is invalid"}
	}
	if err := validateProviderEventIdentity(&event); err != nil {
		return domain.ProviderEvent{}, err
	}
	if err := validateProviderEventRecipientEmail(event); err != nil {
		return domain.ProviderEvent{}, err
	}

	return event, nil
}

func validateProviderEventIdentity(event *domain.ProviderEvent) error {
	if event == nil {
		return domain.RequestValidationError{Code: domain.ErrorCodeInvalidJSON, Message: "provider event identity is required"}
	}
	event.ProviderAccountCode = strings.TrimSpace(event.ProviderAccountCode)
	event.ProviderChannelCode = strings.TrimSpace(event.ProviderChannelCode)
	event.ProviderMessageID = strings.TrimSpace(event.ProviderMessageID)
	event.OccurredAt = strings.TrimSpace(event.OccurredAt)
	if event.ProviderAccountCode == "" ||
		event.ProviderChannelCode == "" ||
		event.ProviderMessageID == "" ||
		event.OccurredAt == "" {
		return domain.RequestValidationError{Code: domain.ErrorCodeInvalidJSON, Message: "provider account, channel, message id, and occurred_at are required"}
	}
	if !isValidIdentifierFilter(event.ProviderAccountCode) || !isValidIdentifierFilter(event.ProviderChannelCode) {
		return domain.RequestValidationError{Code: domain.ErrorCodeInvalidJSON, Message: "provider account or channel is invalid"}
	}
	occurredAt, err := normalizeProviderEventOccurredAt(event.OccurredAt)
	if err != nil {
		return domain.RequestValidationError{Code: domain.ErrorCodeInvalidJSON, Message: "occurred_at must be RFC3339"}
	}
	event.OccurredAt = occurredAt

	return nil
}

func normalizeProviderEventOccurredAt(value string) (string, error) {
	occurredAt, err := time.Parse(time.RFC3339Nano, strings.TrimSpace(value))
	if err != nil {
		return "", err
	}

	return occurredAt.UTC().Format(time.RFC3339Nano), nil
}

func validateProviderEventRecipientEmail(event domain.ProviderEvent) error {
	if !requiresSuppression(event.EventType) {
		return nil
	}
	if strings.TrimSpace(event.RecipientEmail) == "" {
		return domain.RequestValidationError{Code: domain.ErrorCodeInvalidJSON, Message: "recipient email is required for bounce and complaint events"}
	}
	if !isValidSingleAddrSpecEmail(event.RecipientEmail) {
		return domain.RequestValidationError{Code: domain.ErrorCodeInvalidJSON, Message: "recipient email is invalid"}
	}

	return nil
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
