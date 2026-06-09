package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/muxmail/muxmail/internal/domain"
	"github.com/muxmail/muxmail/internal/lite"
)

const (
	defaultMessageListLimit = 50
	maxMessageListLimit     = 200
	maxSceneFilterBytes     = 64
)

type messageListResponse struct {
	App      string                  `json:"app"`
	Limit    int                     `json:"limit"`
	Messages []messageStatusResponse `json:"messages"`
}

func (r *Runtime) handleMessageList(w http.ResponseWriter, httpRequest *http.Request) {
	requestID, err := domain.NewRequestID()
	if err != nil {
		writeAPIError(w, "", fmt.Errorf("generate request id: %w", err))
		return
	}
	if httpRequest.Method != http.MethodGet {
		http.NotFound(w, httpRequest)
		return
	}

	response, err := r.processMessageList(httpRequest)
	if err != nil {
		writeAPIError(w, requestID, err)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(response)
}

func (r *Runtime) processMessageList(httpRequest *http.Request) (messageListResponse, error) {
	auth, err := r.auth.AuthenticateHeader(httpRequest.Header.Get("Authorization"))
	if err != nil {
		return messageListResponse{}, err
	}

	filter, err := parseMessageListFilter(httpRequest)
	if err != nil {
		return messageListResponse{}, err
	}
	messages, err := r.messageLog.ListLatestMessages(auth.App.Code, filter)
	if err != nil {
		return messageListResponse{}, fmt.Errorf("list latest messages: %w", err)
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

func parseMessageListFilter(httpRequest *http.Request) (lite.MessageListFilter, error) {
	query := httpRequest.URL.Query()
	limit := defaultMessageListLimit
	if rawLimit := strings.TrimSpace(query.Get("limit")); rawLimit != "" {
		parsedLimit, err := strconv.Atoi(rawLimit)
		if err != nil || parsedLimit <= 0 || parsedLimit > maxMessageListLimit {
			return lite.MessageListFilter{}, domain.RequestValidationError{Code: domain.ErrorCodeInvalidQuery, Message: "limit must be between 1 and 200"}
		}
		limit = parsedLimit
	}

	var status domain.MessageStatus
	if rawStatus := strings.TrimSpace(query.Get("status")); rawStatus != "" {
		status = domain.MessageStatus(rawStatus)
		if !status.IsValid() {
			return lite.MessageListFilter{}, domain.RequestValidationError{Code: domain.ErrorCodeInvalidQuery, Message: "status is invalid"}
		}
	}

	scene := strings.TrimSpace(query.Get("scene"))
	if scene != "" && !isValidIdentifierFilter(scene) {
		return lite.MessageListFilter{}, domain.RequestValidationError{Code: domain.ErrorCodeInvalidQuery, Message: "scene is invalid"}
	}

	return lite.MessageListFilter{
		Limit:  limit,
		Status: status,
		Scene:  scene,
	}, nil
}

func isValidIdentifierFilter(value string) bool {
	if value == "" || len(value) > maxSceneFilterBytes {
		return false
	}
	for index := 0; index < len(value); index++ {
		char := value[index]
		valid := (char >= 'a' && char <= 'z') ||
			(char >= '0' && char <= '9') ||
			char == '_' ||
			char == '-'
		if !valid {
			return false
		}
		if (index == 0 || index == len(value)-1) && !(char >= 'a' && char <= 'z') && !(char >= '0' && char <= '9') {
			return false
		}
	}

	return true
}
