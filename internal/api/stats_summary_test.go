package api

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/muxmail/muxmail/internal/config"
	"github.com/muxmail/muxmail/internal/domain"
	"github.com/muxmail/muxmail/internal/lite"
)

func TestStatsSummaryReturnsEmptyWhenStatsOff(t *testing.T) {
	runtime := openTestRuntime(t, "off")
	defer runtime.Close()

	recorder := performStatsSummary(t, runtime, "", testAPIKey)
	if recorder.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", recorder.Code, recorder.Body.String())
	}

	var response statsSummaryResponse
	decodeJSON(t, recorder.Body.String(), &response)
	if response.App != "project_a" || response.Window != "24h" {
		t.Fatalf("unexpected summary identity: %+v", response)
	}
	if len(response.Metrics) != 0 || len(response.ProviderDurations) != 0 {
		t.Fatalf("expected empty stats summary, got %+v", response)
	}
}

func TestStatsSummaryAggregatesFileStatsForApp(t *testing.T) {
	now := time.Date(2026, 5, 28, 4, 4, 5, 0, time.UTC)
	cfg := testRuntimeConfig(t, "file")
	runtime, err := NewRuntime(cfg, config.NewSecretResolver(), WithNow(func() time.Time {
		return now
	}))
	if err != nil {
		t.Fatalf("open runtime: %v", err)
	}
	defer runtime.Close()

	_ = runtime.Stats().Record(lite.StatsRecord{
		Timestamp: now.Add(-10 * time.Minute),
		AppCode:   "project_a",
		SceneCode: "register_code",
		Metric:    lite.MetricMessagesQueued,
		Value:     1,
	})
	_ = runtime.Stats().Record(lite.StatsRecord{
		Timestamp:           now.Add(-9 * time.Minute),
		AppCode:             "project_a",
		SceneCode:           "register_code",
		ProviderChannelCode: "resend_auth_api",
		Transport:           domain.TransportAPI,
		Metric:              lite.MetricProviderDurationMS,
		Value:               40,
	})
	_ = runtime.Stats().Record(lite.StatsRecord{
		Timestamp:           now.Add(-8 * time.Minute),
		AppCode:             "project_a",
		SceneCode:           "register_code",
		ProviderChannelCode: "resend_auth_api",
		Transport:           domain.TransportAPI,
		Metric:              lite.MetricProviderDurationMS,
		Value:               60,
	})
	_ = runtime.Stats().Record(lite.StatsRecord{
		Timestamp: now.Add(-7 * time.Minute),
		AppCode:   "project_b",
		Metric:    lite.MetricMessagesQueued,
		Value:     99,
	})

	recorder := performStatsSummary(t, runtime, "1h", testAPIKey)
	if recorder.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", recorder.Code, recorder.Body.String())
	}

	var response statsSummaryResponse
	decodeJSON(t, recorder.Body.String(), &response)
	if response.Metrics[lite.MetricMessagesQueued] != 1 {
		t.Fatalf("expected app-scoped queued metric 1, got %+v", response.Metrics)
	}
	if len(response.ProviderDurations) != 1 {
		t.Fatalf("expected one duration entry, got %+v", response.ProviderDurations)
	}
	duration := response.ProviderDurations[0]
	if duration.ProviderChannel != "resend_auth_api" || duration.Count != 2 || duration.AverageMS != 50 {
		t.Fatalf("unexpected duration summary: %+v", duration)
	}
	if response.Since == "" || response.Until == "" {
		t.Fatalf("expected since/until timestamps, got %+v", response)
	}
}

func TestStatsSummaryRejectsInvalidWindow(t *testing.T) {
	runtime := openTestRuntime(t, "off")
	defer runtime.Close()

	recorder := performStatsSummary(t, runtime, "30d", testAPIKey)
	assertErrorResponse(t, recorder, http.StatusUnprocessableEntity, domain.ErrorCodeInvalidQuery)
}

func TestStatsSummaryRequiresAuthorization(t *testing.T) {
	runtime := openTestRuntime(t, "off")
	defer runtime.Close()

	recorder := performStatsSummary(t, runtime, "24h", "")
	assertErrorResponse(t, recorder, http.StatusUnauthorized, domain.ErrorCodeUnauthorized)
}

func performStatsSummary(t *testing.T, runtime *Runtime, window string, apiKey string) *httptest.ResponseRecorder {
	t.Helper()

	request := httptest.NewRequest(http.MethodGet, "/v1/stats/summary", nil)
	if window != "" {
		query := request.URL.Query()
		query.Set("window", window)
		request.URL.RawQuery = query.Encode()
	}
	if apiKey != "" {
		request.Header.Set("Authorization", "Bearer "+apiKey)
	}
	request.RemoteAddr = "127.0.0.1:12345"
	recorder := httptest.NewRecorder()
	runtime.Handler().ServeHTTP(recorder, request)

	return recorder
}
