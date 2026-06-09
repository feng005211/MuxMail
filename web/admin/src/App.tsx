import {
  Alert,
  Badge,
  Button,
  Card,
  Col,
  ConfigProvider,
  Descriptions,
  Drawer,
  Empty,
  Form,
  Grid,
  Input,
  InputNumber,
  Layout,
  Menu,
  Modal,
  Result,
  Row,
  Select,
  Space,
  Statistic,
  Table,
  Tabs,
  Tag,
  Tooltip,
  Typography,
  message as antdMessage
} from 'antd';
import type { ColumnsType } from 'antd/es/table';
import enUS from 'antd/locale/en_US';
import zhCN from 'antd/locale/zh_CN';
import {
  ApiOutlined,
  BarChartOutlined,
  CheckCircleOutlined,
  CloudServerOutlined,
  DatabaseOutlined,
  DisconnectOutlined,
  FileTextOutlined,
  GlobalOutlined,
  MailOutlined,
  ReloadOutlined,
  SafetyCertificateOutlined,
  SendOutlined,
  SettingOutlined
} from '@ant-design/icons';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import dayjs from 'dayjs';
import { useEffect, useRef, useState } from 'react';
import { Bar, BarChart, CartesianGrid, ResponsiveContainer, Tooltip as ChartTooltip, XAxis, YAxis } from 'recharts';
import {
  MuxMailAPIError,
  getAdminConfigSummary,
  getFailedMessages,
  getHealth,
  getMessageAttempts,
  getMessageEvents,
  getMessages,
  getProviderEvents,
  getReady,
  getStatsSummary,
  getSuppressions,
  getVersion,
  sendTestMessage
} from './api';
import { apiErrorMessages, dictionaries, localeStorageKey, type TranslationKey } from './i18n';
import { readLocalStorage, writeLocalStorage } from './storage';
import type {
  AdminConfigSummary,
  Locale,
  MessageAttempt,
  MessageEvent,
  MessageSnapshot,
  MessageStatus,
  Provider,
  ProviderDuration,
  ProviderEventListEntry,
  ProviderEventType,
  StatsSummaryResponse,
  SuppressionEntry,
  SuppressionReason
} from './types';

const { Header, Sider, Content } = Layout;
const { Text, Title } = Typography;

const navStorageKey = 'muxmail.admin.nav';
const adminTheme = {
  token: {
    colorPrimary: '#1677ff',
    borderRadius: 6,
    fontFamily:
      '-apple-system, BlinkMacSystemFont, "Segoe UI", Roboto, "Helvetica Neue", Arial, sans-serif'
  },
  components: {
    Layout: {
      siderBg: '#101820',
      triggerBg: '#101820'
    }
  }
};

type NavKey = 'dashboard' | 'messages' | 'events' | 'suppressions' | 'send' | 'config';
const navKeys: NavKey[] = ['dashboard', 'messages', 'events', 'suppressions', 'send', 'config'];
const defaultMessageFilters: MessageFilters = {
  limit: 50,
  status: '',
  scene: '',
  failedOnly: false
};
const defaultSuppressionFilters: SuppressionFilters = {
  limit: 50,
  reason: '',
  email: ''
};
const defaultProviderEventFilters: ProviderEventFilters = {
  limit: 50,
  provider: '',
  eventType: ''
};
const defaultMaxTemplateVarBytes = 8192;
const defaultMaxContextBytes = 4096;
const maxContextStringBytes = 256;
const appScopedQueryKeys = [
  ['admin-config'],
  ['stats'],
  ['messages'],
  ['suppressions'],
  ['provider-events'],
  ['attempts'],
  ['events']
];

interface AppProps {
  initialLocale: Locale;
}

interface MessageFilters {
  limit: number;
  status: MessageStatus | '';
  scene: string;
  failedOnly: boolean;
}

interface SuppressionFilters {
  limit: number;
  reason: SuppressionReason | '';
  email: string;
}

interface ProviderEventFilters {
  limit: number;
  provider: Provider | '';
  eventType: ProviderEventType | '';
}

interface SendTestFormValues {
  scene: string;
  to: string;
  locale?: string;
  vars?: string;
  idempotency_key: string;
  business_request_id?: string;
  user_ip?: string;
  user_id?: string;
}

interface SendTestContext {
  user_ip?: string;
  user_id?: string;
  request_id?: string;
}

const statusColors: Record<string, string> = {
  queued: 'blue',
  sending: 'geekblue',
  retrying: 'gold',
  sent: 'green',
  failed: 'red',
  delivered: 'green',
  bounced: 'volcano',
  complained: 'magenta'
};

const eventColors: Record<string, string> = {
  delivered: 'green',
  bounced: 'volcano',
  complained: 'magenta'
};

const metricLabels: Record<string, string> = {
  messages_queued: 'Messages queued',
  messages_sent: 'Messages sent',
  messages_failed: 'Messages failed',
  requests_rate_limited: 'Rate limited',
  requests_queue_full: 'Queue full',
  requests_idempotent_replay: 'Idempotent replay',
  attempts_sent: 'Provider sent',
  attempts_failed: 'Provider failed',
  provider_events_delivered: 'Delivered events',
  provider_events_bounced: 'Bounce events',
  provider_events_complained: 'Complaint events'
};

