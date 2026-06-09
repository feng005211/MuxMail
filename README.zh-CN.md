# MuxMail

[English](README.md)

MuxMail 是一个自托管的事务邮件路由网关，面向验证码、找回密码和其他关键业务邮件场景。

当前 MVP 刻意保持轻量：单进程、单容器、文件配置、内存队列、内存限流、内存幂等缓存、静态退信名单 YAML，以及 JSONL 审计日志。

## 当前 MVP 能力

- 按 App 隔离的 API Key、Scene、Template、Route Policy 和 Provider Channel。
- 异步发送接口 `POST /v1/mail/send`。
- 按 App 隔离的消息、尝试、退信名单、Provider Event 和统计摘要只读 API。
- 可选内置 Lite Admin UI，路径为 `/admin/`，支持仪表盘、消息排查、Provider Event、退信名单、测试发送和安全配置摘要。
- 可选的标准化事件、Resend 原生事件、Brevo 原生事件接收器。
- 通过 Mock API、Resend API/SMTP、Brevo API/SMTP 进行 Provider failover。
- 进程内 fixed-window 限流和幂等控制。
- JSONL 消息日志、尝试日志、事件日志和可选统计日志。
- `muxmail config validate` 与 `muxmail send dry-run` 命令。

已接入平台、每日/月度额度和公开价格来源见 [docs/provider-support.md](docs/provider-support.md)。

## MVP 非目标

- 不做 Tenant 模型。
- 不要求 PostgreSQL 或 Redis。
- 不要求使用 Lite Admin，且 Lite Admin 不做在线编辑配置。
- 不做入站 SMTP Server。
- 默认不启用 Provider Webhook Receiver。
- 不做营销群发、附件、打开追踪或点击追踪。

## 验证

```powershell
$env:GOCACHE = (Join-Path (Get-Location) '.gocache')
go test ./...
go vet ./...
make build
```

如果你的系统默认 Go cache 目录可写，也可以不设置 `GOCACHE`。

如果本机可用 `make`，推荐直接执行：

```sh
make verify
```

`make verify` 会先在 `web/admin` 内执行 `npm ci`，构建 Lite Admin UI，临时放入 Go 嵌入资源目录完成二进制构建，然后恢复源码树里的轻量占位资源。

## 配置校验与 Dry-Run

校验示例配置：

```powershell
go run ./cmd/muxmail config validate -c config.example.yaml
```

校验生产配置，并拒绝所有 `plain:` 密钥引用：

```powershell
go run ./cmd/muxmail config validate -c config.yaml --strict
```

`config.example.yaml` 故意使用了 `plain:` 占位值，因此 `--strict` 主要用于真实本地配置或生产配置。
容器部署建议从 `config.container.example.yaml` 开始，它已经改成了 `env:` 密钥引用，并使用 `/var/lib/muxmail` 作为数据路径。

执行一次 dry-run，在不入队、不调用真实 Provider 的情况下检查模板与路由：

```powershell
go run ./cmd/muxmail send dry-run -c config.example.yaml --app project_a --scene register_code --to user@example.com --locale en-US --var code=123456 --var expire_minutes=10
```

对应的 `make` 目标：

```sh
make validate-example
make validate-container-example
make dry-run-example
```

## 本地运行

1. 把 `config.example.yaml` 复制为 `config.yaml`。
2. 真实部署时，把示例密钥替换成 `env:` 或 `file:` 引用。
3. 把 `logging.dir` 和 `suppression_file` 改到本机可写路径。
4. 启动服务。

```powershell
go run ./cmd/muxmail serve -c config.yaml
```

源码目录本地运行时，先用 `make admin-build` 或 `cd web/admin && npm run build` 生成 `web/admin/dist`；Docker 镜像构建会自动完成这一步。

打开内置 Lite Admin UI：

```text
http://localhost:8080/admin/
```

管理界面在当前浏览器会话中使用 App API Key 调用现有 App 隔离 API。API Key 只保存在页面内存中，不写入浏览器存储，也不暴露 Provider 密钥或在线编辑 YAML 配置；`/v1/` API 响应会带 `Cache-Control: no-store`。`/admin/` HTML 入口同样使用 `no-store`，避免升级后浏览器继续缓存旧 Admin bundle。

更完整的本地联调、容器部署、1Panel 和 Webhook 说明见 [docs/deployment.md](docs/deployment.md)。

## API

当前版本：

```text
0.1.0
```

机器可读的 API 合约：

```text
docs/openapi.yaml
```

发布记录：

```text
CHANGELOG.md
```

项目协作说明：

```text
CONTRIBUTING.md
SECURITY.md
```

```http
POST /v1/mail/send
Authorization: Bearer <app_api_key>
Content-Type: application/json
Idempotency-Key: <stable-request-key>
```

```json
{
  "scene": "register_code",
  "to": "user@example.com",
  "locale": "en-US",
  "vars": {
    "code": "123456",
    "expire_minutes": "10"
  },
  "context": {
    "user_ip": "203.0.113.10",
    "request_id": "business-request-001"
  }
}
```

接口会在校验完成、日志写入成功、队列入队成功后返回 `202 Accepted`。真正的 Provider 投递由 Worker 异步完成。

查询当前 App 最近的消息快照：

```http
GET /v1/mail/messages?limit=50&status=failed&scene=register_code
Authorization: Bearer <app_api_key>
```

查询当前 App 最近的失败消息：

