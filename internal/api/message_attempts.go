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

const messageAttemptsPathSuffix = "/attempts"

type messageAttemptsResponse struct {
	MessageID string                        `json:"message_id"`
	App       string                        `json:"app"`
	Attempts  []messageAttemptResponseEntry `json:"attempts"`
}

type messageAttemptResponseEntry struct {
	LoggedAt          string               `json:"logged_at"`
	AttemptNo         int                  `json:"attempt_no"`
	Provider          domain.Provider      `json:"provider"`
	ProviderAccount   string               `json:"provider_account"`
	ProviderChannel   string               `json:"provider_channel"`
	Transport         domain.Transport     `json:"transport"`
	Status            domain.AttemptStatus `json:"status"`
	FailureClass      domain.FailureClass  `json:"failure_class,omitempty"`
	ErrorCode         domain.ErrorCode     `json:"error_code,omitempty"`
	ErrorMessage      string               `json:"error_message,omitempty"`
	ProviderMessageID string               `json:"provider_message_id,omitempty"`
	DurationMS        int                  `json:"duration_ms"`
}

func (r *Runtime) handleMessageAttempts(w http.ResponseWriter, httpRequest *http.Request) {
	requestID, err := domain.NewRequestID()
	if err != nil {
		writeAPIError(w, "", fmt.Errorf("generate request id: %w", err))
		return
	}
	if httpRequest.Method != http.MethodGet {
		http.NotFound(w, httpRequest)
		return
	}

	response, err := r.processMessageAttempts(httpRequest)
	if err != nil {
		writeAPIError(w, requestID, err)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(response)
}

func (r *Runtime) processMessageAttempts(httpRequest *http.Request) (messageAttemptsResponse, error) {
	auth, err := r.auth.AuthenticateHeader(httpRequest.Header.Get("Authorization"))
	if err != nil {
		return messageAttemptsResponse{}, err
	}

	messageID, ok := messageIDFromAttemptsPath(httpRequest.URL.Path)
	if !ok {
		return messageAttemptsResponse{}, APIError{Code: domain.ErrorCodeMessageNotFound, Message: "message not found"}
	}

	snapshot, found, err := r.messageLog.FindLatestMessage(auth.App.Code, messageID)
	if err != nil {
		return messageAttemptsResponse{}, fmt.Errorf("find message for attempt query: %w", err)
	}
	if !found {
		return messageAttemptsResponse{}, APIError{Code: domain.ErrorCodeMessageNotFound, Message: "message not found"}
	}

	attempts, err := r.messageLog.ListAttempts(auth.App.Code, messageID)
	if err != nil {
		return messageAttemptsResponse{}, fmt.Errorf("list message attempts: %w", err)
	}

	return messageAttemptsFromSnapshots(snapshot, attempts), nil
}

func messageIDFromAttemptsPath(path string) (string, bool) {
	if !strings.HasSuffix(path, messageAttemptsPathSuffix) {
		return "", false
	}
	messagePath := strings.TrimSuffix(path, messageAttemptsPathSuffix)
	return messageIDFromPath(messagePath)
}

func messageAttemptsFromSnapshots(snapshot lite.MessageSnapshot, attempts []lite.AttemptSnapshot) messageAttemptsResponse {
	response := messageAttemptsResponse{
		MessageID: snapshot.MessageID,
		App:       snapshot.AppCode,
		Attempts:  make([]messageAttemptResponseEntry, 0, len(attempts)),
	}
	for _, attempt := range attempts {
		response.Attempts = append(response.Attempts, messageAttemptResponseEntry{
			LoggedAt:          attempt.Timestamp.UTC().Format(time.RFC3339Nano),
			AttemptNo:         attempt.AttemptNo,
			Provider:          attempt.Provider,
			ProviderAccount:   attempt.ProviderAccountCode,
			ProviderChannel:   attempt.ProviderChannelCode,
			Transport:         attempt.Transport,
			Status:            attempt.Status,
			FailureClass:      attempt.FailureClass,
			ErrorCode:         attempt.ErrorCode,
			ErrorMessage:      attempt.ErrorMessage,
			ProviderMessageID: attempt.ProviderMessageID,
			DurationMS:        attempt.DurationMS,
		})
	}

	return response
}
