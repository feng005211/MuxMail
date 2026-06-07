package provider

import (
	"bufio"
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"net"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/muxmail/muxmail/internal/domain"
)

func TestSMTPTransportSendsTextOnlyMIME(t *testing.T) {
	server := newTestSMTPServer(t, testSMTPServerConfig{})
	defer server.close()

	result := sendThroughTestSMTP(t, server, testSMTPMessage("text"))
	if !result.IsAccepted() {
		t.Fatalf("expected accepted SMTP result, got %+v", result)
	}

	data := server.messageData()
	if !strings.Contains(data, `Content-Type: text/plain; charset="utf-8"`) {
		t.Fatalf("expected text/plain MIME, got:\n%s", data)
	}
	if !strings.Contains(data, "WW91ciBjb2RlIGlzIDEyMzQ1Ni4=") {
		t.Fatalf("expected base64 encoded text body, got:\n%s", data)
	}
	if strings.Contains(data, "multipart/alternative") {
		t.Fatalf("did not expect multipart MIME for text-only message, got:\n%s", data)
	}
	if strings.Contains(data, "Reply-To:") || strings.Contains(data, "List-Unsubscribe:") || strings.Contains(data, "Cc:") || strings.Contains(data, "Bcc:") {
		t.Fatalf("unexpected unsupported mail headers:\n%s", data)
	}
}

func TestSMTPTransportSendsHTMLOnlyMIME(t *testing.T) {
	server := newTestSMTPServer(t, testSMTPServerConfig{})
	defer server.close()

	result := sendThroughTestSMTP(t, server, testSMTPMessage("html"))
	if !result.IsAccepted() {
		t.Fatalf("expected accepted SMTP result, got %+v", result)
	}

	data := server.messageData()
	if !strings.Contains(data, `Content-Type: text/html; charset="utf-8"`) {
		t.Fatalf("expected text/html MIME, got:\n%s", data)
	}
	if !strings.Contains(data, "PHA+WW91ciBjb2RlIGlzIDEyMzQ1Ni48L3A+") {
		t.Fatalf("expected base64 encoded HTML body, got:\n%s", data)
	}
	if strings.Contains(data, "multipart/alternative") {
		t.Fatalf("did not expect multipart MIME for HTML-only message, got:\n%s", data)
	}
}

func TestSMTPTransportSendsMultipartAlternativeMIME(t *testing.T) {
	server := newTestSMTPServer(t, testSMTPServerConfig{})
	defer server.close()

	result := sendThroughTestSMTP(t, server, testSMTPMessage("both"))
	if !result.IsAccepted() {
		t.Fatalf("expected accepted SMTP result, got %+v", result)
	}

	data := server.messageData()
	if !strings.Contains(data, "Content-Type: multipart/alternative;") ||
		!strings.Contains(data, `Content-Type: text/plain; charset="utf-8"`) ||
		!strings.Contains(data, `Content-Type: text/html; charset="utf-8"`) {
		t.Fatalf("expected multipart alternative MIME, got:\n%s", data)
	}
}

func TestSMTPTransportClassifies4xxAsTemporaryFailure(t *testing.T) {
	server := newTestSMTPServer(t, testSMTPServerConfig{rcptResponse: "451 try later"})
	defer server.close()

	result := sendThroughTestSMTP(t, server, testSMTPMessage("text"))
	if !result.IsFailed() || result.Failed.FailureClass != domain.FailureClassTemporary {
		t.Fatalf("expected temporary failure, got %+v", result)
	}
}

func TestSMTPTransportClassifies5xxRecipientAsPermanentFailure(t *testing.T) {
	server := newTestSMTPServer(t, testSMTPServerConfig{rcptResponse: "550 invalid recipient"})
	defer server.close()

	result := sendThroughTestSMTP(t, server, testSMTPMessage("text"))
	if !result.IsFailed() || result.Failed.FailureClass != domain.FailureClassMessagePermanent {
		t.Fatalf("expected permanent failure, got %+v", result)
	}
	if result.Failed.ErrorCode != domain.ErrorCodeInvalidRecipient {
		t.Fatalf("expected invalid_recipient error code, got %+v", result.Failed.ErrorCode)
	}
}

