package api

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"strings"

	"github.com/muxmail/muxmail/internal/domain"
	"github.com/muxmail/muxmail/internal/lite"
	mailtemplate "github.com/muxmail/muxmail/internal/template"
)

type sendResponse struct {
	RequestID string               `json:"request_id"`
	MessageID string               `json:"message_id"`
	Status    domain.MessageStatus `json:"status"`
}

func (r *Runtime) handleSend(w http.ResponseWriter, httpRequest *http.Request) {
	requestID, err := domain.NewRequestID()
	if err != nil {
		writeAPIError(w, "", fmt.Errorf("generate request id: %w", err))
		return
	}
	if httpRequest.Method != http.MethodPost {
		http.NotFound(w, httpRequest)
		return
	}

	response, err := r.processSend(httpRequest, requestID)
	if err != nil {
		writeAPIError(w, requestID, err)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusAccepted)
	_ = json.NewEncoder(w).Encode(response)
}

func (r *Runtime) processSend(httpRequest *http.Request, requestID string) (sendResponse, error) {
	auth, err := r.auth.AuthenticateHeader(httpRequest.Header.Get("Authorization"))
	if err != nil {
		return sendResponse{}, err
	}
	if err := validateSendContentType(httpRequest.Header.Get("Content-Type")); err != nil {
		return sendResponse{}, err
	}

	maxBodyBytes := int64(r.defaults.MaxRequestBodyBytes)
	body, err := io.ReadAll(io.LimitReader(httpRequest.Body, maxBodyBytes+1))
	if err != nil {
		return sendResponse{}, domain.RequestValidationError{Code: domain.ErrorCodeRequestTooLarge, Message: "request body is too large"}
	}
	if int64(len(body)) > maxBodyBytes {
		return sendResponse{}, domain.RequestValidationError{Code: domain.ErrorCodeRequestTooLarge, Message: "request body is too large"}
	}
	raw, err := decodeSendJSONObject(body)
	if err != nil {
		return sendResponse{}, err
	}

	sceneCode, err := extractSceneCode(raw)
	if err != nil {
		return sendResponse{}, err
	}

	scene, err := findSceneByCode(auth.App, sceneCode)
	if err != nil {
		return sendResponse{}, err
	}

	request, err := domain.ValidateSendRequest(domain.SendRequestValidationInput{
		ContentType:    httpRequest.Header.Get("Content-Type"),
		IdempotencyKey: httpRequest.Header.Get("Idempotency-Key"),
		Body:           body,
		AllowedLocales: auth.App.AllowedLocales,
	}, domain.SendRequestValidationOptions{
		MaxRequestBodyBytes: r.defaults.MaxRequestBodyBytes,
		MaxTemplateVarBytes: r.defaults.MaxTemplateVarBytes,
		MaxContextBytes:     r.defaults.MaxContextBytes,
	})
	if err != nil {
		return sendResponse{}, err
	}

	rendered, err := mailtemplate.Render(auth.App, scene, request)
	if err != nil {
		return sendResponse{}, err
	}

	fingerprint, err := domain.RequestFingerprint(request.NormalizedToEmail, rendered.Locale, request.Vars)
	if err != nil {
		return sendResponse{}, fmt.Errorf("build request fingerprint: %w", err)
	}
	idempotencyHash := domain.IdempotencyHash(auth.App.Code, scene.Code, request.IdempotencyKey)

	idempotencyReservation, idempotencyDecision, err := r.idempotent.Reserve(auth.App.Code, scene.Code, idempotencyHash, fingerprint)
	if err != nil {
		return sendResponse{}, err
	}
	if idempotencyDecision.State == lite.IdempotencyDecisionReplay {
		_ = r.stats.Record(lite.StatsRecord{
			AppCode:   auth.App.Code,
			SceneCode: scene.Code,
			Metric:    lite.MetricRequestsIdempotentReplay,
			Value:     1,
		})
		return sendResponse{RequestID: requestID, MessageID: idempotencyDecision.MessageID, Status: domain.MessageStatusQueued}, nil
	}
	idempotencyCompleted := false
	defer func() {
		if !idempotencyCompleted {
			idempotencyReservation.Release()
		}
	}()

	if entry, ok := r.suppressed.Contains(auth.App.Code, request.NormalizedToEmail); ok {
		return sendResponse{}, domain.RequestValidationError{
			Code:    domain.ErrorCodeSuppressedRecipient,
			Message: "recipient is suppressed: " + string(entry.Reason),
		}
	}

	selection, err := domain.SelectRoute(scene, request.NormalizedToEmail, r.defaults.MaxAttemptsPerMessage)
	if err != nil {
		return sendResponse{}, err
	}

	callerIP := r.callerIP.resolve(httpRequest.RemoteAddr, httpRequest.Header)
	queueReservation, err := r.queue.Reserve()
	if err != nil {
		var queueFull lite.QueueFullError
		if errors.As(err, &queueFull) {
			_ = r.stats.Record(lite.StatsRecord{
				AppCode:   auth.App.Code,
				SceneCode: scene.Code,
				Metric:    lite.MetricRequestsQueueFull,
				Value:     1,
			})
		}
		return sendResponse{}, err
	}
	queueCommitted := false
	defer func() {
		if !queueCommitted {
			queueReservation.Release()
		}
	}()

	rateLimitRequest := lite.RateLimitRequest{
		AppCode:           auth.App.Code,
		SceneCode:         scene.Code,
		NormalizedToEmail: request.NormalizedToEmail,
		UserIP:            request.UserIP,
		CallerIP:          callerIP,
		Policy:            scene.RateLimit,
	}
	rateLimitDecision, err := r.rateLimit.Allow(rateLimitRequest)
	if err != nil {
		_ = r.stats.Record(lite.StatsRecord{
			AppCode:   auth.App.Code,
			SceneCode: scene.Code,
			Metric:    lite.MetricRequestsRateLimited,
			Value:     1,
		})
		return sendResponse{}, err
	}
	rateLimitCommitted := false
	defer func() {
		if !rateLimitCommitted {
			r.rateLimit.Rollback(rateLimitDecision)
		}
	}()

	messageID, err := domain.NewMessageID()
	if err != nil {
		return sendResponse{}, fmt.Errorf("generate message id: %w", err)
	}
	message := domain.Message{
		RequestID:          requestID,
		BusinessRequestID:  request.BusinessRequestID,
		MessageID:          messageID,
		AppCode:            auth.App.Code,
		APIKeyName:         auth.APIKey.Name,
		SceneCode:          scene.Code,
		ToEmail:            request.To,
		NormalizedToEmail:  request.NormalizedToEmail,
		ToDomain:           selection.RecipientDomain,
		ToHash:             domain.ToHash(auth.App.Code, request.NormalizedToEmail),
		Locale:             rendered.Locale,
		Subject:            rendered.Subject,
		HTMLBody:           rendered.HTMLBody,
		TextBody:           rendered.TextBody,
		ProviderChannels:   selection.Channels,
		Status:             domain.MessageStatusQueued,
		IdempotencyHash:    idempotencyHash,
		RequestFingerprint: fingerprint,
		CallerIP:           callerIP,
		UserIP:             request.UserIP,
		UserIDHash:         domain.UserIDHash(auth.App.Code, request.UserID),
	}
	if err := r.messageLog.AppendMessage(message); err != nil {
		return sendResponse{}, fmt.Errorf("append queued message: %w", err)
	}
	if err := commitInitialQueueTask(r.messageLog, queueReservation, message); err != nil {
		return sendResponse{}, err
	}
	queueCommitted = true
	rateLimitCommitted = true
	if err := idempotencyReservation.CompleteQueued(messageID); err != nil {
		return sendResponse{}, err
	}
	idempotencyCompleted = true

	_ = r.stats.Record(lite.StatsRecord{
		AppCode:   auth.App.Code,
		SceneCode: scene.Code,
		Metric:    lite.MetricMessagesQueued,
		Value:     1,
	})

	return sendResponse{RequestID: requestID, MessageID: messageID, Status: domain.MessageStatusQueued}, nil
}

