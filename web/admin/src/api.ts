import type {
  AdminConfigSummary,
  ErrorResponse,
  HealthResponse,
  MessageAttemptsResponse,
  MessageEventsResponse,
  MessageListResponse,
  MessageStatus,
  Provider,
  ProviderEventListResponse,
  ProviderEventType,
  SendResponse,
  StatsSummaryResponse,
  SuppressionListResponse,
  SuppressionReason,
  VersionResponse
} from './types';

export class MuxMailAPIError extends Error {
  code: string;
  requestID: string;
  status: number;

  constructor(message: string, code: string, requestID: string, status: number) {
    super(message);
    this.name = 'MuxMailAPIError';
    this.code = code;
    this.requestID = requestID;
    this.status = status;
  }
}

interface RequestOptions {
  apiKey?: string;
  idempotencyKey?: string;
  body?: unknown;
  bodyText?: string;
  query?: Record<string, string | number | undefined>;
  signal?: AbortSignal;
}

export interface SendTestPayload {
  scene: string;
  to: string;
  locale?: string;
  context?: {
    user_ip?: string;
    user_id?: string;
    request_id?: string;
  };
}

export async function getHealth(signal?: AbortSignal): Promise<HealthResponse> {
  return request<HealthResponse>('/healthz', { signal });
}

export async function getReady(signal?: AbortSignal): Promise<HealthResponse> {
  return request<HealthResponse>('/readyz', { signal });
}

export async function getVersion(signal?: AbortSignal): Promise<VersionResponse> {
  return request<VersionResponse>('/version', { signal });
}

export async function getAdminConfigSummary(apiKey: string, signal?: AbortSignal): Promise<AdminConfigSummary> {
  return request<AdminConfigSummary>('/v1/admin/config-summary', { apiKey, signal });
}

export async function getStatsSummary(apiKey: string, window: string, signal?: AbortSignal): Promise<StatsSummaryResponse> {
  return request<StatsSummaryResponse>('/v1/stats/summary', {
    apiKey,
    query: { window },
    signal
  });
}

export async function getMessages(
  apiKey: string,
  filters: { limit?: number; status?: MessageStatus | ''; scene?: string },
  signal?: AbortSignal
): Promise<MessageListResponse> {
  return request<MessageListResponse>('/v1/mail/messages', {
    apiKey,
    query: {
      limit: filters.limit,
      status: filters.status || undefined,
      scene: filters.scene || undefined
    },
    signal
  });
}

export async function getFailedMessages(
  apiKey: string,
  filters: { limit?: number; scene?: string },
  signal?: AbortSignal
): Promise<MessageListResponse> {
  return request<MessageListResponse>('/v1/mail/messages/failed', {
    apiKey,
    query: {
      limit: filters.limit,
      scene: filters.scene || undefined
    },
    signal
  });
}

export async function getMessageAttempts(
  apiKey: string,
  messageID: string,
  signal?: AbortSignal
): Promise<MessageAttemptsResponse> {
  return request<MessageAttemptsResponse>(`/v1/mail/messages/${encodeURIComponent(messageID)}/attempts`, {
    apiKey,
    signal
  });
}

export async function getMessageEvents(
  apiKey: string,
  messageID: string,
  signal?: AbortSignal
): Promise<MessageEventsResponse> {
  return request<MessageEventsResponse>(`/v1/mail/messages/${encodeURIComponent(messageID)}/events`, {
    apiKey,
    signal
  });
}

export async function getProviderEvents(
  apiKey: string,
  filters: { limit?: number; provider?: Provider | ''; eventType?: ProviderEventType | '' },
  signal?: AbortSignal
): Promise<ProviderEventListResponse> {
  return request<ProviderEventListResponse>('/v1/provider-events', {
    apiKey,
    query: {
      limit: filters.limit,
      provider: filters.provider || undefined,
      event_type: filters.eventType || undefined
    },
    signal
  });
}

export async function getSuppressions(
  apiKey: string,
  filters: { limit?: number; reason?: SuppressionReason | ''; email?: string },
  signal?: AbortSignal
): Promise<SuppressionListResponse> {
  return request<SuppressionListResponse>('/v1/suppressions', {
    apiKey,
    query: {
      limit: filters.limit,
      reason: filters.reason || undefined,
      email: filters.email || undefined
    },
    signal
  });
}

export async function sendTestMessage(
  apiKey: string,
  idempotencyKey: string,
  payload: SendTestPayload,
  varsJSON: string,
  signal?: AbortSignal
): Promise<SendResponse> {
  return request<SendResponse>('/v1/mail/send', {
    apiKey,
    idempotencyKey,
    bodyText: buildSendTestBody(payload, varsJSON),
    signal
  });
}

function buildSendTestBody(payload: SendTestPayload, varsJSON: string): string {
  const fields = [
    `"scene":${JSON.stringify(payload.scene)}`,
    `"to":${JSON.stringify(payload.to)}`,
    `"vars":${varsJSON || '{}'}`
  ];
  if (payload.locale) {
    fields.push(`"locale":${JSON.stringify(payload.locale)}`);
  }
  if (payload.context) {
    fields.push(`"context":${JSON.stringify(payload.context)}`);
  }

  return `{${fields.join(',')}}`;
}

async function request<T>(path: string, options: RequestOptions = {}): Promise<T> {
  const headers = new Headers();
  if (options.apiKey) {
    headers.set('Authorization', `Bearer ${options.apiKey}`);
  }
  if (options.idempotencyKey) {
    headers.set('Idempotency-Key', options.idempotencyKey);
  }

  const init: RequestInit = {
    headers,
    signal: options.signal,
    cache: 'no-store',
    credentials: 'omit'
  };
  if (options.bodyText !== undefined) {
    headers.set('Content-Type', 'application/json');
    init.method = 'POST';
    init.body = options.bodyText;
  } else if (options.body !== undefined) {
    headers.set('Content-Type', 'application/json');
    init.method = 'POST';
    init.body = JSON.stringify(options.body);
  }

  const url = new URL(path, window.location.origin);
  if (options.query) {
    for (const [key, value] of Object.entries(options.query)) {
      if (value !== undefined && value !== '') {
        url.searchParams.set(key, String(value));
      }
    }
  }

  const response = await fetch(url, init);
  const text = await response.text();
  const data = parseJSON(text);

  if (!response.ok) {
    const error = parseErrorResponse(data);
    throw new MuxMailAPIError(
      error?.error.message || response.statusText,
      error?.error.code || 'http_error',
      error?.error.request_id || '',
      response.status
    );
  }

  return data as T;
}

function parseErrorResponse(data: unknown): ErrorResponse | undefined {
  if (!data || typeof data !== 'object') {
    return undefined;
  }
  const maybeError = (data as { error?: unknown }).error;
  if (!maybeError || typeof maybeError !== 'object') {
    return undefined;
  }
  const payload = maybeError as Partial<ErrorResponse['error']>;
  if (typeof payload.message !== 'string' || typeof payload.code !== 'string') {
    return undefined;
  }

  return {
    error: {
      code: payload.code,
      message: payload.message,
      request_id: typeof payload.request_id === 'string' ? payload.request_id : ''
    }
  };
}

function parseJSON(text: string): unknown {
  if (!text) {
    return undefined;
  }
  try {
    return JSON.parse(text);
  } catch {
    return undefined;
  }
}