func TestSMTPTransportClassifiesAuthFailureAsChannelFailure(t *testing.T) {
	server := newTestSMTPServer(t, testSMTPServerConfig{authResponse: "535 auth failed"})
	defer server.close()

	result := sendThroughTestSMTP(t, server, testSMTPMessage("text"))
	if !result.IsFailed() || result.Failed.FailureClass != domain.FailureClassChannel {
		t.Fatalf("expected channel failure, got %+v", result)
	}
}

func TestSMTPTransportUsesAccountAPIKeyFallback(t *testing.T) {
	server := newTestSMTPServer(t, testSMTPServerConfig{})
	defer server.close()

	result := sendThroughTestSMTPWithSecrets(
		t,
		server,
		testSMTPMessage("text"),
		StaticSecretResolver{"account_api_key": "secret"},
		func(request *SendRequest) {
			request.Channel.SMTP.PasswordRef = ""
			request.Account.CredentialRefs = map[string]string{"api_key": "account_api_key"}
		},
	)
	if !result.IsAccepted() {
		t.Fatalf("expected accepted SMTP result with account fallback, got %+v", result)
	}
}

func sendThroughTestSMTP(t *testing.T, server *testSMTPServer, message domain.Message) SendResult {
	t.Helper()

	return sendThroughTestSMTPWithSecrets(t, server, message, StaticSecretResolver{"smtp_password": "secret"})
}

type testSMTPSendOption func(*SendRequest)

func sendThroughTestSMTPWithSecrets(t *testing.T, server *testSMTPServer, message domain.Message, secrets SecretResolver, options ...testSMTPSendOption) SendResult {
	t.Helper()

	port, err := strconv.Atoi(server.port())
	if err != nil {
		t.Fatalf("parse server port: %v", err)
	}
	transport := NewSMTPTransport(
		secrets,
		WithoutSMTPPortRequirement(),
		WithSMTPTLSConfig(&tls.Config{InsecureSkipVerify: true, ServerName: "localhost"}),
	)
	request := SendRequest{
		Message: message,
		Account: domain.ProviderAccount{
			Code:           "resend_main",
			Provider:       domain.ProviderResend,
			Enabled:        true,
			CredentialRefs: map[string]string{"api_key": "account_api_key"},
		},
		Channel: domain.ProviderChannel{
			Code:      "resend_auth_smtp",
			Account:   "resend_main",
			Transport: domain.TransportSMTP,
			Enabled:   true,
			FromName:  "MuxMail",
			From:      "no-reply@example.com",
			SMTP: &domain.SMTPSettings{
				Host:        "127.0.0.1",
				Port:        port,
				Username:    "resend",
				PasswordRef: "smtp_password",
			},
		},
	}
	for _, option := range options {
		option(&request)
	}

	result, err := transport.Send(context.Background(), request)
	if err != nil {
		t.Fatalf("smtp send returned error: %v", err)
	}

	return result
}

func testSMTPMessage(kind string) domain.Message {
	message := domain.Message{
		MessageID: "msg_01ABC",
		ToEmail:   "user@example.com",
		Subject:   "Your verification code",
	}
	switch kind {
	case "text":
		message.TextBody = "Your code is 123456."
	case "html":
		message.HTMLBody = "<p>Your code is 123456.</p>"
	case "both":
		message.TextBody = "Your code is 123456."
		message.HTMLBody = "<p>Your code is 123456.</p>"
	default:
		panic("unknown message kind")
	}

	return message
}

type testSMTPServerConfig struct {
	authResponse string
	rcptResponse string
}

type testSMTPServer struct {
	listener net.Listener
	cert     tls.Certificate
	config   testSMTPServerConfig
	done     chan struct{}
	mu       sync.Mutex
	data     string
}

