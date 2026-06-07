package provider

import (
	"context"

	"github.com/muxmail/muxmail/internal/domain"
)

// Provider sends one rendered message through a provider channel.
type Provider interface {
	Send(ctx context.Context, request SendRequest) (SendResult, error)
}

// SendRequest contains the provider-neutral data needed to send a message.
type SendRequest struct {
	Message domain.Message
	Account domain.ProviderAccount
	Channel domain.ProviderChannel
	Attempt int
}

// SendResult is the normalized provider adapter result.
type SendResult struct {
	Accepted          *AcceptedResult
	Failed            *FailedResult
	RetryAfterSeconds int
}

// AcceptedResult means the provider accepted the message for delivery.
type AcceptedResult struct {
	ProviderMessageID string
}

// FailedResult means the provider rejected or could not complete the attempt.
type FailedResult struct {
	FailureClass domain.FailureClass
	ErrorCode    domain.ErrorCode
	ErrorMessage string
}

// Accepted creates a normalized accepted result.
func Accepted(providerMessageID string) SendResult {
	return SendResult{
		Accepted: &AcceptedResult{
			ProviderMessageID: providerMessageID,
		},
	}
}

// Failed creates a normalized failed result.
func Failed(failureClass domain.FailureClass, errorCode domain.ErrorCode, errorMessage string) SendResult {
	return SendResult{
		Failed: &FailedResult{
			FailureClass: failureClass,
			ErrorCode:    errorCode,
			ErrorMessage: errorMessage,
		},
	}
}

// TemporaryFailure creates a failed result that can try the next provider channel.
func TemporaryFailure(errorCode domain.ErrorCode, errorMessage string) SendResult {
	return Failed(domain.FailureClassTemporary, errorCode, errorMessage)
}

// ChannelFailure creates a failed result for an unusable provider channel.
func ChannelFailure(errorCode domain.ErrorCode, errorMessage string) SendResult {
	return Failed(domain.FailureClassChannel, errorCode, errorMessage)
}

// MessagePermanentFailure creates a failed result that stops further attempts.
func MessagePermanentFailure(errorCode domain.ErrorCode, errorMessage string) SendResult {
	return Failed(domain.FailureClassMessagePermanent, errorCode, errorMessage)
}

// WithRetryAfter returns result with a provider retry-after hint in seconds.
func WithRetryAfter(result SendResult, retryAfterSeconds int) SendResult {
	if retryAfterSeconds > 0 {
		result.RetryAfterSeconds = retryAfterSeconds
	}

	return result
}

// IsAccepted reports whether result is accepted.
func (r SendResult) IsAccepted() bool {
	return r.Accepted != nil && r.Failed == nil
}

// IsFailed reports whether result is failed.
func (r SendResult) IsFailed() bool {
	return r.Failed != nil && r.Accepted == nil
}
