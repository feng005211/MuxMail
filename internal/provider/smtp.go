package provider

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/base64"
	"errors"
	"fmt"
	"mime"
	"mime/multipart"
	"net"
	"net/mail"
	"net/smtp"
	"net/textproto"
	"strings"

	"github.com/muxmail/muxmail/internal/domain"
)

// SMTPTransport sends mail through SMTP submission with STARTTLS.
type SMTPTransport struct {
	secrets     SecretResolver
	dialer      func(ctx context.Context, network string, address string) (net.Conn, error)
	tlsConfig   *tls.Config
	requirePort bool
}

// SMTPTransportOption customizes SMTP transport behavior.
type SMTPTransportOption func(*SMTPTransport)

// NewSMTPTransport creates the shared SMTP transport used by provider adapters.
func NewSMTPTransport(secrets SecretResolver, options ...SMTPTransportOption) *SMTPTransport {
	transport := &SMTPTransport{
		secrets:     secrets,
		requirePort: true,
	}
	for _, option := range options {
		option(transport)
	}

	return transport
}

// WithSMTPDialer overrides network dialing for local tests.
func WithSMTPDialer(dialer func(ctx context.Context, network string, address string) (net.Conn, error)) SMTPTransportOption {
	return func(transport *SMTPTransport) {
		transport.dialer = dialer
	}
}

// WithSMTPTLSConfig overrides the TLS config used by STARTTLS.
func WithSMTPTLSConfig(config *tls.Config) SMTPTransportOption {
	return func(transport *SMTPTransport) {
		transport.tlsConfig = config
	}
}

// WithoutSMTPPortRequirement disables the production port-587 guard for local tests.
func WithoutSMTPPortRequirement() SMTPTransportOption {
	return func(transport *SMTPTransport) {
		transport.requirePort = false
	}
}

// Send sends one message through SMTP and returns a normalized provider result.
func (t *SMTPTransport) Send(ctx context.Context, request SendRequest) (SendResult, error) {
	if err := ctx.Err(); err != nil {
		return SendResult{}, err
	}
	if request.Channel.Transport != domain.TransportSMTP {
		return ChannelFailure(domain.ErrorCodeProviderUnavailable, "smtp transport required"), nil
	}
	if request.Channel.SMTP == nil || request.Channel.SMTP.Host == "" || request.Channel.SMTP.Port == 0 || request.Channel.SMTP.Username == "" {
		return ChannelFailure(domain.ErrorCodeProviderUnavailable, "smtp configuration invalid"), nil
	}
	if t.requirePort && request.Channel.SMTP.Port != 587 {
		return ChannelFailure(domain.ErrorCodeProviderUnavailable, "smtp port must be 587"), nil
	}

	password, err := t.password(request)
	if err != nil {
		return ChannelFailure(domain.ErrorCodeProviderUnavailable, "smtp password unavailable"), nil
	}
	message, err := buildMIMEMessage(request)
	if err != nil {
		return MessagePermanentFailure(domain.ErrorCodeInternal, "smtp message build failed"), nil
	}

	address := net.JoinHostPort(request.Channel.SMTP.Host, fmt.Sprintf("%d", request.Channel.SMTP.Port))
	client, err := t.openClient(ctx, address, request.Channel.SMTP.Host)
	if err != nil {
		return TemporaryFailure(domain.ErrorCodeProviderUnavailable, "smtp connection failed"), nil
	}
	defer client.Close()

	if result, failed := smtpResult(client.Hello("localhost"), smtpStageConnect); failed {
		return result, nil
	}
	tlsConfig := t.tlsConfig
	if tlsConfig == nil {
		tlsConfig = &tls.Config{ServerName: request.Channel.SMTP.Host, MinVersion: tls.VersionTLS12}
	}
	if result, failed := smtpResult(client.StartTLS(tlsConfig), smtpStageStartTLS); failed {
		return result, nil
	}
	if result, failed := smtpResult(client.Auth(smtp.PlainAuth("", request.Channel.SMTP.Username, password, request.Channel.SMTP.Host)), smtpStageAuth); failed {
		return result, nil
	}
	if result, failed := smtpResult(client.Mail(request.Channel.From), smtpStageMailFrom); failed {
		return result, nil
	}
	if result, failed := smtpResult(client.Rcpt(strings.TrimSpace(request.Message.ToEmail)), smtpStageRcptTo); failed {
		return result, nil
	}

	writer, err := client.Data()
	if err != nil {
		return classifySMTPError(err, smtpStageData), nil
	}
	if _, err := writer.Write(message); err != nil {
		writer.Close()
		return TemporaryFailure(domain.ErrorCodeProviderUnavailable, "smtp data write failed"), nil
	}
	if err := writer.Close(); err != nil {
		return classifySMTPError(err, smtpStageData), nil
	}
	_ = client.Quit()

	return Accepted(""), nil
}

func (t *SMTPTransport) password(request SendRequest) (string, error) {
	ref := ""
	if request.Channel.SMTP != nil {
		ref = request.Channel.SMTP.PasswordRef
	}
	if ref == "" {
		ref = request.Account.CredentialRefs["api_key"]
	}
	if ref == "" || t.secrets == nil {
		return "", fmt.Errorf("smtp password reference is required")
	}

	return t.secrets.ResolveSecret(ref)
}

func (t *SMTPTransport) openClient(ctx context.Context, address string, host string) (*smtp.Client, error) {
	var conn net.Conn
	var err error
	if t.dialer != nil {
		conn, err = t.dialer(ctx, "tcp", address)
	} else {
		dialer := net.Dialer{}
		conn, err = dialer.DialContext(ctx, "tcp", address)
	}
	if err != nil {
		return nil, err
	}
	if deadline, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(deadline)
	}

	client, err := smtp.NewClient(conn, host)
	if err != nil {
		conn.Close()
		return nil, err
	}

	return client, nil
}

