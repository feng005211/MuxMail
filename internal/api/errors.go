package api

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/muxmail/muxmail/internal/domain"
	"github.com/muxmail/muxmail/internal/lite"
	mailtemplate "github.com/muxmail/muxmail/internal/template"
)

type errorResponse struct {
	Error errorPayload `json:"error"`
}

type errorPayload struct {
	Code      domain.ErrorCode `json:"code"`
	Message   string           `json:"message"`
	RequestID string           `json:"request_id"`
}

// APIError is a stable synchronous API failure outside pure request validation.
type APIError struct {
	Code    domain.ErrorCode
	Message string
}

// Error returns the stable public API error code.
func (e APIError) Error() string {
	return string(e.Code)
}

func writeAPIError(w http.ResponseWriter, requestID string, err error) {
	code, message := classifyAPIError(err)
	status := statusForErrorCode(code)

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(errorResponse{
		Error: errorPayload{
			Code:      code,
			Message:   message,
			RequestID: requestID,
		},
	})
}

func classifyAPIError(err error) (domain.ErrorCode, string) {
	var apiError APIError
	if errors.As(err, &apiError) {
		return apiError.Code, apiError.Message
	}

	var validationError domain.RequestValidationError
	if errors.As(err, &validationError) {
		return validationError.Code, validationError.Message
	}

	var renderError mailtemplate.RenderError
	if errors.As(err, &renderError) {
		return renderError.Code, renderError.Message
	}

	var routeError domain.RouteSelectionError
	if errors.As(err, &routeError) {
		return routeError.Code, routeError.Message
	}

	var idempotencyConflict lite.IdempotencyConflictError
	if errors.As(err, &idempotencyConflict) {
		return idempotencyConflict.Code, "idempotency conflict"
	}

	var rateLimited lite.RateLimitExceededError
	if errors.As(err, &rateLimited) {
		return rateLimited.Code, "request rate limited"
	}

	var queueFull lite.QueueFullError
	if errors.As(err, &queueFull) {
		return queueFull.Code, "queue is full"
	}

	var authError AuthError
	if errors.As(err, &authError) {
		return authError.Code, authError.Message
	}

	if errors.Is(err, lite.ErrMemoryQueueClosed) {
		return domain.ErrorCodeInternal, "internal error"
	}

	return domain.ErrorCodeInternal, "internal error"
}

func statusForErrorCode(code domain.ErrorCode) int {
	switch code {
	case domain.ErrorCodeUnauthorized:
		return http.StatusUnauthorized
	case domain.ErrorCodeAppDisabled, domain.ErrorCodeSceneDisabled:
		return http.StatusForbidden
	case domain.ErrorCodeSceneNotFound, domain.ErrorCodeMessageNotFound:
		return http.StatusNotFound
	case domain.ErrorCodeWebhookDisabled:
		return http.StatusServiceUnavailable
	case domain.ErrorCodeIdempotencyConflict:
		return http.StatusConflict
	case domain.ErrorCodeRequestTooLarge:
		return http.StatusRequestEntityTooLarge
	case domain.ErrorCodeUnsupportedMediaType:
		return http.StatusUnsupportedMediaType
	case domain.ErrorCodeRateLimited:
		return http.StatusTooManyRequests
	case domain.ErrorCodeQueueFull:
		return http.StatusServiceUnavailable
	case domain.ErrorCodeInternal:
		return http.StatusInternalServerError
	default:
		return http.StatusUnprocessableEntity
	}
}
