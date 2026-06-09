export type Locale = 'en-US' | 'zh-CN';

export type MessageStatus =
  | 'queued'
  | 'sending'
  | 'retrying'
  | 'sent'
  | 'failed'
  | 'delivered'
  | 'bounced'
  | 'complained';

export type Provider = 'mock' | 'resend' | 'brevo';
export type Transport = 'api' | 'smtp';
export type AttemptProvider = Provider | '';
export type AttemptTransport = Transport | '';
export type AttemptStatus = 'sending' | 'sent' | 'failed';
export type FailureClass = '' | 'temporary_failure' | 'channel_failure' | 'message_permanent_failure';
export type ProviderEventType = 'delivered' | 'bounced' | 'complained';
export type SuppressionReason = 'hard_bounce' | 'complaint' | 'manual';

export interface ErrorResponse {
  error: {
    code: string;
    message: string;
    request_id: string;
  };
}

export interface HealthResponse {
  status: string;
}

export interface VersionResponse {
  version: string;
}

export interface MessageSnapshot {
  message_id: string;
  request_id: string;
  business_request_id?: string;
  app: string;
  scene: string;
  status: MessageStatus;
  locale: string;
  to_domain: string;
  to_hash: string;
  error_code?: string;
  error_message?: string;
  updated_at: string;
}

export interface MessageListResponse {
  app: string;
  limit: number;
  messages: MessageSnapshot[];
}

export interface MessageAttemptsResponse {
  message_id: string;
  app: string;
  attempts: MessageAttempt[];
}

export interface MessageAttempt {
  logged_at: string;
  attempt_no: number;
  provider: AttemptProvider;
  provider_account: string;
  provider_channel: string;
  transport: AttemptTransport;
  status: AttemptStatus;
  failure_class?: FailureClass;
  error_code?: string;
  error_message?: string;
  provider_message_id?: string;
  duration_ms: number;
}

export interface MessageEventsResponse {
  message_id: string;
  app: string;
  events: MessageEvent[];
}

export interface MessageEvent {
  logged_at: string;
  provider: Provider;
  provider_account: string;
  provider_channel: string;
  provider_message_id: string;
  event_type: ProviderEventType;
  occurred_at: string;
}

export interface ProviderEventListResponse {
  app: string;
  limit: number;
  events: ProviderEventListEntry[];
}

export interface ProviderEventListEntry {
  message_id: string;
  logged_at: string;
  provider: Provider;
  provider_account: string;
  provider_channel: string;
  provider_message_id: string;
  event_type: ProviderEventType;
  occurred_at: string;
}

export interface StatsSummaryResponse {
  app: string;
  window: '1h' | '24h' | '7d';
  since: string;
  until: string;
  metrics: Record<string, number>;
  provider_durations: ProviderDuration[];
}

export interface ProviderDuration {
  provider_channel: string;
  transport: Transport;
  count: number;
  average_ms: number;
  total_ms: number;
}

export interface SuppressionListResponse {
  app: string;
  limit: number;
  suppressions: SuppressionEntry[];
}

export interface SuppressionEntry {
  email: string;
  normalized_email: string;
  reason: SuppressionReason;
}

export interface SendResponse {
  request_id: string;
  message_id: string;
  status: MessageStatus;
}

export interface AdminConfigSummary {
  app: AdminAppSummary;
  runtime: AdminRuntimeSummary;
  defaults: AdminDefaultsSummary;
  provider_accounts: AdminProviderAccountSummary[];
  provider_channels: AdminProviderChannelSummary[];
}

export interface AdminAppSummary {
  code: string;
  name: string;
  enabled: boolean;
  default_locale: string;
  allowed_locales: string[];
  api_keys: AdminAPIKeySummary[];
  scenes: AdminSceneSummary[];
  templates: AdminTemplateSummary[];
}

export interface AdminAPIKeySummary {
  name: string;
  enabled: boolean;
}

export interface AdminSceneSummary {
  code: string;
  name: string;
  enabled: boolean;
  template: string;
  rate_limit: AdminRateLimitSummary;
  route_policy: Record<string, string[]>;
}

export interface AdminRateLimitSummary {
  same_email_per_minute: number;
  same_email_per_day: number;
  same_user_ip_per_hour: number;
  same_caller_ip_per_hour: number;
}

export interface AdminTemplateSummary {
  code: string;
  locale: string;
  enabled: boolean;
  subject: string;
  required_vars: string[];
  has_html: boolean;
  has_text: boolean;
}

export interface AdminRuntimeSummary {
  config_store: string;
  queue: string;
  rate_limiter: string;
  message_log: string;
  stats: string;
  suppression: string;
  webhooks: boolean;
}

export interface AdminDefaultsSummary {
  provider_timeout_seconds: number;
  max_attempts_per_message: number;
  retry_backoff_seconds: number[];
  memory_queue_size: number;
  worker_concurrency: number;
  idempotency_cache_size: number;
  idempotency_ttl_hours: number;
  max_request_body_bytes: number;
  max_template_var_bytes: number;
  max_context_bytes: number;
}

export interface AdminProviderAccountSummary {
  code: string;
  provider: Provider;
  enabled: boolean;
}

export interface AdminProviderChannelSummary {
  code: string;
  account: string;
  provider: Provider;
  transport: Transport;
  enabled: boolean;
  sender_domain: string;
  from_name: string;
  from: string;
  smtp?: {
    host: string;
    port: number;
  };
}