```http
GET /v1/mail/messages/failed?limit=50&scene=register_code
Authorization: Bearer <app_api_key>
```

查询一封消息当前的 Lite 模式状态：

```http
GET /v1/mail/messages/{message_id}
Authorization: Bearer <app_api_key>
```

响应按 App 做隔离，不会返回完整收件人邮箱、邮件正文、模板变量、caller IP 或 user IP。

查询一封消息的 Provider Event 时间线：

```http
GET /v1/mail/messages/{message_id}/events
Authorization: Bearer <app_api_key>
```

响应按 App 做隔离，会返回这封消息已记录的 Provider Event 序列，但不会暴露收件人邮箱或原始 Webhook Payload。

查询一封消息的发送尝试时间线：

```http
GET /v1/mail/messages/{message_id}/attempts
Authorization: Bearer <app_api_key>
```

响应按 App 做隔离，会返回每个 `attempt_no` 的发送与最终 sent 或 failed 记录。

查询当前 App 的退信名单：

```http
GET /v1/suppressions?limit=50&reason=complaint&email=user@example.com
Authorization: Bearer <app_api_key>
```

这个接口会返回完整邮箱地址，因为退信名单审查和人工清理需要真实收件人标识。

查询当前 App 最近的 Provider Event：

```http
GET /v1/provider-events?limit=50&provider=brevo&event_type=bounced
Authorization: Bearer <app_api_key>
```

响应按 App 做隔离，不会暴露原始 Webhook Payload 或完整收件人邮箱。

查询 Lite 模式统计摘要：

```http
GET /v1/stats/summary?window=24h
Authorization: Bearer <app_api_key>
```

支持的时间窗口只有 `1h`、`24h` 和 `7d`。当 `stats: off` 时，接口返回空摘要；当 `stats: file` 时，MuxMail 会聚合 `mail-stats.jsonl`。

标准化 Provider Event 接收接口：

```http
POST /v1/provider-events
Authorization: Bearer <webhook_shared_secret>
Content-Type: application/json
```

这个接口默认关闭。开启条件是配置 `webhooks.enabled: true` 和 `webhooks.shared_secret_ref`。它接收 MuxMail 标准化后的 Provider Event，并可把消息从 `sent` 推进到 `delivered`、`bounced` 或 `complained`。
Webhook 事件必须带完整 provider account、channel 和 provider message 元数据，并且只有这些 identity 匹配同一 App、同一消息的已发送 attempt 时才会被接受。如果已发送 attempt 因服务商 accepted 响应没有返回 provider id 而记录了空 `provider_message_id`，认证后的 webhook 仍必须提供 `provider_message_id`，并匹配已记录的 provider account 和 channel。
Lite JSONL 日志会保存脱敏后的来源摘要，而不是标准化请求里的原始 `event_payload`。

Resend 原生 Webhook：

```http
POST /v1/provider-events/resend
Content-Type: application/json
svix-id: <id>
svix-timestamp: <unix-seconds>
svix-signature: <signature>
```

开启条件是配置 `webhooks.enabled: true` 和 `webhooks.resend_secret_ref`。MuxMail 会校验 Svix 签名，并映射 `email.delivered`、`email.bounced`、`email.complained`。

Brevo 原生 Webhook：

```http
POST /v1/provider-events/brevo
Authorization: Bearer <brevo_webhook_token>
Content-Type: application/json
```

开启条件是配置 `webhooks.enabled: true` 和 `webhooks.brevo_token_ref`。MuxMail 会映射 Brevo 的 `delivered`、`hardBounce` 和 `spam` 事件，从 Brevo tags 中取回 MuxMail 元数据，并使用 Brevo `ts_event` 作为 UTC 事件时间。

bounce 和 complaint 事件必须提供合法的单个收件人邮箱，并会自动 upsert 到 `suppression.yaml`。
收件人邮箱只用于退信名单更新，不会写入 JSONL 日志。
具有相同 app/message/provider/provider account/provider channel/provider message/event type/occurred-at 组合的重复事件不会再次追加事件记录，但仍可重放幂等 suppression 更新和单调状态修复。
迟到的 delivered 事件不会把已经 bounced 或 complained 的消息回滚为 delivered，但事件本身仍会记录。

健康检查接口：

```text
GET /healthz
GET /readyz
GET /version
```

## 部署说明

- 已发布的镜像可直接从 GHCR 拉取：

```sh
docker pull ghcr.io/feng005211/muxmail:latest
```

- `muxmail serve -c config.yaml` 是 MVP 的核心运行方式。
- `serve` 进程会同时启动 HTTP API 和内存 Worker。
- TLS 应由 1Panel、OpenResty、Nginx 等反向代理终止。
- 容器部署时，把配置挂载到 `/etc/muxmail/config.yaml`，把数据目录挂载到 `/var/lib/muxmail`。
- 容器中的 `logging.dir` 应设为 `/var/lib/muxmail/logs`，`suppression_file` 应设为 `/var/lib/muxmail/suppression.yaml`。
- 真实 API Key 和 Provider Secret 应放在环境变量或密钥文件里。
- `compose.example.yaml` 使用 `ghcr.io/feng005211/muxmail:latest`，并默认把 `config.container.example.yaml` 挂载为容器示例配置。
- 发布版本从 `VERSION` 读取；GitHub Release tag 必须是 `v${VERSION}`。

Docker、1Panel、本地运行和排障说明见 [docs/deployment.md](docs/deployment.md)。