function App({ initialLocale }: AppProps) {
  const [locale, setLocale] = useState<Locale>(initialLocale);
  const dict = dictionaries[locale];
  const t = (key: TranslationKey) => dict[key];
  const [apiKey, setAPIKey] = useState('');
  const [draftAPIKey, setDraftAPIKey] = useState('');
  const [connectionID, setConnectionID] = useState(0);
  const [nav, setNav] = useState<NavKey>(() => normalizeNavKey(readLocalStorage(navStorageKey)));
  const [messageFilters, setMessageFilters] = useState<MessageFilters>(defaultMessageFilters);
  const [suppressionFilters, setSuppressionFilters] = useState<SuppressionFilters>(defaultSuppressionFilters);
  const [providerEventFilters, setProviderEventFilters] = useState<ProviderEventFilters>(defaultProviderEventFilters);
  const [statsWindow, setStatsWindow] = useState('24h');
  const [selectedMessage, setSelectedMessage] = useState<MessageSnapshot | null>(null);
  const [authorizationLost, setAuthorizationLost] = useState(false);
  const queryClient = useQueryClient();
  const screens = Grid.useBreakpoint();
  const compact = !screens.lg;
  const formatError = (error: unknown) => errorMessage(error, locale);

  const healthQuery = useQuery({ queryKey: ['health'], queryFn: ({ signal }) => getHealth(signal) });
  const readyQuery = useQuery({ queryKey: ['ready'], queryFn: ({ signal }) => getReady(signal) });
  const versionQuery = useQuery({ queryKey: ['version'], queryFn: ({ signal }) => getVersion(signal) });
  const configQuery = useQuery({
    queryKey: ['admin-config', connectionID],
    queryFn: ({ signal }) => getAdminConfigSummary(apiKey, signal),
    enabled: Boolean(apiKey && !authorizationLost)
  });
  const configAuthError = authorizationLost || isAuthorizationFailure(configQuery.error);
  const canQueryAppData = Boolean(apiKey && !configAuthError && configQuery.data);
  const querySelectedMessage = canQueryAppData ? selectedMessage : null;
  const statsQuery = useQuery({
    queryKey: ['stats', connectionID, statsWindow],
    queryFn: ({ signal }) => getStatsSummary(apiKey, statsWindow, signal),
    enabled: canQueryAppData
  });
  const messagesQuery = useQuery({
    queryKey: ['messages', connectionID, messageFilters],
    queryFn: ({ signal }) =>
      messageFilters.failedOnly
        ? getFailedMessages(apiKey, messageFilters, signal)
        : getMessages(apiKey, messageFilters, signal),
    enabled: canQueryAppData
  });
  const suppressionsQuery = useQuery({
    queryKey: ['suppressions', connectionID, suppressionFilters],
    queryFn: ({ signal }) => getSuppressions(apiKey, suppressionFilters, signal),
    enabled: canQueryAppData
  });
  const providerEventsQuery = useQuery({
    queryKey: ['provider-events', connectionID, providerEventFilters],
    queryFn: ({ signal }) => getProviderEvents(apiKey, providerEventFilters, signal),
    enabled: canQueryAppData
  });
  const attemptsQuery = useQuery({
    queryKey: ['attempts', connectionID, querySelectedMessage?.message_id],
    queryFn: ({ signal }) => getMessageAttempts(apiKey, querySelectedMessage?.message_id || '', signal),
    enabled: Boolean(querySelectedMessage)
  });
  const eventsQuery = useQuery({
    queryKey: ['events', connectionID, querySelectedMessage?.message_id],
    queryFn: ({ signal }) => getMessageEvents(apiKey, querySelectedMessage?.message_id || '', signal),
    enabled: Boolean(querySelectedMessage)
  });
  const scopedAuthError = [statsQuery.error, messagesQuery.error, suppressionsQuery.error, providerEventsQuery.error, attemptsQuery.error, eventsQuery.error].some(
    isAuthorizationFailure
  );
  const authError = configAuthError || scopedAuthError;
  const hasAdminConfig = canQueryAppData && !scopedAuthError;
  const activeSelectedMessage = hasAdminConfig ? selectedMessage : null;

  const clearAppScopedQueries = async () => {
    await Promise.all(appScopedQueryKeys.map((queryKey) => queryClient.cancelQueries({ queryKey })));
    for (const queryKey of appScopedQueryKeys) {
      queryClient.removeQueries({ queryKey });
    }
  };

  const connect = async () => {
    const trimmed = draftAPIKey.trim();
    if (!trimmed) {
      antdMessage.error(t('apiKeyRequired'));
      return;
    }
    setSelectedMessage(null);
    setMessageFilters(defaultMessageFilters);
    setSuppressionFilters(defaultSuppressionFilters);
    setProviderEventFilters(defaultProviderEventFilters);
    setStatsWindow('24h');
    setAuthorizationLost(false);
    await clearAppScopedQueries();
    setAPIKey(trimmed);
    setConnectionID((value) => value + 1);
    setDraftAPIKey('');
  };

  const disconnect = async () => {
    setAPIKey('');
    setDraftAPIKey('');
    setConnectionID((value) => value + 1);
    setSelectedMessage(null);
    setAuthorizationLost(false);
    await queryClient.cancelQueries();
    queryClient.removeQueries();
  };

  const changeLocale = (next: Locale) => {
    writeLocalStorage(localeStorageKey, next);
    setLocale(next);
  };

  const changeNav = (key: NavKey) => {
    writeLocalStorage(navStorageKey, key);
    setNav(key);
  };

  const refreshActive = () => {
    queryClient.invalidateQueries();
  };

  const configError = Boolean(apiKey && configQuery.error && !authError && !configQuery.data);
  const configBackgroundError = Boolean(apiKey && configQuery.error && !authError && configQuery.data);
  const activeError: unknown =
    nav === 'dashboard'
      ? (authError ? undefined : statsQuery.error)
      : nav === 'messages'
        ? (authError ? undefined : messagesQuery.error)
        : nav === 'suppressions'
          ? (authError ? undefined : suppressionsQuery.error)
          : nav === 'events'
            ? (authError ? undefined : providerEventsQuery.error)
            : undefined;

  useEffect(() => {
    if (!authError) {
      return;
    }
    setAuthorizationLost(true);
    setSelectedMessage(null);
    void Promise.all(appScopedQueryKeys.map((queryKey) => queryClient.cancelQueries({ queryKey }))).then(() => {
      for (const queryKey of appScopedQueryKeys) {
        queryClient.removeQueries({ queryKey });
      }
    });
  }, [authError, queryClient]);

  const content = !apiKey ? (
    <ConnectPanel
      t={t}
      apiKey={draftAPIKey}
      onAPIKeyChange={setDraftAPIKey}
      onConnect={connect}
    />
  ) : configQuery.isLoading || (configQuery.isFetching && !configQuery.data) ? (
    <Result status="info" title={t('connecting')} />
  ) : authError ? (
    <Result
      status="warning"
      title={t('unauthorized')}
      extra={
        <Button icon={<DisconnectOutlined />} onClick={disconnect}>
          {t('disconnect')}
        </Button>
      }
    />
  ) : configError ? (
    <Result
      status="error"
      title={t('configLoadFailed')}
      subTitle={formatError(configQuery.error)}
      extra={
        <Button icon={<DisconnectOutlined />} onClick={disconnect}>
          {t('disconnect')}
        </Button>
      }
    />
  ) : (
    <>
      {configBackgroundError && (
        <Alert
          showIcon
          type="error"
          message={t('configLoadFailed')}
          description={formatError(configQuery.error)}
          className="view-error"
        />
      )}
      {activeError && (
        <Alert
          showIcon
          type="error"
          message={t('error')}
          description={formatError(activeError)}
          className="view-error"
        />
      )}
      {nav === 'dashboard' && (
        <DashboardView
          t={t}
          health={healthQuery.data}
          ready={readyQuery.data}
          version={versionQuery.data}
          config={configQuery.data}
          stats={statsQuery.data}
          statsWindow={statsWindow}
          onStatsWindowChange={setStatsWindow}
          loading={configQuery.isLoading || statsQuery.isLoading}
        />
      )}
      {nav === 'messages' && (
        <MessagesView
          t={t}
          filters={messageFilters}
          scenes={configQuery.data?.app.scenes.map((scene) => scene.code) || []}
          messages={messagesQuery.data?.messages || []}
          loading={messagesQuery.isLoading}
          onFiltersChange={setMessageFilters}
          onSelect={setSelectedMessage}
        />
      )}
      {nav === 'suppressions' && (
        <SuppressionsView
          t={t}
          filters={suppressionFilters}
          suppressions={suppressionsQuery.data?.suppressions || []}
          loading={suppressionsQuery.isLoading}
          onFiltersChange={setSuppressionFilters}
        />
      )}
      {nav === 'events' && (
        <ProviderEventsView
          t={t}
          filters={providerEventFilters}
          events={providerEventsQuery.data?.events || []}
          loading={providerEventsQuery.isLoading}
          onFiltersChange={setProviderEventFilters}
        />
      )}
      {nav === 'send' && (
        <SendTestView
          t={t}
          apiKey={apiKey}
          config={configQuery.data}
          formatError={formatError}
          onUnauthorized={() => setAuthorizationLost(true)}
          onSent={() => {
            queryClient.invalidateQueries({ queryKey: ['messages'] });
            queryClient.invalidateQueries({ queryKey: ['stats'] });
          }}
        />
      )}
      {nav === 'config' && <ConfigurationView t={t} config={configQuery.data} loading={configQuery.isLoading} />}
    </>
  );

  return (
    <ConfigProvider locale={locale === 'zh-CN' ? zhCN : enUS} theme={adminTheme}>
      <Layout className="app-shell">
        <Sider
          breakpoint="lg"
          collapsedWidth={compact ? 0 : 64}
          width={232}
          className="app-sider"
        >
          <div className="brand">
            <MailOutlined />
            <span>{t('appTitle')}</span>
          </div>
          <Menu
            theme="dark"
            mode="inline"
            selectedKeys={[nav]}
            onClick={({ key }) => changeNav(key as NavKey)}
            items={[
              { key: 'dashboard', icon: <BarChartOutlined />, label: t('dashboard') },
              { key: 'messages', icon: <FileTextOutlined />, label: t('messages') },
              { key: 'events', icon: <CloudServerOutlined />, label: t('events') },
              { key: 'suppressions', icon: <SafetyCertificateOutlined />, label: t('suppressions') },
              { key: 'send', icon: <SendOutlined />, label: t('sendTest') },
              { key: 'config', icon: <SettingOutlined />, label: t('configuration') }
            ]}
          />
        </Sider>
        <Layout>
          <Header className="app-header">
            <Space size="middle" wrap>
              <Badge
                status={hasAdminConfig ? 'success' : 'default'}
                text={hasAdminConfig ? `${t('connected')}: ${configQuery.data?.app.code}` : t('disconnected')}
              />
              <Select
                aria-label={t('language')}
                value={locale}
                onChange={changeLocale}
                options={[
                  { value: 'en-US', label: 'EN' },
                  { value: 'zh-CN', label: '中文' }
                ]}
                size="small"
                suffixIcon={<GlobalOutlined />}
              />
              <Tooltip title={t('refresh')}>
                <Button icon={<ReloadOutlined />} onClick={refreshActive} />
              </Tooltip>
              {apiKey && (
                <Button icon={<DisconnectOutlined />} onClick={disconnect}>
                  {t('disconnect')}
                </Button>
              )}
            </Space>
          </Header>
          <Content className="app-content">{content}</Content>
        </Layout>
        <MessageDrawer
          t={t}
          message={activeSelectedMessage}
          attempts={activeSelectedMessage ? attemptsQuery.data?.attempts || [] : []}
          events={activeSelectedMessage ? eventsQuery.data?.events || [] : []}
          attemptsError={attemptsQuery.error}
          eventsError={eventsQuery.error}
          loading={Boolean(activeSelectedMessage) && (attemptsQuery.isLoading || eventsQuery.isLoading)}
          formatError={formatError}
          onClose={() => setSelectedMessage(null)}
        />
      </Layout>
    </ConfigProvider>
  );
}

