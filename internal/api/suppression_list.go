package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/mail"
	"strconv"
	"strings"

	"github.com/muxmail/muxmail/internal/domain"
	"github.com/muxmail/muxmail/internal/lite"
)

const (
	defaultSuppressionListLimit = 50
	maxSuppressionListLimit     = 200
)

type suppressionListResponse struct {
	App          string                    `json:"app"`
	Limit        int                       `json:"limit"`
	Suppressions []suppressionResponseItem `json:"suppressions"`
}

type suppressionResponseItem struct {
	Email           string                   `json:"email"`
	NormalizedEmail string                   `json:"normalized_email"`
	Reason          domain.SuppressionReason `json:"reason"`
}

func (r *Runtime) handleSuppressionList(w http.ResponseWriter, httpRequest *http.Request) {
	requestID, err := domain.NewRequestID()
	if err != nil {
		writeAPIError(w, "", fmt.Errorf("generate request id: %w", err))
		return
	}
	if httpRequest.Method != http.MethodGet {
		http.NotFound(w, httpRequest)
		return
	}

	response, err := r.processSuppressionList(httpRequest)
	if err != nil {
		writeAPIError(w, requestID, err)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(response)
}

func (r *Runtime) processSuppressionList(httpRequest *http.Request) (suppressionListResponse, error) {
	auth, err := r.auth.AuthenticateHeader(httpRequest.Header.Get("Authorization"))
	if err != nil {
		return suppressionListResponse{}, err
	}

	filter, err := parseSuppressionListFilter(httpRequest)
	if err != nil {
		return suppressionListResponse{}, err
	}
	entries := r.suppressed.List(auth.App.Code, filter)

	response := suppressionListResponse{
		App:          auth.App.Code,
		Limit:        filter.Limit,
		Suppressions: make([]suppressionResponseItem, 0, len(entries)),
	}
	for _, entry := range entries {
		response.Suppressions = append(response.Suppressions, suppressionResponseItem{
			Email:           entry.Email,
			NormalizedEmail: entry.NormalizedEmail,
			Reason:          entry.Reason,
		})
	}

	return response, nil
}

func parseSuppressionListFilter(httpRequest *http.Request) (lite.SuppressionListFilter, error) {
	query := httpRequest.URL.Query()
	limit := defaultSuppressionListLimit
	if rawLimit := strings.TrimSpace(query.Get("limit")); rawLimit != "" {
		parsedLimit, err := strconv.Atoi(rawLimit)
		if err != nil || parsedLimit <= 0 || parsedLimit > maxSuppressionListLimit {
			return lite.SuppressionListFilter{}, domain.RequestValidationError{Code: domain.ErrorCodeInvalidQuery, Message: "limit must be between 1 and 200"}
		}
		limit = parsedLimit
	}

	var reason domain.SuppressionReason
	if rawReason := strings.TrimSpace(query.Get("reason")); rawReason != "" {
		reason = domain.SuppressionReason(rawReason)
		if !reason.IsValid() {
			return lite.SuppressionListFilter{}, domain.RequestValidationError{Code: domain.ErrorCodeInvalidQuery, Message: "reason is invalid"}
		}
	}

	email := strings.TrimSpace(query.Get("email"))
	if email != "" {
		if !isValidSuppressionFilterEmail(email) {
			return lite.SuppressionListFilter{}, domain.RequestValidationError{Code: domain.ErrorCodeInvalidQuery, Message: "email is invalid"}
		}
	}

	return lite.SuppressionListFilter{
		Limit:  limit,
		Reason: reason,
		Email:  email,
	}, nil
}

func isValidSuppressionFilterEmail(value string) bool {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" || !isASCII(trimmed) {
		return false
	}
	if strings.ContainsAny(trimmed, "<>") || strings.Contains(trimmed, " ") {
		return false
	}
	if _, err := mail.ParseAddress(trimmed); err != nil {
		return false
	}

	parts := strings.Split(trimmed, "@")
	return len(parts) == 2 && parts[0] != "" && parts[1] != ""
}

func isASCII(value string) bool {
	for _, r := range value {
		if r > 127 {
			return false
		}
	}

	return true
}