func commitInitialQueueTask(messageLog *lite.MessageLog, reservation *lite.QueueReservation, message domain.Message) error {
	if err := reservation.Commit(lite.QueueTask{Message: message, AttemptNo: 1}); err != nil {
		failed := message
		failed.Status = domain.MessageStatusFailed
		failed.ErrorCode = domain.ErrorCodeInternal
		failed.ErrorMessage = "queue commit failed"
		if appendErr := messageLog.AppendMessage(failed); appendErr != nil {
			return fmt.Errorf("commit queued message: %w; append failed message: %v", err, appendErr)
		}
		return err
	}

	return nil
}

func findSceneByCode(app domain.App, code string) (domain.Scene, error) {
	for _, scene := range app.Scenes {
		if scene.Code != code {
			continue
		}
		if !scene.Enabled {
			return domain.Scene{}, APIError{Code: domain.ErrorCodeSceneDisabled, Message: "scene disabled"}
		}

		return scene, nil
	}

	return domain.Scene{}, APIError{Code: domain.ErrorCodeSceneNotFound, Message: "scene not found"}
}

func validateSendContentType(contentType string) error {
	if strings.TrimSpace(contentType) == "" {
		return domain.RequestValidationError{Code: domain.ErrorCodeUnsupportedMediaType, Message: "content type must be application/json"}
	}
	mediaType, _, err := mime.ParseMediaType(contentType)
	if err != nil || !strings.EqualFold(mediaType, "application/json") {
		return domain.RequestValidationError{Code: domain.ErrorCodeUnsupportedMediaType, Message: "content type must be application/json"}
	}

	return nil
}

func decodeSendJSONObject(body []byte) (map[string]json.RawMessage, error) {
	var raw map[string]json.RawMessage
	decoder := json.NewDecoder(bytes.NewReader(body))
	if err := decoder.Decode(&raw); err != nil {
		return nil, domain.RequestValidationError{Code: domain.ErrorCodeInvalidJSON, Message: "request body must be a JSON object"}
	}
	var trailing any
	if err := decoder.Decode(&trailing); err == nil {
		return nil, domain.RequestValidationError{Code: domain.ErrorCodeInvalidJSON, Message: "request body must contain a single JSON object"}
	} else if err != io.EOF {
		return nil, domain.RequestValidationError{Code: domain.ErrorCodeInvalidJSON, Message: "request body must contain a single JSON object"}
	}
	if raw == nil {
		return nil, domain.RequestValidationError{Code: domain.ErrorCodeInvalidJSON, Message: "request body must be a JSON object"}
	}

	return raw, nil
}

func extractSceneCode(raw map[string]json.RawMessage) (string, error) {
	value, exists := raw["scene"]
	if !exists {
		return "", domain.RequestValidationError{Code: domain.ErrorCodeInvalidJSON, Message: "scene is required"}
	}
	var scene string
	if err := json.Unmarshal(value, &scene); err != nil {
		return "", domain.RequestValidationError{Code: domain.ErrorCodeInvalidJSON, Message: "scene must be a string"}
	}
	if scene == "" {
		return "", domain.RequestValidationError{Code: domain.ErrorCodeInvalidJSON, Message: "scene is required"}
	}
	if !isValidIdentifierFilter(scene) {
		return "", domain.RequestValidationError{Code: domain.ErrorCodeInvalidJSON, Message: "scene is invalid"}
	}

	return scene, nil
}