interface TranslationProps {
  t: (key: TranslationKey) => string;
}

interface ConnectPanelProps extends TranslationProps {
  apiKey: string;
  onAPIKeyChange: (value: string) => void;
  onConnect: () => void;
}

function ConnectPanel({ t, apiKey, onAPIKeyChange, onConnect }: ConnectPanelProps) {
  return (
    <div className="connect-panel">
      <Card className="connect-card">
        <Space direction="vertical" size="middle" className="full-width">
          <Title level={3}>{t('appTitle')}</Title>
          <Input.Password
            value={apiKey}
            onChange={(event) => onAPIKeyChange(event.target.value)}
            placeholder={t('keyPlaceholder')}
            onPressEnter={onConnect}
            autoComplete="off"
            spellCheck={false}
          />
          <Button type="primary" icon={<ApiOutlined />} onClick={onConnect} block>
            {t('connect')}
          </Button>
        </Space>
      </Card>
    </div>
  );
}

interface DashboardViewProps extends TranslationProps {
  health?: { status: string };
  ready?: { status: string };
  version?: { version: string };
  config?: AdminConfigSummary;
  stats?: StatsSummaryResponse;
  statsWindow: string;
  onStatsWindowChange: (value: string) => void;
  loading: boolean;
}

function DashboardView({
  t,
  health,
  ready,
  version,
  config,
  stats,
  statsWindow,
  onStatsWindowChange,
  loading
}: DashboardViewProps) {
  const metrics = metricRows(stats);
  return (
    <Space direction="vertical" size="middle" className="full-width">
      <Row gutter={[16, 16]}>
        <Col xs={24} sm={12} xl={6}>
          <Card loading={loading}>
            <Statistic
              title={t('healthy')}
              value={health?.status || '-'}
              prefix={<CheckCircleOutlined />}
            />
          </Card>
        </Col>
        <Col xs={24} sm={12} xl={6}>
          <Card loading={loading}>
            <Statistic title={t('ready')} value={ready?.status || '-'} prefix={<CloudServerOutlined />} />
          </Card>
        </Col>
        <Col xs={24} sm={12} xl={6}>
          <Card loading={loading}>
            <Statistic title={t('version')} value={version?.version || '-'} prefix={<DatabaseOutlined />} />
          </Card>
        </Col>
        <Col xs={24} sm={12} xl={6}>
          <Card loading={loading}>
            <Statistic title={t('app')} value={config?.app.code || '-'} prefix={<ApiOutlined />} />
          </Card>
        </Col>
      </Row>
      <Card
        title={t('dashboard')}
        extra={
          <Select
            value={statsWindow}
            onChange={onStatsWindowChange}
            options={[
              { value: '1h', label: '1h' },
              { value: '24h', label: '24h' },
              { value: '7d', label: '7d' }
            ]}
            size="small"
          />
        }
      >
        <Row gutter={[16, 16]}>
          <Col xs={24} xl={8}>
            <Table
              rowKey="metric"
              size="small"
              pagination={false}
              dataSource={metrics}
              locale={{ emptyText: <Empty description={t('noData')} /> }}
              columns={[
                { title: t('metric'), dataIndex: 'label' },
                { title: t('value'), dataIndex: 'value', align: 'right' }
              ]}
            />
          </Col>
          <Col xs={24} xl={8}>
            <MetricChart metrics={metrics} emptyText={t('noData')} />
          </Col>
          <Col xs={24} xl={8}>
            <ProviderLatencyTable t={t} durations={stats?.provider_durations || []} />
          </Col>
        </Row>
      </Card>
    </Space>
  );
}

function metricRows(stats?: StatsSummaryResponse) {
  if (!stats) {
    return [];
  }
  return Object.entries(stats.metrics)
    .sort(([left], [right]) => left.localeCompare(right))
    .map(([metric, value]) => ({
      metric,
      label: metricLabels[metric] || metric,
      value
    }));
}

interface MetricChartProps {
  metrics: Array<{ metric: string; label: string; value: number }>;
  emptyText: string;
}

function MetricChart({ metrics, emptyText }: MetricChartProps) {
  if (metrics.length === 0) {
    return <Empty description={emptyText} />;
  }

  return (
    <div className="metric-chart">
      <ResponsiveContainer width="100%" height={260}>
        <BarChart data={metrics} layout="vertical" margin={{ top: 8, right: 8, bottom: 8, left: 8 }}>
          <CartesianGrid strokeDasharray="3 3" horizontal={false} />
          <XAxis type="number" allowDecimals={false} />
          <YAxis type="category" dataKey="label" width={132} tick={{ fontSize: 12 }} />
          <ChartTooltip />
          <Bar dataKey="value" fill="#1677ff" radius={[0, 4, 4, 0]} />
        </BarChart>
      </ResponsiveContainer>
    </div>
  );
}

