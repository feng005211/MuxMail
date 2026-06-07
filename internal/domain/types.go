package domain

// App is the top-level business isolation unit in MuxMail.
type App struct {
	Code           string
	Name           string
	Enabled        bool
	DefaultLocale  string
	AllowedLocales []string
	APIKeys        []APIKeyMetadata
	Scenes         []Scene
	Templates      []Template
}

// APIKeyMetadata stores the runtime metadata needed to authenticate an App key.
type APIKeyMetadata struct {
	Name    string
	Enabled bool
	KeyHash string
}

// Scene defines a sending scenario and its policy bindings.
type Scene struct {
	Code        string
	Name        string
	Enabled     bool
	Template    string
	RateLimit   RateLimitPolicy
	RoutePolicy RoutePolicy
}

// Template defines one locale-specific email template.
type Template struct {
	Code         string
	Locale       string
	Enabled      bool
	Subject      string
	RequiredVars []string
	HTMLBody     string
	TextBody     string
}

// ProviderAccount stores provider identity and secret references.
type ProviderAccount struct {
	Code           string
	Provider       Provider
	Enabled        bool
	CredentialRefs map[string]string
}

// ProviderChannel defines one routable delivery channel.
type ProviderChannel struct {
	Code         string
	Account      string
	Transport    Transport
	Enabled      bool
	SenderDomain string
	FromName     string
	From         string
	SMTP         *SMTPSettings
}

// SMTPSettings stores SMTP submission settings for a provider channel.
type SMTPSettings struct {
	Host        string
	Port        int
	Username    string
	PasswordRef string
}

// RoutePolicy maps recipient domains to ordered provider channel codes.
type RoutePolicy map[string][]string

// RateLimitPolicy defines fixed-window limits for one Scene.
type RateLimitPolicy struct {
	SameEmailPerMinute  int
	SameEmailPerDay     int
	SameUserIPPerHour   int
	SameCallerIPPerHour int
}

// Message is the internal representation of a mail send request after validation.
type Message struct {
	RequestID          string
	BusinessRequestID  string
	MessageID          string
	AppCode            string
	APIKeyName         string
	SceneCode          string
	ToEmail            string
	NormalizedToEmail  string
	ToDomain           string
	ToHash             string
	Locale             string
	Subject            string
	HTMLBody           string
	TextBody           string
	ProviderChannels   []string
	Status             MessageStatus
	IdempotencyHash    string
	RequestFingerprint string
	CallerIP           string
	UserIP             string
	UserIDHash         string
	ErrorCode          ErrorCode
	ErrorMessage       string
}

// Attempt records one provider channel delivery attempt.
type Attempt struct {
	MessageID           string
	AttemptNo           int
	Provider            Provider
	ProviderAccountCode string
	ProviderChannelCode string
	Transport           Transport
	Status              AttemptStatus
	FailureClass        FailureClass
	ErrorCode           ErrorCode
	ErrorMessage        string
	ProviderMessageID   string
	DurationMS          int
}

// ProviderEvent records one normalized provider webhook event.
type ProviderEvent struct {
	MessageID           string
	AppCode             string
	Provider            Provider
	ProviderAccountCode string
	ProviderChannelCode string
	ProviderMessageID   string
	EventType           ProviderEventType
	RecipientEmail      string
	EventPayload        string
	OccurredAt          string
}

// SuppressionEntry defines one App-scoped suppressed recipient address.
type SuppressionEntry struct {
	AppCode         string
	Email           string
	NormalizedEmail string
	Reason          SuppressionReason
}
