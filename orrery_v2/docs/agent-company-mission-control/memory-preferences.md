# 个人助理的稳定偏好与按需记忆

个人助理现在可以在普通回合中看到少量长期交互偏好。例如，用户明确说“以后默认用中文回答”，后台提取成功后，后续私有会话会带入这个偏好。任务经历、项目知识和组织记忆继续由 Agent 通过读取工具按需检索。

此实现参考了 Anthropic [commerce-agents 的记忆模块](https://github.com/anthropics/commerce-agents/blob/fd4d59224ab96b43c6dc6888207c67b3bd5a24cf/commerce-common/commerce_common/memory.py)的显式事实提取、主题更新和默认注入思路，复用 Orrery 的逻辑身份、MemoryCandidate / MemoryRecord、事务、生命周期、审计与执行队列。没有引入独立记忆数据库，也没有修改 Agent 调度和运行时协议。

## 一次偏好更新

```mermaid
flowchart LR
  U[私有会话中的用户表达] --> E[保存交流经历]
  E --> G[已接受的来源和偏好信号检查]
  G --> Q[持久化后台任务]
  Q --> X[只从用户原话提取]
  X --> V[主题、证据、长度与状态校验]
  V --> W[事务更新当前偏好并归档旧版本]
  W --> C[后续回合注入限长偏好]
```

提取发生在回复保存之后，使用现有自然语言记忆模型配置和低 reasoning effort。HTTP 超时为 20 秒，任务超时为 30 秒，最多尝试 3 次。任务 ID 由已保存的交流候选生成，相同交流重复捕获不会新增任务。同一逻辑助理的提取任务使用同一个队列键。提取或记忆读取失败不阻止对话完成；失败任务与日志保留实际失败状态。

后台尚未完成时，紧接着发起的回合可能仍看到上一个版本。当前用户指令优先于保存的偏好。

外部 CLI 复用会话时，即使上下文指纹没有变化，也会把稳定偏好放入本轮必须保留的上下文片段，避免偏好只在首次会话中出现。普通任务经历沿用原有的按需读取策略。

## 注入和保存范围

| 项目 | 行为 |
|---|---|
| 身份 | 由租户、Owner 和个人助理逻辑身份共同校验，切换执行宿主不改变记忆归属 |
| 会话范围 | 仅个人助理私有会话自动提取和注入；包括 PA 发起的 Mission 在内的共享群组不自动使用私有偏好 |
| 主题 | `response_language`、`response_format`、`response_detail`、`report_format`、`communication_style`、`timezone`、`units`、`currency` |
| 单次提取 | 最多 3 条；每个值最多 200 Unicode 字符，来源引文最多 500 字符 |
| 输入 | 最多 2000 字符的完整用户文本；超过上限的消息跳过提取，避免截断限定条件 |
| 默认上下文 | 最多 8 个主题，总计不超过 2000 UTF-8 字节；较新的完整条目优先，不截断单条偏好 |
| 来源 | 引文必须出现在用户消息中；同时检查引文所在句子，过滤一次性要求、问题、假设及明显的转述或引用 |
| 敏感内容 | 待审核或敏感的原始经历不能自动生成偏好；额外过滤常见联系方式、长数字标识和凭据特征 |
| 更新 | 同一 Profile 和主题对应稳定记录 ID；旧记录归档为修订，当前记录通过 `SupersedesRefs` 指向旧版本 |
| 乱序和重试 | 根据来源经历时间比较新旧；同一来源候选写入幂等；保存与修订在同一主题事务内完成 |
| 删除和审核 | 不注入已删除、归档、匿名化、禁用、过期或未被接受的偏好；其原始来源失效也会停止注入 |
| 恢复 | 后台提取不会重新启用已删除或禁用的偏好；仅因时间过期的记录可以由更新的用户表达续期 |

默认偏好记录使用 `stable_preference` 标签、`preference.<topic>` 主题和 `Preferences` 字段。`Facts` 保存原话，`LineageRefs` 指向交流记录与会话/Run/用户消息，`SourceEvidenceRefs` 保存用户消息引用。保留期限不会超过来源记录和 Profile 的期限。关闭现有 Memory 总开关同时停止提取与默认读取。

此处使用提取模型判断语义，代码检查不能证明所有释义都完全正确。当前策略偏保守，可能漏掉复杂条件或少见表达；它也不会自动回填历史会话。初始范围是八类交互偏好，业务约束与专业经验仍使用原有记忆链路。

## 检索结果的来源标识

`memory_search` 返回的个人和集体摘要增加两个字段：

| 字段 | 值 | 含义 |
|---|---|---|
| `retrieval_mode` | `semantic` | 语义召回返回了记录 |
| `retrieval_mode` | `profile_digest` | 普通 Profile 摘要，没有按照本次查询匹配；不能作为语义命中的证据 |
| `retrieval_mode` | `none` | 没有可用的摘要内容 |
| `fallback_reason` | `no_semantic_match` | 语义召回没有命中，返回空结果 |
| `fallback_reason` | `semantic_unavailable` | 没有配置语义召回实现，返回空结果 |

显式查询只返回匹配结果；不会用无关的 Profile 摘要替换空结果，语义服务报错仍作为错误返回。直接读取 Profile 概览没有回退原因。默认偏好上下文使用 `stable_preferences` 标识，不会伪装成查询结果。

默认回复遵循已保存的交互偏好；当前用户明确提出不同要求时优先执行当前要求。仅使用另一种语言提问，不视为修改默认回答语言。该规则随每轮偏好上下文提供给模型，实际回答仍需要端到端验收。

## 中文组织规则比较

World consolidator 现在识别同一主体和动作下的常见肯定/否定要求，例如“报告必须用表格”与“报告不要用表格”，并保留句子边界以处理摘要、正文重复出现的情况。中文字符二元组参与相似度计算，相同中文规则不会仅因没有空格被当成新规则。

这是针对常见明确规则的比较器，不是完整中文语义推理。问题、假设、引用、不同主体/动作和双重否定等不明确情况不会仅凭一个否定词被判为冲突。复杂同义表达仍可能漏检。

## 代码与验证入口

- 偏好提取、保存和默认上下文：`internal/app/memorypreference/`。
- 私有会话注入与续聊：`internal/app/discussionsession/service_personal_assistant_preferences.go`、`runtime_context_parts.go`。
- 生产模型、队列和 Provider 接线：`cmd/server/production_memory_preferences.go`、`production_memory_pageindex.go`。
- 语义检索来源：`cmd/server/production_discussion_memory.go`、`production_mcp_gateway.go`。
- 中文规则：`internal/memory/distillation/world_chinese.go`。

针对性验证命令：

```sh
go test ./internal/memory/... ./internal/app/memorypreference ./internal/app/discussionsession
go test ./cmd/server -run 'Memory|Preference' -count=1
```

测试覆盖真实记忆存储及后台队列、偏好更新和来源追踪、幂等重放、乱序、事务回滚、删除/审核/过期、跨租户与跨 Owner 隔离、宿主切换、模型/读取/元数据失败，以及语义命中和各类回退。