interface ProviderLatencyTableProps extends TranslationProps {
  durations: ProviderDuration[];
}

function ProviderLatencyTable({ t, durations }: ProviderLatencyTableProps) {
  return (
    <Table
      rowKey="provider_channel"
      size="small"
      pagination={false}
      dataSource={durations}
      locale={{ emptyText: <Empty description={t('noData')} /> }}
      columns={[
        { title: t('channel'), dataIndex: 'provider_channel' },
        { title: t('transport'), dataIndex: 'transport', width: 96 },
        { title: t('attempts'), dataIndex: 'count', align: 'right', width: 96 },
        {
          title: t('duration'),
          dataIndex: 'average_ms',
          align: 'right',
          width: 120,
          render: (value: number) => `${Math.round(value)} ms`
        }
      ]}
    />
  );
}

interface MessagesViewProps extends TranslationProps {
  filters: MessageFilters;
  scenes: string[];
  messages: MessageSnapshot[];
  loading: boolean;
  onFiltersChange: (value: MessageFilters) => void;
  onSelect: (value: MessageSnapshot) => void;
}

function MessagesView({ t, filters, scenes, messages, loading, onFiltersChange, onSelect }: MessagesViewProps) {
  const columns: ColumnsType<MessageSnapshot> = [
    {
      title: t('status'),
      dataIndex: 'status',
      width: 116,
      render: (status: MessageStatus) => <Tag color={statusColors[status]}>{status}</Tag>
    },
    {
      title: t('messageID'),
      dataIndex: 'message_id',
      ellipsis: true,
      render: (value: string, record) => (
        <Button type="link" onClick={() => onSelect(record)} className="link-button">
          {value}
        </Button>
      )
    },
    { title: t('scene'), dataIndex: 'scene', width: 160 },
    { title: t('locale'), dataIndex: 'locale', width: 100 },
    { title: t('toDomain'), dataIndex: 'to_domain', width: 160 },
    {
      title: t('updatedAt'),
      dataIndex: 'updated_at',
      width: 180,
      render: formatTime
    },
    {
      title: t('error'),
      dataIndex: 'error_code',
      width: 180,
      render: (_value: string, record) =>
        record.error_code ? <Tooltip title={record.error_message}>{record.error_code}</Tooltip> : '-'
    }
  ];

  return (
    <Card
      title={t('messages')}
      extra={
        <Space wrap>
          <Select
            value={filters.failedOnly ? 'failed' : 'all'}
            onChange={(value) => onFiltersChange({ ...filters, failedOnly: value === 'failed', status: '' })}
            options={[
              { value: 'all', label: t('all') },
              { value: 'failed', label: t('failedOnly') }
            ]}
            className="filter-select"
          />
          {!filters.failedOnly && (
            <Select
              value={filters.status}
              onChange={(value) => onFiltersChange({ ...filters, status: value })}
              options={[
                { value: '', label: t('all') },
                ...['queued', 'sending', 'retrying', 'sent', 'failed', 'delivered', 'bounced', 'complained'].map(
                  (status) => ({ value: status, label: status })
                )
              ]}
              className="filter-select"
            />
          )}
          <Select
            value={filters.scene}
            onChange={(value) => onFiltersChange({ ...filters, scene: value })}
            options={[{ value: '', label: t('all') }, ...scenes.map((scene) => ({ value: scene, label: scene }))]}
            className="filter-select"
          />
          <InputNumber
            min={1}
            max={200}
            value={filters.limit}
            onChange={(value) => onFiltersChange({ ...filters, limit: value || 50 })}
            className="small-number"
          />
        </Space>
      }
    >
      <Table
        rowKey="message_id"
        size="small"
        loading={loading}
        columns={columns}
        dataSource={messages}
        scroll={{ x: 980 }}
        pagination={{ pageSize: 25, showSizeChanger: false }}
      />
    </Card>
  );
}

interface MessageDrawerProps extends TranslationProps {
  message: MessageSnapshot | null;
  attempts: MessageAttempt[];
  events: MessageEvent[];
  attemptsError: unknown;
  eventsError: unknown;
  loading: boolean;
  formatError: (error: unknown) => string;
  onClose: () => void;
}

function MessageDrawer({ t, message, attempts, events, attemptsError, eventsError, loading, formatError, onClose }: MessageDrawerProps) {
  const attemptsErrorMessage = attemptsError ? formatError(attemptsError) : '';
  const eventsErrorMessage = eventsError ? formatError(eventsError) : '';

  return (
    <Drawer
      title={message?.message_id || t('details')}
      open={Boolean(message)}
      onClose={onClose}
      width={720}
    >
      {message && (
        <Space direction="vertical" size="middle" className="full-width">
          <Descriptions size="small" bordered column={1}>
            <Descriptions.Item label={t('status')}>
              <Tag color={statusColors[message.status]}>{message.status}</Tag>
            </Descriptions.Item>
            <Descriptions.Item label={t('requestID')}>{message.request_id}</Descriptions.Item>
            <Descriptions.Item label={t('scene')}>{message.scene}</Descriptions.Item>
            <Descriptions.Item label={t('toDomain')}>{message.to_domain}</Descriptions.Item>
            <Descriptions.Item label={t('updatedAt')}>{formatTime(message.updated_at)}</Descriptions.Item>
            {message.error_code && (
              <Descriptions.Item label={t('error')}>
                {message.error_code} {message.error_message ? `- ${message.error_message}` : ''}
              </Descriptions.Item>
            )}
          </Descriptions>
          <Tabs
            items={[
              {
                key: 'attempts',
                label: t('attempts'),
                children: (
                  <Space direction="vertical" size="small" className="full-width">
                    {attemptsErrorMessage && (
                      <Alert showIcon type="error" message={`${t('attempts')}: ${t('error')}`} description={attemptsErrorMessage} />
                    )}
                    <Table
                      rowKey={(record) =>
                        JSON.stringify([
                          record.attempt_no,
                          record.status,
                          record.provider,
                          record.provider_account,
                          record.provider_channel,
                          record.provider_message_id || '',
                          record.logged_at
                        ])
                      }
                      size="small"
                      loading={loading}
                      pagination={false}
                      dataSource={attempts}
                      scroll={{ x: 900 }}
                      columns={[
                        { title: '#', dataIndex: 'attempt_no', width: 56 },
                        {
                          title: t('status'),
                          dataIndex: 'status',
                          width: 104,
                          render: (status: string) => <Tag color={statusColors[status]}>{status}</Tag>
                        },
                        { title: t('provider'), dataIndex: 'provider', width: 112, render: emptyText },
                        { title: t('channel'), dataIndex: 'provider_channel', render: emptyText },
                        { title: t('transport'), dataIndex: 'transport', width: 96, render: emptyText },
                        {
                          title: t('error'),
                          dataIndex: 'error_code',
                          width: 156,
                          render: (_: string, record: MessageAttempt) =>
                            record.error_code ? <Tooltip title={record.error_message || record.error_code}>{record.error_code}</Tooltip> : '-'
                        },
                        {
                          title: t('duration'),
                          dataIndex: 'duration_ms',
                          width: 108,
                          align: 'right',
                          render: (value: number) => `${value} ms`
                        }
                      ]}
                    />
                  </Space>
                )
              },
              {
                key: 'events',
                label: t('events'),
                children: (
                  <Space direction="vertical" size="small" className="full-width">
                    {eventsErrorMessage && (
                      <Alert showIcon type="error" message={`${t('events')}: ${t('error')}`} description={eventsErrorMessage} />
                    )}
                    <Table
                      rowKey={(record) =>
                        JSON.stringify([
                          record.provider,
                          record.provider_account,
                          record.provider_channel,
                          record.provider_message_id,
                          record.event_type,
                          record.occurred_at,
                          record.logged_at
                        ])
                      }
                      size="small"
                      loading={loading}
                      pagination={false}
                      dataSource={events}
                      scroll={{ x: 720 }}
                      columns={[
                        {
                          title: t('status'),
                          dataIndex: 'event_type',
                          width: 120,
                          render: (value: string) => <Tag color={eventColors[value]}>{value}</Tag>
                        },
                        { title: t('provider'), dataIndex: 'provider', width: 112 },
                        { title: t('channel'), dataIndex: 'provider_channel' },
                        { title: t('occurredAt'), dataIndex: 'occurred_at', width: 180, render: formatTime }
                      ]}
                    />
                  </Space>
                )
              }
            ]}
          />
        </Space>
      )}
    </Drawer>
  );
}

