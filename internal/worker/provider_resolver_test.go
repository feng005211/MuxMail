package worker

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
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/muxmail/muxmail/internal/config"
	"github.com/muxmail/muxmail/internal/domain"
	"github.com/muxmail/muxmail/internal/lite"
	"github.com/muxmail/muxmail/internal/provider"
)

func TestProviderResolverFromConfigSendsResendSMTPWithMetadata(t *testing.T) {
	assertProviderResolverSendsSMTPWithMetadata(t, domain.ProviderResend, "resend_main", "resend_auth_smtp")
}

func TestProviderResolverFromConfigSendsBrevoSMTPWithMetadata(t *testing.T) {
	assertProviderResolverSendsSMTPWithMetadata(t, domain.ProviderBrevo, "brevo_main", "brevo_auth_smtp")
}

func TestProviderResolverFromConfigSendsResendAPIWithMetadata(t *testing.T) {
	assertProviderResolverSendsAPIWithMetadata(
		t,
		domain.ProviderResend,
		"resend_main",
		"resend_auth_api",
		"/emails",
		`{"id":"resend_api_123"}`,
		"resend_api_123",
	)
}

func TestProviderResolverFromConfigSendsBrevoAPIWithMetadata(t *testing.T) {
	assertProviderResolverSendsAPIWithMetadata(
		t,
		domain.ProviderBrevo,
		"brevo_main",
		"brevo_auth_api",
		"/smtp/email",
		`{"messageId":"brevo_api_123"}`,
		"brevo_api_123",
	)
}

func assertProviderResolverSendsAPIWithMetadata(t *testing.T, providerName domain.Provider, accountCode string, channelCode string, path string, responseBody string, providerMessageID string) {
	t.Helper()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		if request.URL.Path != path {
			t.Fatalf("expected path %s, got %s", path, request.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(responseBody))
	}))
	defer server.Close()

	options := []ProviderResolverBuildOption{}
	switch providerName {
	case domain.ProviderResend:
		options = append(options, WithResendAPIOptions(
			provider.WithResendBaseURL(server.URL),
			provider.WithResendHTTPClient(server.Client()),
		))
	case domain.ProviderBrevo:
		options = append(options, WithBrevoAPIOptions(
			provider.WithBrevoBaseURL(server.URL),
			provider.WithBrevoHTTPClient(server.Client()),
		))
	}
	resolver, err := NewProviderResolverFromConfig(testAPIResolverConfig(providerName, accountCode, channelCode), config.NewSecretResolver(), options...)
	if err != nil {
		t.Fatalf("build provider resolver: %v", err)
	}

	now := func() time.Time {
		return time.Date(2026, 5, 29, 1, 2, 3, 0, time.UTC)
	}
	dir := t.TempDir()
	log, err := lite.NewMessageLog(lite.MessageLogConfig{
		Dir:        dir,
		MaxBytes:   1 << 20,
		MaxBackups: 2,
		Now:        now,
	})
	if err != nil {
		t.Fatalf("open message log: %v", err)
	}
	defer log.Close()

	queue, err := lite.NewMemoryQueue(lite.MemoryQueueConfig{Capacity: 10, Now: now})
	if err != nil {
		t.Fatalf("open queue: %v", err)
	}
	defer queue.Close()

	workerRuntime, err := New(Config{
		Queue:                 queue,
		MessageLog:            log,
		Stats:                 lite.NewNoopStatsSink(),
		ProviderResolver:      resolver,
		MaxAttemptsPerMessage: 3,
		RetryBackoffSeconds:   []int{0, 0, 0},
		ProviderTimeout:       2 * time.Second,
		WorkerConcurrency:     1,
		Now:                   now,
	})
	if err != nil {
		t.Fatalf("open worker: %v", err)
	}

	message := testWorkerMessage()
	message.ToEmail = "user@example.com"
	message.ProviderChannels = []string{channelCode}
	if err := workerRuntime.ProcessTask(context.Background(), lite.QueueTask{Message: message, AttemptNo: 1}); err != nil {
		t.Fatalf("process provider api task: %v", err)
	}

	attempts := readJSONLLines(t, filepath.Join(dir, "mail-attempts.jsonl"))
	if len(attempts) != 2 {
		t.Fatalf("expected sending and sent attempts, got %d", len(attempts))
	}
	sentAttempt := attempts[1]
	assertRecordValue(t, sentAttempt, "provider", string(providerName))
	assertRecordValue(t, sentAttempt, "provider_account", accountCode)
	assertRecordValue(t, sentAttempt, "provider_channel", channelCode)
	assertRecordValue(t, sentAttempt, "transport", string(domain.TransportAPI))
	assertRecordValue(t, sentAttempt, "provider_message_id", providerMessageID)
}