func buildMIMEMessage(request SendRequest) ([]byte, error) {
	from := (&mail.Address{Name: request.Channel.FromName, Address: request.Channel.From}).String()
	to := strings.TrimSpace(request.Message.ToEmail)
	headers := textproto.MIMEHeader{}
	headers.Set("From", from)
	headers.Set("To", to)
	headers.Set("Subject", mime.QEncoding.Encode("utf-8", request.Message.Subject))
	headers.Set("MIME-Version", "1.0")

	switch {
	case request.Message.HTMLBody != "" && request.Message.TextBody != "":
		return buildMultipartAlternative(headers, request.Message.TextBody, request.Message.HTMLBody)
	case request.Message.HTMLBody != "":
		headers.Set("Content-Type", `text/html; charset="utf-8"`)
		headers.Set("Content-Transfer-Encoding", "base64")
		return buildSinglepart(headers, request.Message.HTMLBody), nil
	case request.Message.TextBody != "":
		headers.Set("Content-Type", `text/plain; charset="utf-8"`)
		headers.Set("Content-Transfer-Encoding", "base64")
		return buildSinglepart(headers, request.Message.TextBody), nil
	default:
		return nil, fmt.Errorf("message body is required")
	}
}

func buildSinglepart(headers textproto.MIMEHeader, body string) []byte {
	var buffer bytes.Buffer
	writeHeaders(&buffer, headers)
	buffer.WriteString("\r\n")
	writeBase64Body(&buffer, body)
	return buffer.Bytes()
}

func buildMultipartAlternative(headers textproto.MIMEHeader, textBody string, htmlBody string) ([]byte, error) {
	var buffer bytes.Buffer
	multipartWriter := multipart.NewWriter(&buffer)
	headers.Set("Content-Type", `multipart/alternative; boundary="`+multipartWriter.Boundary()+`"`)
	writeHeaders(&buffer, headers)
	buffer.WriteString("\r\n")

	textHeaders := textproto.MIMEHeader{}
	textHeaders.Set("Content-Type", `text/plain; charset="utf-8"`)
	textHeaders.Set("Content-Transfer-Encoding", "base64")
	textPart, err := multipartWriter.CreatePart(textHeaders)
	if err != nil {
		return nil, err
	}
	writeBase64ToWriter(textPart, textBody)

	htmlHeaders := textproto.MIMEHeader{}
	htmlHeaders.Set("Content-Type", `text/html; charset="utf-8"`)
	htmlHeaders.Set("Content-Transfer-Encoding", "base64")
	htmlPart, err := multipartWriter.CreatePart(htmlHeaders)
	if err != nil {
		return nil, err
	}
	writeBase64ToWriter(htmlPart, htmlBody)

	if err := multipartWriter.Close(); err != nil {
		return nil, err
	}

	return buffer.Bytes(), nil
}

func writeHeaders(buffer *bytes.Buffer, headers textproto.MIMEHeader) {
	order := []string{"From", "To", "Subject", "MIME-Version", "Content-Type", "Content-Transfer-Encoding"}
	for _, key := range order {
		values := headers.Values(key)
		for _, value := range values {
			buffer.WriteString(key)
			buffer.WriteString(": ")
			buffer.WriteString(value)
			buffer.WriteString("\r\n")
		}
	}
}

func writeBase64Body(buffer *bytes.Buffer, body string) {
	encoded := base64.StdEncoding.EncodeToString([]byte(body))
	for len(encoded) > 76 {
		buffer.WriteString(encoded[:76])
		buffer.WriteString("\r\n")
		encoded = encoded[76:]
	}
	buffer.WriteString(encoded)
	buffer.WriteString("\r\n")
}

func writeBase64ToWriter(writer interface{ Write([]byte) (int, error) }, body string) {
	var buffer bytes.Buffer
	writeBase64Body(&buffer, body)
	_, _ = writer.Write(buffer.Bytes())
}

type smtpStage string

const (
	smtpStageConnect  smtpStage = "connect"
	smtpStageStartTLS smtpStage = "starttls"
	smtpStageAuth     smtpStage = "auth"
	smtpStageMailFrom smtpStage = "mail_from"
	smtpStageRcptTo   smtpStage = "rcpt_to"
	smtpStageData     smtpStage = "data"
	smtpStageQuit     smtpStage = "quit"
)

func smtpResult(err error, stage smtpStage) (SendResult, bool) {
	if err == nil {
		return SendResult{}, false
	}

	return classifySMTPError(err, stage), true
}

func classifySMTPError(err error, stage smtpStage) SendResult {
	var smtpErr *textproto.Error
	if errors.As(err, &smtpErr) {
		switch {
		case smtpErr.Code >= 400 && smtpErr.Code <= 499:
			return TemporaryFailure(domain.ErrorCodeProviderUnavailable, "smtp temporary failure")
		case smtpErr.Code >= 500 && smtpErr.Code <= 599:
			switch stage {
			case smtpStageConnect, smtpStageStartTLS, smtpStageAuth, smtpStageMailFrom:
				return ChannelFailure(domain.ErrorCodeProviderUnavailable, "smtp channel failure")
			case smtpStageRcptTo:
				return MessagePermanentFailure(domain.ErrorCodeInvalidRecipient, "smtp recipient rejected")
			case smtpStageData:
				return MessagePermanentFailure(domain.ErrorCodeProviderUnavailable, "smtp message rejected")
			default:
				return MessagePermanentFailure(domain.ErrorCodeProviderUnavailable, "smtp permanent failure")
			}
		}
	}

	return TemporaryFailure(domain.ErrorCodeProviderUnavailable, "smtp request failed")
}