interface SuppressionsViewProps extends TranslationProps {
  filters: SuppressionFilters;
  suppressions: SuppressionEntry[];
  loading: boolean;
  onFiltersChange: (value: SuppressionFilters) => void;
}

function SuppressionsView({ t, filters, suppressions, loading, onFiltersChange }: SuppressionsViewProps) {
  return (
    <Card
      title={t('suppressions')}
      extra={
        <Space wrap>
          <Input
            value={filters.email}
            onChange={(event) => onFiltersChange({ ...filters, email: event.target.value })}
            placeholder={t('email')}
            className="email-filter"
          />
          <Select
            value={filters.reason}
            onChange={(value) => onFiltersChange({ ...filters, reason: value })}
            options={[
              { value: '', label: t('all') },
              { value: 'hard_bounce', label: 'hard_bounce' },
              { value: 'complaint', label: 'complaint' },
              { value: 'manual', label: 'manual' }
            ]}
            className="filter-select"
          />
          <InputNumber
            min={1}
            max={200}
            value={filters.limit}
            onChange={(value) => onFiltersChange({ ...filters, limit: value || 50 })}
            className="small-number"
          />
        </Space>
      }
    >
      <Table
        rowKey="normalized_email"
        size="small"
        loading={loading}
        dataSource={suppressions}
        columns={[
          { title: t('email'), dataIndex: 'email' },
          { title: 'Normalized', dataIndex: 'normalized_email' },
          {
            title: t('reason'),
            dataIndex: 'reason',
            width: 160,
            render: (reason: string) => <Tag>{reason}</Tag>
          }
        ]}
      />
    </Card>
  );
}

interface ProviderEventsViewProps extends TranslationProps {
  filters: ProviderEventFilters;
  events: ProviderEventListEntry[];
  loading: boolean;
  onFiltersChange: (value: ProviderEventFilters) => void;
}

function ProviderEventsView({ t, filters, events, loading, onFiltersChange }: ProviderEventsViewProps) {
  return (
    <Card
      title={t('events')}
      extra={
        <Space wrap>
          <Select
            value={filters.provider}
            onChange={(value) => onFiltersChange({ ...filters, provider: value })}
            options={[
              { value: '', label: t('all') },
              { value: 'mock', label: 'mock' },
              { value: 'resend', label: 'resend' },
              { value: 'brevo', label: 'brevo' }
            ]}
            className="filter-select"
          />
          <Select
            value={filters.eventType}
            onChange={(value) => onFiltersChange({ ...filters, eventType: value })}
            options={[
              { value: '', label: t('all') },
              { value: 'delivered', label: 'delivered' },
              { value: 'bounced', label: 'bounced' },
              { value: 'complained', label: 'complained' }
            ]}
            className="filter-select"
          />
          <InputNumber
            min={1}
            max={200}
            value={filters.limit}
            onChange={(value) => onFiltersChange({ ...filters, limit: value || 50 })}
            className="small-number"
          />
        </Space>
      }
    >
      <Table
        rowKey={(record) =>
          JSON.stringify([
            record.message_id,
            record.provider,
            record.provider_account,
            record.provider_channel,
            record.provider_message_id,
            record.event_type,
            record.occurred_at,
            record.logged_at
          ])
        }
        size="small"
        loading={loading}
        dataSource={events}
        scroll={{ x: 1120 }}
        pagination={{ pageSize: 25, showSizeChanger: false }}
        columns={[
          {
            title: t('eventType'),
            dataIndex: 'event_type',
            width: 132,
            render: (value: string) => <Tag color={eventColors[value]}>{value}</Tag>
          },
          { title: t('messageID'), dataIndex: 'message_id', width: 240, ellipsis: true },
          { title: t('provider'), dataIndex: 'provider', width: 112 },
          { title: t('channel'), dataIndex: 'provider_channel', width: 180 },
          { title: t('providerMessageID'), dataIndex: 'provider_message_id', width: 220, ellipsis: true },
          { title: t('occurredAt'), dataIndex: 'occurred_at', width: 180, render: formatTime },
          { title: t('loggedAt'), dataIndex: 'logged_at', width: 180, render: formatTime }
        ]}
      />
    </Card>
  );
}

interface SendTestViewProps extends TranslationProps {
  apiKey: string;
  config?: AdminConfigSummary;
  formatError: (error: unknown) => string;
  onUnauthorized: () => void;
  onSent: () => void;
}