func assertProviderResolverSendsSMTPWithMetadata(t *testing.T, providerName domain.Provider, accountCode string, channelCode string) {
	t.Helper()

	server := newWorkerSMTPServer(t)
	defer server.close()

	port, err := strconv.Atoi(server.port())
	if err != nil {
		t.Fatalf("parse smtp port: %v", err)
	}

	resolver, err := NewProviderResolverFromConfig(
		testSMTPResolverConfig(providerName, accountCode, channelCode, port),
		config.NewSecretResolver(),
		WithSMTPTransportOptions(
			provider.WithoutSMTPPortRequirement(),
			provider.WithSMTPTLSConfig(&tls.Config{InsecureSkipVerify: true, ServerName: "localhost"}),
		),
	)
	if err != nil {
		t.Fatalf("build provider resolver: %v", err)
	}

	now := func() time.Time {
		return time.Date(2026, 5, 29, 1, 2, 3, 0, time.UTC)
	}
	dir := t.TempDir()
	log, err := lite.NewMessageLog(lite.MessageLogConfig{
		Dir:        dir,
		MaxBytes:   1 << 20,
		MaxBackups: 2,
		Now:        now,
	})
	if err != nil {
		t.Fatalf("open message log: %v", err)
	}
	defer log.Close()

	queue, err := lite.NewMemoryQueue(lite.MemoryQueueConfig{Capacity: 10, Now: now})
	if err != nil {
		t.Fatalf("open queue: %v", err)
	}
	defer queue.Close()

	workerRuntime, err := New(Config{
		Queue:                 queue,
		MessageLog:            log,
		Stats:                 lite.NewNoopStatsSink(),
		ProviderResolver:      resolver,
		MaxAttemptsPerMessage: 3,
		RetryBackoffSeconds:   []int{0, 0, 0},
		ProviderTimeout:       2 * time.Second,
		WorkerConcurrency:     1,
		Now:                   now,
	})
	if err != nil {
		t.Fatalf("open worker: %v", err)
	}

	message := testWorkerMessage()
	message.ToEmail = "user@example.com"
	message.ProviderChannels = []string{channelCode}
	if err := workerRuntime.ProcessTask(context.Background(), lite.QueueTask{Message: message, AttemptNo: 1}); err != nil {
		t.Fatalf("process smtp task: %v", err)
	}

	attempts := readJSONLLines(t, filepath.Join(dir, "mail-attempts.jsonl"))
	if len(attempts) != 2 {
		t.Fatalf("expected sending and sent attempts, got %d", len(attempts))
	}
	sentAttempt := attempts[1]
	assertRecordValue(t, sentAttempt, "provider", string(providerName))
	assertRecordValue(t, sentAttempt, "provider_account", accountCode)
	assertRecordValue(t, sentAttempt, "provider_channel", channelCode)
	assertRecordValue(t, sentAttempt, "transport", string(domain.TransportSMTP))
	assertRecordValue(t, sentAttempt, "status", string(domain.AttemptStatusSent))
}

func testAPIResolverConfig(providerName domain.Provider, accountCode string, channelCode string) *config.Config {
	return &config.Config{
		ProviderAccounts: []config.ProviderAccountConfig{
			{
				Code:     accountCode,
				Provider: providerName,
				Credentials: map[string]string{
					"api_key": "plain:provider-secret",
				},
			},
		},
		ProviderChannels: []config.ProviderChannelConfig{
			{
				Code:         channelCode,
				Account:      accountCode,
				Transport:    domain.TransportAPI,
				SenderDomain: "auth.example.com",
				FromName:     "MuxMail",
				From:         "no-reply@auth.example.com",
			},
		},
	}
}

