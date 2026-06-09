package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/muxmail/muxmail/internal/domain"
)

func (r *Runtime) handleFailedMessageList(w http.ResponseWriter, httpRequest *http.Request) {
	requestID, err := domain.NewRequestID()
	if err != nil {
		writeAPIError(w, "", fmt.Errorf("generate request id: %w", err))
		return
	}
	if httpRequest.Method != http.MethodGet {
		http.NotFound(w, httpRequest)
		return
	}

	response, err := r.processFailedMessageList(httpRequest)
	if err != nil {
		writeAPIError(w, requestID, err)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(response)
}

func (r *Runtime) processFailedMessageList(httpRequest *http.Request) (messageListResponse, error) {
	auth, err := r.auth.AuthenticateHeader(httpRequest.Header.Get("Authorization"))
	if err != nil {
		return messageListResponse{}, err
	}

	if strings.TrimSpace(httpRequest.URL.Query().Get("status")) != "" {
		return messageListResponse{}, domain.RequestValidationError{Code: domain.ErrorCodeInvalidQuery, Message: "status is not supported on failed message list"}
	}

	filter, err := parseMessageListFilter(httpRequest)
	if err != nil {
		return messageListResponse{}, err
	}
	filter.Status = domain.MessageStatusFailed

	messages, err := r.messageLog.ListLatestMessages(auth.App.Code, filter)
	if err != nil {
		return messageListResponse{}, fmt.Errorf("list failed messages: %w", err)
	}

	response := messageListResponse{
		App:      auth.App.Code,
		Limit:    filter.Limit,
		Messages: make([]messageStatusResponse, 0, len(messages)),
	}
	for _, message := range messages {
		response.Messages = append(response.Messages, messageStatusFromSnapshot(message))
	}

	return response, nil
}