function SendTestView({ t, apiKey, config, formatError, onUnauthorized, onSent }: SendTestViewProps) {
  const [form] = Form.useForm();
  const abortRef = useRef<AbortController | null>(null);
  const maxTemplateVarBytes = config?.defaults.max_template_var_bytes || defaultMaxTemplateVarBytes;
  const maxContextBytes = config?.defaults.max_context_bytes || defaultMaxContextBytes;
  const mutation = useMutation({
    mutationFn: async (values: SendTestFormValues) => {
      abortRef.current?.abort();
      const controller = new AbortController();
      abortRef.current = controller;
      const varsJSON = (values.vars || '{}').trim() || '{}';
      try {
        parseVariablesJSON(varsJSON, t('invalidJSON'), maxTemplateVarBytes);
        const context = buildSendTestContext(values);
        validateSendTestContextSize(context, maxContextBytes, t('invalidContext'));

        return await sendTestMessage(
          apiKey,
          values.idempotency_key,
          {
            scene: values.scene.trim(),
            to: values.to.trim(),
            locale: values.locale?.trim() || undefined,
            context
          },
          varsJSON,
          controller.signal
        );
      } finally {
        if (abortRef.current === controller) {
          abortRef.current = null;
        }
      }
    },
    onSuccess: (response) => {
      antdMessage.success(`${t('queued')}: ${response.message_id}`);
      form.setFieldValue('idempotency_key', newAdminIdempotencyKey());
      onSent();
    },
    onError: (error) => {
      if (isAbortError(error)) {
        return;
      }
      if (isAuthorizationFailure(error)) {
        onUnauthorized();
        return;
      }
      Modal.error({ title: t('error'), content: formatError(error) });
    }
  });

  const sendableScenes = (config?.app.scenes || []).filter((scene) => scene.enabled);
  const firstScene = sendableScenes[0]?.code;
  const defaultLocale = config?.app.default_locale;
  const selectedScene = Form.useWatch('scene', form) || firstScene;
  const selectedLocale = Form.useWatch('locale', form) || defaultLocale;
  const templateForSelectedScene = findTemplateForScene(config, selectedScene, selectedLocale);
  const exampleVarsJSON = buildExampleVarsJSON(templateForSelectedScene?.required_vars || []);
  const previousExampleVars = useRef('{}');

  useEffect(() => {
    return () => {
      abortRef.current?.abort();
      abortRef.current = null;
    };
  }, [apiKey]);

  useEffect(() => {
    const initialVars = buildExampleVarsJSON(findTemplateForScene(config, firstScene, defaultLocale)?.required_vars || []);
    form.setFieldsValue({
      scene: firstScene,
      locale: defaultLocale,
      vars: initialVars,
      idempotency_key: newAdminIdempotencyKey(),
      to: '',
      business_request_id: '',
      user_ip: '',
      user_id: ''
    });
    previousExampleVars.current = initialVars;
  }, [config?.app.code, defaultLocale, firstScene, form]);

  useEffect(() => {
    const currentVars = form.getFieldValue('vars');
    if (!currentVars || currentVars === '{}' || currentVars === previousExampleVars.current) {
      form.setFieldValue('vars', exampleVarsJSON);
    }
    previousExampleVars.current = exampleVarsJSON;
  }, [exampleVarsJSON, form]);

  return (
    <Card title={t('sendTest')}>
      <Form
        form={form}
        layout="vertical"
        initialValues={{
          scene: firstScene,
          locale: defaultLocale,
          vars: exampleVarsJSON,
          idempotency_key: newAdminIdempotencyKey()
        }}
        onFinish={(values) =>
          mutation.mutate({
            ...values,
            idempotency_key: values.idempotency_key.trim(),
            to: values.to.trim(),
            business_request_id: values.business_request_id?.trim(),
            user_ip: values.user_ip?.trim(),
            user_id: values.user_id?.trim()
          })
        }
      >
        <Row gutter={16}>
          <Col xs={24} md={8}>
            <Form.Item name="scene" label={t('scene')} rules={[{ required: true }]}>
              <Select options={sendableScenes.map((scene) => ({ value: scene.code, label: scene.code }))} />
            </Form.Item>
          </Col>
          <Col xs={24} md={8}>
            <Form.Item name="locale" label={t('locale')}>
              <Select options={(config?.app.allowed_locales || []).map((value) => ({ value, label: value }))} />
            </Form.Item>
          </Col>
          <Col xs={24} md={8}>
            <Form.Item
              name="idempotency_key"
              label={t('idempotencyKey')}
              normalize={trimStringValue}
              rules={[
                { required: true, whitespace: true },
                visibleASCIIWithoutWhitespaceRule(128, t('invalidIdempotencyKey'))
              ]}
            >
              <Input />
            </Form.Item>
          </Col>
          <Col xs={24} md={12}>
            <Form.Item
              name="to"
              label={t('recipient')}
              normalize={trimStringValue}
              rules={[{ required: true, whitespace: true }, addrSpecEmailRule(t('invalidRecipient'))]}
            >
              <Input />
            </Form.Item>
          </Col>
          <Col xs={24} md={12}>
            <Form.Item
              name="business_request_id"
              label={t('businessRequestID')}
              normalize={trimStringValue}
              rules={[visibleASCIIWithoutWhitespaceRule(128, t('invalidBusinessRequestID'))]}
            >
              <Input />
            </Form.Item>
          </Col>
          <Col xs={24} md={12}>
            <Form.Item name="user_ip" label={t('userIP')} normalize={trimStringValue} rules={[userIPRule(t('invalidUserIP'))]}>
              <Input />
            </Form.Item>
          </Col>
          <Col xs={24} md={12}>
            <Form.Item name="user_id" label={t('userID')} normalize={trimStringValue} rules={[byteLengthRule(maxContextStringBytes, t('invalidUserID'))]}>
              <Input />
            </Form.Item>
          </Col>
          <Col span={24}>
            <Form.Item name="vars" label={t('variablesJSON')} rules={[{ required: true }, jsonObjectRule(t('invalidJSON'), maxTemplateVarBytes)]}>
              <Input.TextArea rows={8} spellCheck={false} />
            </Form.Item>
          </Col>
        </Row>
        <Button type="primary" icon={<SendOutlined />} htmlType="submit" loading={mutation.isPending}>
          {t('submit')}
        </Button>
      </Form>
    </Card>
  );
}

interface ConfigurationViewProps extends TranslationProps {
  config?: AdminConfigSummary;
  loading: boolean;
}

