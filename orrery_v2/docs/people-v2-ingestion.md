# People v2 本地接入

People 是人员、组织、岗位、任职、汇报关系和长期责任的事实源。People 在业务写入、审计记录、命令回执和 Outbox 事件之间保持同一事务；Orrery 只消费事件，不回写 People 业务状态。

## 接口

- 地址：`POST /api/v1/people/events`
- 本地默认地址：`http://127.0.0.1:8080/api/v1/people/events`
- 鉴权：`Authorization: Bearer <service-token>`
- 完整性：请求体必须带匹配的 `Digest: SHA-256=<base64>`
- 幂等：`Idempotency-Key` 必须等于 `batch_id`
- 批量上限：500 个事件
- 事件协议：`stream=people.v2`、`schema_version=2`

接收端先把完整批次写入 `people_ingestion_batches` 和 `people_ingestion_events`，再按批内顺序写入 Ontology 事件日志并执行映射。投影失败时不返回确认游标；People 会按既有 Outbox 重试策略重新发送。批次和事件分别按 `(tenant_id, stream, batch_id)` 与 `(tenant_id, event_id)` 去重。

## 本地令牌

当前 smoke 配置的 `secret.root` 是配置文件相对路径 `.dev/smoke-data/secrets`；由于配置文件本身位于 `.dev`，从仓库根目录看到的解析结果是 `.dev/.dev/smoke-data/secrets`。可在 PowerShell 中生成一个仅供本机使用的令牌：

```powershell
$peopleTokenBytes = New-Object byte[] 32
[Security.Cryptography.RandomNumberGenerator]::Fill($peopleTokenBytes)
$peopleToken = [Convert]::ToBase64String($peopleTokenBytes)
New-Item -ItemType Directory -Force .\.dev\.dev\smoke-data\secrets | Out-Null
Set-Content -NoNewline .\.dev\.dev\smoke-data\secrets\people_ingestion_token $peopleToken
```

也可以在没有同名 secret 文件时设置 `ORRERY_SECRET_PEOPLE_INGESTION_TOKEN`。令牌至少 32 字节，People 的 `ORRERY_PEOPLE_PUBLISHER_TOKEN` 必须使用完全相同的值。令牌、游标签名 secret 和人员档案加密密钥都不得提交到 Git。

## 启动顺序

1. 启动 Orrery API，并确认实际 API 地址。使用桌面开发配置时通常是 `127.0.0.1:8080`。
2. 在 Orrery-people 仓库启动本地 PostgreSQL：`docker compose -f deploy/compose.test.yml up -d --wait postgres`。
3. 按 People README 配置开发身份、档案加密密钥和租户。
4. 为 People 增加以下发布参数后启动 API：

```powershell
$env:ORRERY_PEOPLE_PUBLISHER_URL = "http://127.0.0.1:8080/api/v1/people/events"
$env:ORRERY_PEOPLE_PUBLISHER_TOKEN = $peopleToken
$env:ORRERY_PEOPLE_PUBLISHER_CURSOR_SECRET = "replace-with-a-separate-32-byte-local-secret"
go run .\cmd\orrery-people
```

未配置 `ORRERY_PEOPLE_PUBLISHER_URL` 时，People 仍会正常写入本地 Outbox，但同步状态为 `not_configured`，不会隐式连接任何远端服务。

## 当前投影范围

以下事件已经进入 Context Graph：

- `people.subject.v2.changed`
- `people.org_unit.v2.changed`
- `people.job_profile.v2.changed`
- `people.position.v2.changed`
- `people.assignment.v2.changed`
- `people.reporting_relationship.v2.changed`
- `people.responsibility.v2.changed`
- `people.responsibility_binding.v2.changed`

`subject_fact`、`capability_assertion`、`segment`、`source_system`、`source_authority_rule`、`quality_issue`、`employee_profile` 和 `change_set` 事件会被鉴权、校验、持久化并确认，但暂不生成 Context Graph 对象或关系。人员私密档案值和证据正文不得出现在交付载荷中。
