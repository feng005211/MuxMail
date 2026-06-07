# MuxMail 接入平台支持矩阵

最后核验日期：2026-06-08。

额度、价格、功能开关和地区币种都由邮件服务商自行调整。本文记录的是最后核验时公开页面上的信息，生产部署前必须再以服务商官网、控制台和合同为准。MuxMail 当前 MVP 不会自动同步或扣减服务商额度；请用 App/Scene 级限流和路由顺序把发送量控制在服务商账号允许范围内。

## 当前已接入平台

| Provider | MuxMail provider 值 | 已支持 Transport | 已支持事件接收 | 主要用途 |
| --- | --- | --- | --- | --- |
| Mock | `mock` | `api` | 不适用 | 本地开发、测试、dry-run 之外的无网络发送验证 |
| Resend | `resend` | `api`, `smtp` | Resend 原生 Webhook、MuxMail 标准化事件 | 事务邮件主通道或备用通道 |
| Brevo | `brevo` | `api`, `smtp` | Brevo 原生 Webhook、MuxMail 标准化事件 | 事务邮件主通道或备用通道 |

Mailgun、AWS SES、腾讯云 SES、阿里云 DirectMail 目前只在设计文档中作为后续候选平台出现，不属于当前代码已接入平台。

## 配置字段

| Provider | Provider Account 凭据 | SMTP Channel 字段 | DNS / 发信域名要求 |
| --- | --- | --- | --- |
| Mock | 不需要真实凭据 | 不支持 SMTP | 不需要 DNS 认证 |
| Resend | `credentials.api_key` | `smtp.host`, `smtp.port: 587`, `smtp.username`, `smtp.password_ref` | 发信域名必须先在 Resend 完成 DKIM、SPF、DMARC 等认证；MuxMail 要求 `from` 邮箱域名等于 `sender_domain` |
| Brevo | `credentials.api_key` | `smtp.host`, `smtp.port: 587`, `smtp.username`, `smtp.password_ref` | 发信域名必须先在 Brevo 完成域名认证；MuxMail 要求 `from` 邮箱域名等于 `sender_domain` |

生产配置中所有真实密钥都使用 `env:` 或 `file:` 引用，不把 Provider API Key、SMTP 密码或 Webhook Secret 写入仓库。

## 免费和付费额度

### Mock

| Plan | 每日额度 | 每月额度 | 价格 | 说明 |
| --- | --- | --- | --- | --- |
| Local | 不限制 | 不限制 | 免费 | 只在本机进程内返回模拟结果，不调用外部服务商，不代表真实送达 |

### Resend

| Plan | 每日额度 | 每月额度 | 价格 | 超额 |
| --- | --- | --- | --- | --- |
| Free | 100 emails/day | 3,000 emails/month | $0/month | Free plan 不支持付费超额 |
| Pro | No daily limit | 50,000 emails/month | $20/month | $0.90 / 1,000 extra emails |
| Pro | No daily limit | 100,000 emails/month | $35/month | $0.90 / 1,000 extra emails |
| Scale | No daily limit | 100,000 emails/month | $90/month | $0.90 / 1,000 extra emails |
| Scale | No daily limit | 200,000 emails/month | $160/month | $0.80 / 1,000 extra emails |
| Scale | No daily limit | 500,000 emails/month | $350/month | $0.70 / 1,000 extra emails |
| Scale | No daily limit | 1,000,000 emails/month | $650/month | $0.65 / 1,000 extra emails |
| Scale | No daily limit | 1,500,000 emails/month | $825/month | $0.52 / 1,000 extra emails |
| Scale | No daily limit | 2,500,000 emails/month | $1,150/month | $0.46 / 1,000 extra emails |
| Enterprise | No daily limit | Flexible | Custom | 按合同 |

来源：[Resend Pricing](https://resend.com/pricing) 和 [Resend Knowledge Base: What is Resend Pricing](https://resend.com/docs/knowledge-base/what-is-resend-pricing)。Resend 官方页面同时列出 RESTful API、SMTP relay、Webhook endpoints 和 webhook events 等能力。

### Brevo

| Plan | 每日额度 | 每月额度 | 价格 | 说明 |
| --- | --- | --- | --- | --- |
| Free | 300 emails/day | 9,000 emails/month；未使用的每日额度不结转 | $0/month | 账号获批发送后可用 |
| Starter | No daily sending limit | 5,000 / 10,000 / 15,000 / 20,000-100,000 emails/month | From $9/month | 具体价格随月发送量和 add-on 调整 |
| Standard | No daily sending limit | 5,000 / 10,000 / 15,000 / 20,000-500,000 emails/month | From $18/month | 具体价格随月发送量和 add-on 调整 |
| Professional | 未列固定每日限制 | 150,000-10,000,000 emails/month | From $499/month | 高容量和团队功能面向更正式的商业部署 |
| Enterprise | 按合同 | 按合同 | Custom price | 定制容量、支持和专用能力 |

来源：[Brevo pricing page](https://www.brevo.com/pricing/)、[Brevo Help Center: About Brevo's pricing plans](https://help.brevo.com/hc/en-us/articles/208589409-About-Brevo-s-pricing-plans) 和 [Brevo Transactional Email](https://www.brevo.com/products/transactional-email/)。Brevo Transactional Email 页面明确支持 REST API、SMTP relay 和 Webhook。

## MuxMail 使用建议

第一阶段推荐使用一个主通道和一个备用通道：

```text
auth.example.com       -> Brevo API 主通道
auth-bak.example.com   -> Resend API 备用通道
auth-smtp.example.com  -> Resend SMTP 备用通道
```

验证码、找回密码等关键事务邮件应独立使用 `auth.*` 类子域名，不和营销或批量通知共用发信域名。不要通过注册多个同类免费账号轮换额度；MuxMail 的设计目标是服务商容灾和可审计路由，不是规避服务商限制。

当前 MVP 的路由只按配置顺序、收件域名和失败分类选择候选通道。每日额度、月度额度、成本优先级、健康度统计和自动熔断属于后续阶段能力。