function ConfigurationView({ t, config, loading }: ConfigurationViewProps) {
  if (!config && loading) {
    return <Card loading />;
  }
  if (!config) {
    return <Empty description={t('noData')} />;
  }

  const defaultRows = [
    { key: 'provider_timeout_seconds', label: t('providerTimeout'), value: `${config.defaults.provider_timeout_seconds} s` },
    { key: 'max_attempts_per_message', label: t('maxAttempts'), value: config.defaults.max_attempts_per_message },
    {
      key: 'retry_backoff_seconds',
      label: t('retryBackoff'),
      value: config.defaults.retry_backoff_seconds.map((seconds) => `${seconds}s`).join(' -> ')
    },
    { key: 'memory_queue_size', label: t('memoryQueue'), value: config.defaults.memory_queue_size },
    { key: 'worker_concurrency', label: t('workerConcurrency'), value: config.defaults.worker_concurrency },
    { key: 'idempotency_cache_size', label: t('idempotencyCache'), value: config.defaults.idempotency_cache_size },
    { key: 'idempotency_ttl_hours', label: t('idempotencyTTL'), value: `${config.defaults.idempotency_ttl_hours} h` },
    { key: 'max_request_body_bytes', label: t('requestBodyLimit'), value: `${config.defaults.max_request_body_bytes} bytes` },
    { key: 'max_template_var_bytes', label: t('templateVarLimit'), value: `${config.defaults.max_template_var_bytes} bytes` },
    { key: 'max_context_bytes', label: t('contextLimit'), value: `${config.defaults.max_context_bytes} bytes` }
  ];

  return (
    <Space direction="vertical" size="middle" className="full-width">
      <Row gutter={[16, 16]}>
        <Col xs={24} xl={12}>
          <Card title={t('app')}>
            <Descriptions size="small" bordered column={1}>
              <Descriptions.Item label="Code">{config.app.code}</Descriptions.Item>
              <Descriptions.Item label="Name">{config.app.name}</Descriptions.Item>
              <Descriptions.Item label={t('status')}>{enabledTag(t, config.app.enabled)}</Descriptions.Item>
              <Descriptions.Item label={t('locale')}>
                {config.app.default_locale} / {config.app.allowed_locales.join(', ')}
              </Descriptions.Item>
            </Descriptions>
          </Card>
        </Col>
        <Col xs={24} xl={12}>
          <Card title={t('runtime')}>
            <Descriptions size="small" bordered column={2}>
              <Descriptions.Item label="Config">{config.runtime.config_store}</Descriptions.Item>
              <Descriptions.Item label="Queue">{config.runtime.queue}</Descriptions.Item>
              <Descriptions.Item label="Rate">{config.runtime.rate_limiter}</Descriptions.Item>
              <Descriptions.Item label="Log">{config.runtime.message_log}</Descriptions.Item>
              <Descriptions.Item label="Stats">{config.runtime.stats}</Descriptions.Item>
              <Descriptions.Item label={t('webhooks')}>{String(config.runtime.webhooks)}</Descriptions.Item>
            </Descriptions>
          </Card>
        </Col>
      </Row>
      <Row gutter={[16, 16]}>
        <Col xs={24} xl={12}>
          <Card title={t('apiKeys')}>
            <Table
              rowKey="name"
              size="small"
              pagination={false}
              dataSource={config.app.api_keys}
              locale={{ emptyText: <Empty description={t('noData')} /> }}
              columns={[
                { title: t('name'), dataIndex: 'name' },
                { title: t('status'), dataIndex: 'enabled', width: 120, render: (value: boolean) => enabledTag(t, value) }
              ]}
            />
          </Card>
        </Col>
        <Col xs={24} xl={12}>
          <Card title={t('providerAccounts')}>
            <Table
              rowKey="code"
              size="small"
              pagination={false}
              dataSource={config.provider_accounts}
              locale={{ emptyText: <Empty description={t('noData')} /> }}
              columns={[
                { title: 'Code', dataIndex: 'code' },
                { title: t('provider'), dataIndex: 'provider', width: 120 },
                { title: t('status'), dataIndex: 'enabled', width: 120, render: (value: boolean) => enabledTag(t, value) }
              ]}
            />
          </Card>
        </Col>
      </Row>
      <Card title={t('defaults')}>
        <Table
          rowKey="key"
          size="small"
          pagination={false}
          dataSource={defaultRows}
          columns={[
            { title: t('name'), dataIndex: 'label', width: 260 },
            { title: t('value'), dataIndex: 'value' }
          ]}
        />
      </Card>
      <Card title={t('scene')}>
        <Table
          rowKey="code"
          size="small"
          dataSource={config.app.scenes}
          scroll={{ x: 900 }}
          columns={[
            { title: 'Code', dataIndex: 'code', width: 180 },
            { title: 'Name', dataIndex: 'name', width: 220 },
            { title: t('status'), dataIndex: 'enabled', width: 100, render: (value: boolean) => enabledTag(t, value) },
            { title: t('template'), dataIndex: 'template', width: 200 },
            {
              title: t('routePolicy'),
              dataIndex: 'route_policy',
              render: (value: Record<string, string[]>) =>
                Object.entries(value).map(([domain, channels]) => (
                  <div key={domain}>
                    <Text code>{domain}</Text> {channels.join(' -> ')}
                  </div>
                ))
            }
          ]}
        />
      </Card>
      <Card title={t('template')}>
        <Table
          rowKey={(record) => `${record.code}-${record.locale}`}
          size="small"
          dataSource={config.app.templates}
          scroll={{ x: 900 }}
          columns={[
            { title: 'Code', dataIndex: 'code', width: 180 },
            { title: t('locale'), dataIndex: 'locale', width: 100 },
            { title: t('status'), dataIndex: 'enabled', width: 100, render: (value: boolean) => enabledTag(t, value) },
            { title: t('subject'), dataIndex: 'subject' },
            {
              title: t('requiredVars'),
              dataIndex: 'required_vars',
              width: 220,
              render: (vars: string[]) => vars.map((name) => <Tag key={name}>{name}</Tag>)
            }
          ]}
        />
      </Card>
      <Card title={t('channels')}>
        <Table
          rowKey="code"
          size="small"
          dataSource={config.provider_channels}
          scroll={{ x: 1000 }}
          columns={[
            { title: 'Code', dataIndex: 'code', width: 180 },
            { title: t('provider'), dataIndex: 'provider', width: 112 },
            { title: t('transport'), dataIndex: 'transport', width: 96 },
            { title: t('status'), dataIndex: 'enabled', width: 100, render: (value: boolean) => enabledTag(t, value) },
            { title: t('senderDomain'), dataIndex: 'sender_domain', width: 180 },
            { title: t('from'), dataIndex: 'from', width: 220 },
            {
              title: t('smtpHost'),
              dataIndex: 'smtp',
              render: (smtp?: { host: string; port: number }) => (smtp ? `${smtp.host}:${smtp.port}` : '-')
            }
          ]}
        />
      </Card>
    </Space>
  );
}

function enabledTag(t: (key: TranslationKey) => string, value: boolean) {
  return value ? <Tag color="green">{t('enabled')}</Tag> : <Tag color="red">{t('disabled')}</Tag>;
}

function buildSendTestContext(values: SendTestFormValues): SendTestContext | undefined {
  if (!values.user_ip && !values.user_id && !values.business_request_id) {
    return undefined;
  }

  return {
    user_ip: values.user_ip || undefined,
    user_id: values.user_id || undefined,
    request_id: values.business_request_id || undefined
  };
}

function validateSendTestContextSize(context: SendTestContext | undefined, maxBytes: number, message: string) {
  if (!context) {
    return;
  }
  if (byteLength(JSON.stringify(context)) > maxBytes) {
    throw new Error(message);
  }
}

function trimStringValue(value: unknown) {
  return typeof value === 'string' ? value.trim() : value;
}

function visibleASCIIWithoutWhitespaceRule(maxBytes: number, message: string) {
  return {
    validator(_: unknown, value?: string) {
      if (!value) {
        return Promise.resolve();
      }
      if (value.length > maxBytes || !/^[!-~]+$/.test(value)) {
        return Promise.reject(new Error(message));
      }
      return Promise.resolve();
    }
  };
}

function userIPRule(message: string) {
  return {
    validator(_: unknown, value?: string) {
      if (!value || isValidIP(value)) {
        return Promise.resolve();
      }
      return Promise.reject(new Error(message));
    }
  };
}

function addrSpecEmailRule(message: string) {
  return {
    validator(_: unknown, value?: string) {
      if (!value || isValidAddrSpecEmail(value)) {
        return Promise.resolve();
      }
      return Promise.reject(new Error(message));
    }
  };
}

function byteLengthRule(maxBytes: number, message: string) {
  return {
    validator(_: unknown, value?: string) {
      if (!value || byteLength(value) <= maxBytes) {
        return Promise.resolve();
      }
      return Promise.reject(new Error(message));
    }
  };
}

function jsonObjectRule(message: string, maxBytes: number) {
  return {
    validator(_: unknown, value?: string) {
      if (!value) {
        return Promise.resolve();
      }
      try {
        parseVariablesJSON(value, message, maxBytes);
        return Promise.resolve();
      } catch {
        return Promise.reject(new Error(message));
      }
    }
  };
}

