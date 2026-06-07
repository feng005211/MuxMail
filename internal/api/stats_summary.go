package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"time"

	"github.com/muxmail/muxmail/internal/domain"
	"github.com/muxmail/muxmail/internal/lite"
)

type statsSummaryResponse struct {
	App               string                         `json:"app"`
	Window            string                         `json:"window"`
	Since             string                         `json:"since"`
	Until             string                         `json:"until"`
	Metrics           map[string]float64             `json:"metrics"`
	ProviderDurations []providerDurationSummaryEntry `json:"provider_durations"`
}

type providerDurationSummaryEntry struct {
	ProviderChannel string           `json:"provider_channel"`
	Transport       domain.Transport `json:"transport"`
	Count           int              `json:"count"`
	AverageMS       float64          `json:"average_ms"`
	TotalMS         float64          `json:"total_ms"`
}

func (r *Runtime) handleStatsSummary(w http.ResponseWriter, httpRequest *http.Request) {
	requestID, err := domain.NewRequestID()
	if err != nil {
		writeAPIError(w, "", fmt.Errorf("generate request id: %w", err))
		return
	}
	if httpRequest.Method != http.MethodGet {
		http.NotFound(w, httpRequest)
		return
	}

	response, err := r.processStatsSummary(httpRequest)
	if err != nil {
		writeAPIError(w, requestID, err)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(response)
}

func (r *Runtime) processStatsSummary(httpRequest *http.Request) (statsSummaryResponse, error) {
	auth, err := r.auth.AuthenticateHeader(httpRequest.Header.Get("Authorization"))
	if err != nil {
		return statsSummaryResponse{}, err
	}

	windowName, windowDuration, err := parseStatsWindow(httpRequest.URL.Query().Get("window"))
	if err != nil {
		return statsSummaryResponse{}, err
	}

	until := r.now().UTC()
	since := until.Add(-windowDuration)
	summary, err := r.stats.Summary(auth.App.Code, since, until)
	if err != nil {
		return statsSummaryResponse{}, fmt.Errorf("summarize stats: %w", err)
	}

	return statsSummaryFromLite(windowName, summary), nil
}

func parseStatsWindow(value string) (string, time.Duration, error) {
	if value == "" {
		value = "24h"
	}
	switch value {
	case "1h":
		return value, time.Hour, nil
	case "24h":
		return value, 24 * time.Hour, nil
	case "7d":
		return value, 7 * 24 * time.Hour, nil
	default:
		return "", 0, domain.RequestValidationError{Code: domain.ErrorCodeInvalidQuery, Message: "window must be 1h, 24h, or 7d"}
	}
}

func statsSummaryFromLite(window string, summary lite.StatsSummary) statsSummaryResponse {
	entries := make([]providerDurationSummaryEntry, 0, len(summary.ProviderDurations))
	channels := make([]string, 0, len(summary.ProviderDurations))
	for channel := range summary.ProviderDurations {
		channels = append(channels, channel)
	}
	sort.Strings(channels)
	for _, channel := range channels {
		duration := summary.ProviderDurations[channel]
		entries = append(entries, providerDurationSummaryEntry{
			ProviderChannel: duration.ProviderChannelCode,
			Transport:       duration.Transport,
			Count:           duration.Count,
			AverageMS:       duration.AverageMS,
			TotalMS:         duration.TotalMS,
		})
	}

	return statsSummaryResponse{
		App:               summary.AppCode,
		Window:            window,
		Since:             summary.Since.UTC().Format(time.RFC3339Nano),
		Until:             summary.Until.UTC().Format(time.RFC3339Nano),
		Metrics:           summary.Metrics,
		ProviderDurations: entries,
	}
}