func testSMTPResolverConfig(providerName domain.Provider, accountCode string, channelCode string, port int) *config.Config {
	return &config.Config{
		ProviderAccounts: []config.ProviderAccountConfig{
			{
				Code:     accountCode,
				Provider: providerName,
				Credentials: map[string]string{
					"api_key": "plain:smtp-secret",
				},
			},
		},
		ProviderChannels: []config.ProviderChannelConfig{
			{
				Code:         channelCode,
				Account:      accountCode,
				Transport:    domain.TransportSMTP,
				SenderDomain: "auth.example.com",
				FromName:     "MuxMail",
				From:         "no-reply@auth.example.com",
				SMTP: &config.SMTPConfig{
					Host:     "127.0.0.1",
					Port:     port,
					Username: "muxmail",
				},
			},
		},
	}
}

type workerSMTPServer struct {
	listener net.Listener
	cert     tls.Certificate
	done     chan struct{}
	mu       sync.Mutex
	conn     net.Conn
}

func newWorkerSMTPServer(t *testing.T) *workerSMTPServer {
	t.Helper()

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen smtp server: %v", err)
	}
	server := &workerSMTPServer{
		listener: listener,
		cert:     generateWorkerSMTPCertificate(t),
		done:     make(chan struct{}),
	}
	go server.serve()

	return server
}

func (s *workerSMTPServer) port() string {
	_, port, _ := net.SplitHostPort(s.listener.Addr().String())
	return port
}

func (s *workerSMTPServer) close() {
	_ = s.listener.Close()
	s.mu.Lock()
	if s.conn != nil {
		_ = s.conn.Close()
	}
	s.mu.Unlock()
	<-s.done
}

func (s *workerSMTPServer) serve() {
	defer close(s.done)

	conn, err := s.listener.Accept()
	if err != nil {
		return
	}
	defer conn.Close()
	s.setConn(conn)
	_ = conn.SetDeadline(time.Now().Add(5 * time.Second))

	reader := bufio.NewReader(conn)
	writer := bufio.NewWriter(conn)
	writeWorkerSMTPLine(writer, "220 localhost ESMTP")
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			return
		}
		command := strings.TrimRight(line, "\r\n")
		upper := strings.ToUpper(command)
		switch {
		case strings.HasPrefix(upper, "EHLO"):
			writeWorkerSMTPLine(writer, "250-localhost")
			writeWorkerSMTPLine(writer, "250-STARTTLS")
			writeWorkerSMTPLine(writer, "250 AUTH PLAIN")
		case strings.HasPrefix(upper, "STARTTLS"):
			writeWorkerSMTPLine(writer, "220 ready")
			tlsConn := tls.Server(conn, &tls.Config{Certificates: []tls.Certificate{s.cert}})
			if err := tlsConn.Handshake(); err != nil {
				return
			}
			conn = tlsConn
			s.setConn(conn)
			_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
			reader = bufio.NewReader(conn)
			writer = bufio.NewWriter(conn)
		case strings.HasPrefix(upper, "AUTH"):
			writeWorkerSMTPLine(writer, "235 authenticated")
		case strings.HasPrefix(upper, "MAIL FROM"):
			writeWorkerSMTPLine(writer, "250 ok")
		case strings.HasPrefix(upper, "RCPT TO"):
			writeWorkerSMTPLine(writer, "250 ok")
		case strings.HasPrefix(upper, "DATA"):
			writeWorkerSMTPLine(writer, "354 end with dot")
			if err := discardWorkerSMTPData(reader); err != nil {
				return
			}
			writeWorkerSMTPLine(writer, "250 queued")
		case strings.HasPrefix(upper, "QUIT"):
			writeWorkerSMTPLine(writer, "221 bye")
			return
		default:
			writeWorkerSMTPLine(writer, "250 ok")
		}
	}
}

func (s *workerSMTPServer) setConn(conn net.Conn) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.conn = conn
}

func writeWorkerSMTPLine(writer *bufio.Writer, line string) {
	_, _ = writer.WriteString(line + "\r\n")
	_ = writer.Flush()
}

func discardWorkerSMTPData(reader *bufio.Reader) error {
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			return err
		}
		if strings.TrimRight(line, "\r\n") == "." {
			return nil
		}
	}
}

func generateWorkerSMTPCertificate(t *testing.T) tls.Certificate {
	t.Helper()

	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate smtp key: %v", err)
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
		t.Fatalf("create smtp certificate: %v", err)
	}

	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certDER})
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)})
	cert, err := tls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		t.Fatalf("load smtp certificate: %v", err)
	}

	return cert
}