function parseVariablesJSON(value: string, message: string, maxBytes: number): Record<string, unknown> {
  if (byteLength(value) > maxBytes) {
    throw new Error(message);
  }

  let parsed: unknown;
  try {
    parsed = JSON.parse(replaceJSONNumbersForValidation(value)) as unknown;
  } catch {
    throw new Error(message);
  }
  if (!parsed || typeof parsed !== 'object' || Array.isArray(parsed)) {
    throw new Error(message);
  }

  const variables = parsed as Record<string, unknown>;
  const entries = Object.entries(variables);
  if (entries.length > 32) {
    throw new Error(message);
  }
  for (const [name, variableValue] of entries) {
    if (!isValidVariableName(name) || !isValidVariableValue(variableValue)) {
      throw new Error(message);
    }
  }

  return variables;
}

function replaceJSONNumbersForValidation(value: string) {
  let output = '';
  let index = 0;
  while (index < value.length) {
    const char = value[index];
    if (char === '"') {
      const end = readJSONStringEnd(value, index);
      if (end <= index) {
        return value;
      }
      output += value.slice(index, end);
      index = end;
      continue;
    }
    if (char === '-' || isDigit(char)) {
      const end = readJSONNumberEnd(value, index);
      if (end <= index) {
        return value;
      }
      output += '0';
      index = end;
      continue;
    }
    output += char;
    index += 1;
  }

  return output;
}

function readJSONStringEnd(value: string, start: number) {
  for (let index = start + 1; index < value.length; index += 1) {
    if (value[index] === '\\') {
      index += 1;
      continue;
    }
    if (value[index] === '"') {
      return index + 1;
    }
  }

  return -1;
}

function readJSONNumberEnd(value: string, start: number) {
  let index = start;
  if (value[index] === '-') {
    index += 1;
  }
  if (index >= value.length) {
    return -1;
  }
  if (value[index] === '0') {
    index += 1;
  } else if (value[index] >= '1' && value[index] <= '9') {
    while (index < value.length && isDigit(value[index])) {
      index += 1;
    }
  } else {
    return -1;
  }

  if (value[index] === '.') {
    index += 1;
    const fractionStart = index;
    while (index < value.length && isDigit(value[index])) {
      index += 1;
    }
    if (fractionStart === index) {
      return -1;
    }
  }

  if (value[index] === 'e' || value[index] === 'E') {
    index += 1;
    if (value[index] === '+' || value[index] === '-') {
      index += 1;
    }
    const exponentStart = index;
    while (index < value.length && isDigit(value[index])) {
      index += 1;
    }
    if (exponentStart === index) {
      return -1;
    }
  }

  return index;
}

function isDigit(value: string) {
  return value >= '0' && value <= '9';
}

function isValidVariableName(value: string) {
  return value.length > 0 && byteLength(value) <= 64 && !value.includes('.') && !/\s/.test(value);
}

function isValidVariableValue(value: unknown) {
  if (typeof value === 'string') {
    return byteLength(value) <= 1024;
  }
  if (typeof value === 'number') {
    return Number.isFinite(value);
  }
  return typeof value === 'boolean';
}

function byteLength(value: string) {
  return new TextEncoder().encode(value).length;
}

function isValidAddrSpecEmail(value: string) {
  const trimmed = value.trim();
  if (
    !trimmed ||
    trimmed !== value ||
    byteLength(trimmed) > 254 ||
    /[^\x00-\x7F]/.test(trimmed) ||
    /[\s<>]/.test(trimmed)
  ) {
    return false;
  }

  const parts = trimmed.split('@');
  if (parts.length !== 2) {
    return false;
  }
  const [localPart, domainPart] = parts;
  if (!localPart || byteLength(localPart) > 64 || !isValidAddrSpecLocalPart(localPart)) {
    return false;
  }

  return isValidAddrSpecDomain(domainPart.toLowerCase());
}

function isValidAddrSpecLocalPart(value: string) {
  return /^(?!\.)(?!.*\.\.)(?!.*\.$)[A-Za-z0-9!#$%&'*+/=?^_`{|}~.-]+$/.test(value);
}

function isValidAddrSpecDomain(value: string) {
  if (!value || value.length > 253 || value !== value.trim()) {
    return false;
  }

  const labels = value.split('.');
  return labels.every((label) => {
    if (!label || label.length > 63) {
      return false;
    }
    return /^[a-z0-9](?:[a-z0-9-]*[a-z0-9])?$/.test(label);
  });
}

function isValidIP(value: string) {
  return isValidIPv4(value) || isValidIPv6(value);
}

function isValidIPv4(value: string) {
  const parts = value.split('.');
  if (parts.length !== 4) {
    return false;
  }
  return parts.every((part) => {
    if (!/^\d{1,3}$/.test(part)) {
      return false;
    }
    const numeric = Number(part);
    return numeric >= 0 && numeric <= 255 && String(numeric) === part;
  });
}

function isValidIPv6(value: string) {
  if (!value.includes(':') || /[^0-9a-fA-F:.]/.test(value)) {
    return false;
  }
  try {
    const parsed = new URL(`http://[${value}]/`);
    return parsed.hostname.length > 0;
  } catch {
    return false;
  }
}

function isAbortError(error: unknown) {
  return error instanceof Error && error.name === 'AbortError';
}

function isAuthorizationFailure(error: unknown) {
  return error instanceof MuxMailAPIError && (error.status === 401 || error.code === 'app_disabled');
}

function formatTime(value?: string) {
  if (!value) {
    return '-';
  }
  return dayjs(value).format('YYYY-MM-DD HH:mm:ss');
}

function emptyText(value?: string) {
  return value || '-';
}

function errorMessage(error: unknown, locale: Locale) {
  if (error instanceof MuxMailAPIError) {
    const localized = apiErrorMessages[locale][error.code] || apiErrorMessages['en-US'][error.code] || error.message;
    const requestID = error.requestID ? ` (${error.requestID})` : '';
    return `${error.code}: ${localized}${requestID}`;
  }
  if (error instanceof Error) {
    return error.message;
  }
  return String(error);
}

function findTemplateForScene(config: AdminConfigSummary | undefined, sceneCode: string | undefined, locale: string | undefined) {
  if (!config || !sceneCode) {
    return undefined;
  }
  const scene = config.app.scenes.find((item) => item.code === sceneCode && item.enabled);
  if (!scene) {
    return undefined;
  }
  const targetLocale = locale || config.app.default_locale;
  const requested = config.app.templates.find(
    (template) => template.code === scene.template && template.locale === targetLocale && template.enabled
  );
  if (requested) {
    return requested;
  }
  if (targetLocale === config.app.default_locale) {
    return undefined;
  }
  return config.app.templates.find(
    (template) => template.code === scene.template && template.locale === config.app.default_locale && template.enabled
  );
}

function buildExampleVarsJSON(requiredVars: string[]) {
  const vars = Object.fromEntries(requiredVars.map((name) => [name, exampleValueForVar(name)]));
  return JSON.stringify(vars, null, 2);
}

function exampleValueForVar(name: string) {
  if (name.includes('minute') || name.includes('count') || name.endsWith('_days') || name.endsWith('_hours')) {
    return 10;
  }
  if (name.includes('code')) {
    return '123456';
  }
  return 'change-me';
}

function newAdminIdempotencyKey() {
  return `admin-${Date.now()}-${Math.random().toString(36).slice(2, 10)}`;
}

function normalizeNavKey(value: string | null): NavKey {
  return navKeys.includes(value as NavKey) ? (value as NavKey) : 'dashboard';
}

export default App;
