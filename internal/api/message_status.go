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

const messageStatusPathPrefix = "/v1/mail/messages/"

type messageStatusResponse struct {
	MessageID         string               `json:"message_id"`
	RequestID         string               `json:"request_id"`
	BusinessRequestID string               `json:"business_request_id,omitempty"`
	App               string               `json:"app"`
	Scene             string               `json:"scene"`
	Status            domain.MessageStatus `json:"status"`
	Locale            string               `json:"locale"`
	ToDomain          string               `json:"to_domain"`
	ToHash            string               `json:"to_hash"`
	ErrorCode         domain.ErrorCode     `json:"error_code,omitempty"`
	ErrorMessage      string               `json:"error_message,omitempty"`
	UpdatedAt         string               `json:"updated_at"`
}

func (r *Runtime) handleMessageStatus(w http.ResponseWriter, httpRequest *http.Request) {
	requestID, err := domain.NewRequestID()
	if err != nil {
		writeAPIError(w, "", fmt.Errorf("generate request id: %w", err))
		return
	}
	if httpRequest.Method != http.MethodGet {
		http.NotFound(w, httpRequest)
		return
	}

	response, err := r.processMessageStatus(httpRequest)
	if err != nil {
		writeAPIError(w, requestID, err)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(response)
}

func (r *Runtime) processMessageStatus(httpRequest *http.Request) (messageStatusResponse, error) {
	auth, err := r.auth.AuthenticateHeader(httpRequest.Header.Get("Authorization"))
	if err != nil {
		return messageStatusResponse{}, err
	}

	messageID, ok := messageIDFromPath(httpRequest.URL.Path)
	if !ok {
		return messageStatusResponse{}, APIError{Code: domain.ErrorCodeMessageNotFound, Message: "message not found"}
	}

	snapshot, found, err := r.messageLog.FindLatestMessage(auth.App.Code, messageID)
	if err != nil {
		return messageStatusResponse{}, fmt.Errorf("find message status: %w", err)
	}
	if !found {
		return messageStatusResponse{}, APIError{Code: domain.ErrorCodeMessageNotFound, Message: "message not found"}
	}

	return messageStatusFromSnapshot(snapshot), nil
}

func messageIDFromPath(path string) (string, bool) {
	messageID := strings.TrimPrefix(path, messageStatusPathPrefix)
	if messageID == "" || messageID == path || strings.Contains(messageID, "/") || strings.ContainsAny(messageID, " \t\r\n") {
		return "", false
	}

	return messageID, true
}

func messageStatusFromSnapshot(snapshot lite.MessageSnapshot) messageStatusResponse {
	return messageStatusResponse{
		MessageID:         snapshot.MessageID,
		RequestID:         snapshot.RequestID,
		BusinessRequestID: snapshot.BusinessRequestID,
		App:               snapshot.AppCode,
		Scene:             snapshot.SceneCode,
		Status:            snapshot.Status,
		Locale:            snapshot.Locale,
		ToDomain:          snapshot.ToDomain,
		ToHash:            snapshot.ToHash,
		ErrorCode:         snapshot.ErrorCode,
		ErrorMessage:      snapshot.ErrorMessage,
		UpdatedAt:         snapshot.Timestamp.UTC().Format(time.RFC3339Nano),
	}
}