func newTestSMTPServer(t *testing.T, config testSMTPServerConfig) *testSMTPServer {
	t.Helper()

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen smtp server: %v", err)
	}
	server := &testSMTPServer{
		listener: listener,
		cert:     generateTestCertificate(t),
		config:   config,
		done:     make(chan struct{}),
	}
	go server.serve()

	return server
}

func (s *testSMTPServer) port() string {
	_, port, _ := net.SplitHostPort(s.listener.Addr().String())
	return port
}

func (s *testSMTPServer) close() {
	_ = s.listener.Close()
	<-s.done
}

func (s *testSMTPServer) messageData() string {
	s.mu.Lock()
	defer s.mu.Unlock()

	return s.data
}

func (s *testSMTPServer) serve() {
	defer close(s.done)

	conn, err := s.listener.Accept()
	if err != nil {
		return
	}
	defer conn.Close()

	reader := bufio.NewReader(conn)
	writer := bufio.NewWriter(conn)
	writeSMTPLine(writer, "220 localhost ESMTP")
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			return
		}
		command := strings.TrimRight(line, "\r\n")
		upper := strings.ToUpper(command)
		switch {
		case strings.HasPrefix(upper, "EHLO"):
			writeSMTPLine(writer, "250-localhost")
			writeSMTPLine(writer, "250-STARTTLS")
			writeSMTPLine(writer, "250 AUTH PLAIN")
		case strings.HasPrefix(upper, "STARTTLS"):
			writeSMTPLine(writer, "220 ready")
			tlsConn := tls.Server(conn, &tls.Config{Certificates: []tls.Certificate{s.cert}})
			if err := tlsConn.Handshake(); err != nil {
				return
			}
			conn = tlsConn
			reader = bufio.NewReader(conn)
			writer = bufio.NewWriter(conn)
		case strings.HasPrefix(upper, "AUTH"):
			response := s.config.authResponse
			if response == "" {
				response = "235 authenticated"
			}
			writeSMTPLine(writer, response)
		case strings.HasPrefix(upper, "MAIL FROM"):
			writeSMTPLine(writer, "250 ok")
		case strings.HasPrefix(upper, "RCPT TO"):
			response := s.config.rcptResponse
			if response == "" {
				response = "250 ok"
			}
			writeSMTPLine(writer, response)
		case strings.HasPrefix(upper, "DATA"):
			writeSMTPLine(writer, "354 end with dot")
			data, err := readSMTPData(reader)
			if err != nil {
				return
			}
			s.mu.Lock()
			s.data = data
			s.mu.Unlock()
			writeSMTPLine(writer, "250 queued")
		case strings.HasPrefix(upper, "QUIT"):
			writeSMTPLine(writer, "221 bye")
			return
		default:
			writeSMTPLine(writer, "250 ok")
		}
	}
}

func writeSMTPLine(writer *bufio.Writer, line string) {
	_, _ = writer.WriteString(line + "\r\n")
	_ = writer.Flush()
}

func readSMTPData(reader *bufio.Reader) (string, error) {
	var builder strings.Builder
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			return "", err
		}
		trimmed := strings.TrimRight(line, "\r\n")
		if trimmed == "." {
			return builder.String(), nil
		}
		builder.WriteString(line)
	}
}

func generateTestCertificate(t *testing.T) tls.Certificate {
	t.Helper()

	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate test key: %v", err)
	}
	template := x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "localhost"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		KeyUsage:     x509.KeyUsageKeyEncipherment | x509.KeyUsageDigitalSignature,
		DNSNames:     []string{"localhost"},
		IPAddresses:  []net.IP{net.ParseIP("127.0.0.1")},
	}
	certDER, err := x509.CreateCertificate(rand.Reader, &template, &template, &key.PublicKey, key)
	if err != nil {
		t.Fatalf("create test certificate: %v", err)
	}

	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certDER})
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)})
	cert, err := tls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		t.Fatalf("load test certificate: %v", err)
	}

	return cert
}
