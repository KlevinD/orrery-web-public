package website

import (
	"fmt"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"
)

const standardAnswerPatternMaxRunes = 160

type standardAnswerClass string

const (
	standardAnswerConversational standardAnswerClass = "conversational"
	standardAnswerFactual        standardAnswerClass = "factual"
	standardAnswerRefusal        standardAnswerClass = "refusal"
	standardAnswerClarification  standardAnswerClass = "clarification"
	standardAnswerLeadPrompt     standardAnswerClass = "lead_prompt"
)

type standardAnswer struct {
	IntentID string
	Locale   string
	Exact    []string
	Patterns []string
	Text     string
	Sources  []string
	Class    standardAnswerClass
	Action   string
}

const (
	overviewSource    = "minviabrain://08-Wiki/Public/Orrery/overview.md"
	integrationSource = "minviabrain://08-Wiki/Public/Orrery/integration-smoke.md"
)

var baseWebsiteStandardAnswers = []standardAnswer{
	{
		IntentID: "greeting.zh-CN", Locale: "zh-CN", Exact: []string{"你好", "您好", "嗨", "你好啊", "你好呀", "你好哇", "您好啊", "您好呀", "哈喽", "哈罗", "哈啰", "嗨喽", "在吗", "在不在"},
		Patterns: []string{`^(你好|您好|嗨|你好啊|你好呀|你好哇|您好啊|您好呀|哈喽|哈罗|哈啰|嗨喽|在吗|在不在)$`}, Class: standardAnswerConversational,
		Text: "你好，我是 Orrery 智能顾问。你可以问我 Minvia 产品、企业接入、部署方式或预约演示。",
	},
	{
		IntentID: "greeting.en-US", Locale: "en-US", Exact: []string{"hello", "hi", "hey", "hello there", "hi there", "hey there"},
		Patterns: []string{`^(hello|hi|hey|hello there|hi there|hey there)$`}, Class: standardAnswerConversational,
		Text: "Hello, I am the Orrery advisor. Ask me about Minvia products, enterprise integration, deployment, or a demo.",
	},
	{
		IntentID: "thanks.zh-CN", Locale: "zh-CN", Exact: []string{"谢谢", "感谢", "明白了", "谢谢你", "多谢", "好的谢谢", "收到谢谢"},
		Patterns: []string{`^(谢谢|感谢|明白了|谢谢你|多谢|好的谢谢|收到谢谢)$`}, Class: standardAnswerConversational,
		Text: "不客气。还有产品、接入或部署问题，可以继续问我。",
	},
	{
		IntentID: "thanks.en-US", Locale: "en-US", Exact: []string{"thanks", "thank you", "got it", "many thanks", "thanks a lot", "appreciate it"},
		Patterns: []string{`^(thanks|thank you|got it|many thanks|thanks a lot|appreciate it)$`}, Class: standardAnswerConversational,
		Text: "You are welcome. You can keep asking about products, integrations, or deployment.",
	},
	{
		IntentID: "goodbye.zh-CN", Locale: "zh-CN", Exact: []string{"再见", "拜拜", "结束咨询", "回头见", "先这样", "下次再聊"},
		Patterns: []string{`^(再见|拜拜|结束咨询|回头见|先这样|下次再聊)$`}, Class: standardAnswerConversational,
		Text: "再见。需要时可以随时回来继续咨询。",
	},
	{
		IntentID: "goodbye.en-US", Locale: "en-US", Exact: []string{"goodbye", "bye", "end chat", "see you", "talk later", "see you later"},
		Patterns: []string{`^(goodbye|bye|end chat|see you|talk later|see you later)$`}, Class: standardAnswerConversational,
		Text: "Goodbye. You can return whenever you need more information.",
	},
	{
		IntentID: "restart.zh-CN", Locale: "zh-CN", Exact: []string{"重新开始", "开始新咨询"},
		Class: standardAnswerConversational, Text: "好的。请直接说出这次想了解的产品或业务问题。",
	},
	{
		IntentID: "restart.en-US", Locale: "en-US", Exact: []string{"start over", "new consultation"},
		Class: standardAnswerConversational, Text: "All right. Tell me which product or business question you want to start with.",
	},
	{
		IntentID: "capabilities.zh-CN", Locale: "zh-CN", Exact: []string{"你能做什么", "你可以做什么", "支持哪些场景"},
		Patterns: []string{`^(你能做什么|你可以做什么|支持哪些(产品|业务)?场景)$`}, Class: standardAnswerFactual,
		Text: "我可以介绍 Minvia 与 Orrery、Mission 和数字员工、MCP 与 Ontology、Iris，以及公开的企业接入和部署方式；复杂问题会转给 Orrery 客服智能体。", Sources: []string{overviewSource},
	},
	{
		IntentID: "capabilities.en-US", Locale: "en-US", Exact: []string{"what can you do", "what scenarios do you support"},
		Class: standardAnswerFactual, Text: "I can explain Minvia and Orrery, Missions and digital workers, MCP and Ontology, Iris, public integration options, and deployment models. Complex questions are routed to the Orrery service agent.", Sources: []string{overviewSource},
	},
	{
		IntentID: "minvia.zh-CN", Locale: "zh-CN", Exact: []string{"minvia 是什么", "介绍一下 minvia"},
		Patterns: []string{`^minvia\s*是什么$`},
		Class:    standardAnswerFactual, Text: "Minvia 提供企业智能体即服务（Agents as a Service），通过 Orrery 把目标、人员、数字员工、知识、工具和执行过程组织在可治理的工作环境中。", Sources: []string{overviewSource},
	},
	{
		IntentID: "minvia.en-US", Locale: "en-US", Exact: []string{"what is minvia", "tell me about minvia"},
		Class: standardAnswerFactual, Text: "Minvia provides enterprise Agents as a Service. Orrery organizes goals, people, digital workers, knowledge, tools, and execution in a governed workspace.", Sources: []string{overviewSource},
	},
	{
		IntentID: "orrery.zh-CN", Locale: "zh-CN", Exact: []string{"orrery 是什么", "介绍一下 orrery"},
		Patterns: []string{`^orrery\s*是什么$`},
		Class:    standardAnswerFactual, Text: "Orrery 是支撑 Minvia 企业智能体即服务的企业级 AI Agent Mission Control，用于组织数字员工、Mission、知识、工具、权限、执行证据和验收，并支持云端托管、私有化和本地部署。", Sources: []string{overviewSource},
	},
	{
		IntentID: "orrery.en-US", Locale: "en-US", Exact: []string{"what is orrery", "tell me about orrery"},
		Class: standardAnswerFactual, Text: "Orrery is the enterprise AI Agent Mission Control behind Minvia's Agents as a Service, organizing digital workers, Missions, knowledge, tools, permissions, execution evidence, and acceptance across cloud-hosted, private, and local deployment options.", Sources: []string{overviewSource},
	},
	{
		IntentID: "chatbot-difference.zh-CN", Locale: "zh-CN",
		Exact: []string{"orrery 和普通聊天机器人有什么区别", "orrery 和聊天机器人有什么区别", "orrery 与聊天机器人有什么区别"},
		Class: standardAnswerFactual, Text: "普通聊天机器人主要回答问题；Orrery 围绕业务目标组织 Mission、人员、数字员工、工具、执行证据和验收，用于管理可追踪的工作过程与结果。", Sources: []string{overviewSource},
	},
	{
		IntentID: "chatbot-difference.en-US", Locale: "en-US",
		Exact: []string{"how is orrery different from a chatbot", "what is the difference between orrery and a chatbot"},
		Class: standardAnswerFactual, Text: "A typical chatbot mainly answers questions. Orrery organizes Missions, people, digital workers, tools, execution evidence, and acceptance around business goals so work and outcomes remain traceable.", Sources: []string{overviewSource},
	},
	{
		IntentID: "suitable-scenarios.zh-CN", Locale: "zh-CN",
		Exact: []string{"什么场景适合使用 orrery", "orrery 适合什么场景", "哪些业务适合 orrery"},
		Class: standardAnswerFactual, Text: "Orrery 适合需要人员、数字员工和业务系统共同完成，并且要求明确责任、过程可追踪、结果可验收的业务场景。", Sources: []string{overviewSource},
	},
	{
		IntentID: "suitable-scenarios.en-US", Locale: "en-US",
		Exact: []string{"what scenarios are suitable for orrery", "what is orrery suitable for"},
		Class: standardAnswerFactual, Text: "Orrery suits business scenarios where people, digital workers, and systems collaborate with explicit ownership, traceable execution, and verifiable outcomes.", Sources: []string{overviewSource},
	},
	{
		IntentID: "getting-started.zh-CN", Locale: "zh-CN",
		Exact: []string{"怎么开始使用 orrery", "如何开始使用 orrery", "怎么开始用 orrery"},
		Class: standardAnswerFactual, Text: "建议先选择一个边界清楚、结果可验证的业务目标，建立首个 Mission，再明确参与者、任务输入输出、所需知识和工具以及验收标准。", Sources: []string{overviewSource},
	},
	{
		IntentID: "getting-started.en-US", Locale: "en-US",
		Exact: []string{"how do i get started with orrery", "how do i start using orrery"},
		Class: standardAnswerFactual, Text: "Start with one clearly bounded business goal and a verifiable outcome. Create the first Mission, then define participants, task inputs and outputs, required knowledge and tools, and acceptance criteria.", Sources: []string{overviewSource},
	},
	{
		IntentID: "pov-definition.zh-CN", Locale: "zh-CN",
		Exact: []string{"什么是 pov", "pov 是什么", "proof of value 是什么"},
		Class: standardAnswerFactual, Text: "PoV 是在真实但受控的业务场景中验证 Orrery 能否产生可衡量价值，并确认后续落地所需的数据、系统、流程和治理条件。", Sources: []string{overviewSource},
	},
	{
		IntentID: "pov-definition.en-US", Locale: "en-US",
		Exact: []string{"what is a proof of value", "what is a pov"},
		Class: standardAnswerFactual, Text: "A Proof of Value tests whether Orrery creates measurable value in a real but controlled business scenario and identifies the data, system, process, and governance conditions needed for rollout.", Sources: []string{overviewSource},
	},
	{
		IntentID: "pov-inputs.zh-CN", Locale: "zh-CN",
		Exact: []string{"pov 需要我们投入什么", "做 pov 需要准备什么", "pov 需要什么"},
		Class: standardAnswerFactual, Text: "PoV 通常需要一个明确目标和负责人、必要的业务资料、受控的系统访问条件，以及双方认可的验收标准；具体范围由 PoV 场景确定。", Sources: []string{overviewSource},
	},
	{
		IntentID: "pov-inputs.en-US", Locale: "en-US",
		Exact: []string{"what do we need to provide for a pov", "what does a pov require"},
		Class: standardAnswerFactual, Text: "A PoV normally needs a clear objective and owner, necessary business materials, controlled system-access conditions, and agreed acceptance criteria. The selected scenario determines the exact scope.", Sources: []string{overviewSource},
	},
	{
		IntentID: "replace-existing-systems.zh-CN", Locale: "zh-CN",
		Exact: []string{"orrery 会替换现有系统吗", "orrery 会取代现有系统吗"},
		Class: standardAnswerFactual, Text: "通常不会。Orrery 通过 MCP、受控 API 或适配器连接现有系统，在其上组织智能体协作、权限、执行和审计，而不是要求企业重建全部业务系统。", Sources: []string{integrationSource},
	},
	{
		IntentID: "replace-existing-systems.en-US", Locale: "en-US",
		Exact: []string{"does orrery replace existing systems", "will orrery replace our existing systems"},
		Class: standardAnswerFactual, Text: "Usually not. Orrery connects to existing systems through MCP, governed APIs, or adapters and adds agent coordination, permissions, execution, and auditability without requiring every business system to be rebuilt.", Sources: []string{integrationSource},
	},
	{
		IntentID: "human-collaboration.zh-CN", Locale: "zh-CN",
		Exact: []string{"真人可以和数字员工一起工作吗", "人可以和数字员工协作吗"},
		Class: standardAnswerFactual, Text: "可以。真人和数字员工可以参与同一个 Mission，按职责分工，并在关键节点进行讨论、审批、质量检查和结果验收。", Sources: []string{overviewSource},
	},
	{
		IntentID: "human-collaboration.en-US", Locale: "en-US",
		Exact: []string{"can people work with digital workers", "can humans collaborate with digital workers"},
		Class: standardAnswerFactual, Text: "Yes. People and digital workers can participate in the same Mission, divide work by responsibility, and use discussion, approval, quality checks, and acceptance at key points.", Sources: []string{overviewSource},
	},
	{
		IntentID: "task-contract.zh-CN", Locale: "zh-CN",
		Exact: []string{"什么是 task contract", "task contract 是什么"},
		Class: standardAnswerFactual, Text: "Task Contract 用来明确一项任务的责任人、输入、预期输出、约束和完成标准，使执行结果能够被检查和验收。", Sources: []string{overviewSource},
	},
	{
		IntentID: "task-contract.en-US", Locale: "en-US", Exact: []string{"what is a task contract"},
		Class: standardAnswerFactual, Text: "A Task Contract defines the owner, inputs, expected outputs, constraints, and completion criteria for a task so its result can be reviewed and accepted.", Sources: []string{overviewSource},
	},
	{
		IntentID: "delivery-quality.zh-CN", Locale: "zh-CN",
		Exact: []string{"orrery 如何保证交付质量", "orrery 怎么保障交付质量"},
		Class: standardAnswerFactual, Text: "Orrery 把任务约定、执行证据、质量检查和人工验收连接成闭环；它提供可检查的过程和治理机制，但不对未经确认的业务结果作保证。", Sources: []string{overviewSource},
	},
	{
		IntentID: "delivery-quality.en-US", Locale: "en-US",
		Exact: []string{"how does orrery support delivery quality", "how does orrery ensure delivery quality"},
		Class: standardAnswerFactual, Text: "Orrery connects task contracts, execution evidence, quality checks, and human acceptance into a reviewable loop. It provides process and governance controls but does not guarantee unvalidated business outcomes.", Sources: []string{overviewSource},
	},
	{
		IntentID: "permissions-approvals.zh-CN", Locale: "zh-CN",
		Exact: []string{"orrery 如何控制权限", "orrery 怎么管理权限"},
		Class: standardAnswerFactual, Text: "Orrery 按人员、数字员工、任务和资源边界控制访问，并可在关键工具调用或业务动作前设置审批。具体权限仍需按企业组织和接入系统配置。", Sources: []string{overviewSource},
	},
	{
		IntentID: "permissions-approvals.en-US", Locale: "en-US",
		Exact: []string{"how does orrery control permissions", "how are permissions managed in orrery"},
		Class: standardAnswerFactual, Text: "Orrery controls access across people, digital workers, tasks, and resources, and can require approval before sensitive tool calls or business actions. Exact permissions are configured for the organization and connected systems.", Sources: []string{overviewSource},
	},
	{
		IntentID: "auditability.zh-CN", Locale: "zh-CN",
		Exact: []string{"orrery 的操作可以审计吗", "orrery 支持审计吗"},
		Class: standardAnswerFactual, Text: "Orrery 保留任务、执行、证据、审批、质量检查和验收等过程记录，用于追踪工作如何完成；具体审计范围取决于实际配置和连接器能力。", Sources: []string{overviewSource},
	},
	{
		IntentID: "auditability.en-US", Locale: "en-US",
		Exact: []string{"are orrery operations auditable", "does orrery support audit trails"},
		Class: standardAnswerFactual, Text: "Orrery records task, execution, evidence, approval, quality-check, and acceptance activity so completed work can be traced. Exact audit coverage depends on configuration and connector capabilities.", Sources: []string{overviewSource},
	},
	{
		IntentID: "api-access.zh-CN", Locale: "zh-CN",
		Exact: []string{"orrery 支持 api 接入吗", "orrery 可以通过 api 接入吗"},
		Class: standardAnswerFactual, Text: "支持通过受控 API、MCP 或适配器接入。具体接口、认证方式、读写范围和审计要求需要根据目标系统确认。", Sources: []string{integrationSource},
	},
	{
		IntentID: "api-access.en-US", Locale: "en-US",
		Exact: []string{"does orrery support api integration", "can orrery integrate through an api"},
		Class: standardAnswerFactual, Text: "Orrery can integrate through governed APIs, MCP, or adapters. Available interfaces, authentication, read/write scope, and audit requirements must be confirmed for the target system.", Sources: []string{integrationSource},
	},
	{
		IntentID: "mission.zh-CN", Locale: "zh-CN", Exact: []string{"mission 是什么", "任务 mission 是什么"},
		Class: standardAnswerFactual, Text: "Mission 用来描述一个需要数字员工协作完成的业务目标，并承载任务分解、执行过程、证据和结果。", Sources: []string{overviewSource},
	},
	{
		IntentID: "mission.en-US", Locale: "en-US", Exact: []string{"what is a mission", "what is mission"},
		Class: standardAnswerFactual, Text: "A Mission represents a business objective for digital workers and carries its task breakdown, execution, evidence, and results.", Sources: []string{overviewSource},
	},
	{
		IntentID: "digital-worker.zh-CN", Locale: "zh-CN", Exact: []string{"数字员工是什么", "什么是数字员工"},
		Class: standardAnswerFactual, Text: "数字员工是在明确职责、知识和工具权限边界内工作的企业智能体，可参与 Mission 并留下可检查的执行记录。", Sources: []string{overviewSource},
	},
	{
		IntentID: "digital-worker.en-US", Locale: "en-US", Exact: []string{"what is a digital worker"},
		Class: standardAnswerFactual, Text: "A digital worker is an enterprise agent operating within explicit responsibility, knowledge, and tool boundaries, with reviewable execution records.", Sources: []string{overviewSource},
	},
	{
		IntentID: "mcp.zh-CN", Locale: "zh-CN", Exact: []string{"mcp 是什么", "什么是 mcp"},
		Class: standardAnswerFactual, Text: "MCP 是智能体连接受控工具和数据资源的标准接口。在 Orrery 中，资源注册、权限和调用边界由平台治理。", Sources: []string{overviewSource},
	},
	{
		IntentID: "mcp.en-US", Locale: "en-US", Exact: []string{"what is mcp"},
		Class: standardAnswerFactual, Text: "MCP is a standard interface for agents to use controlled tools and data resources. Orrery governs registration, permissions, and invocation boundaries.", Sources: []string{overviewSource},
	},
	{
		IntentID: "ontology.zh-CN", Locale: "zh-CN", Exact: []string{"ontology 是什么", "什么是 ontology"},
		Class: standardAnswerFactual, Text: "Ontology 用统一的业务对象、关系和规则表达企业语义，使智能体能够在清晰的数据边界内理解和执行。", Sources: []string{overviewSource},
	},
	{
		IntentID: "ontology.en-US", Locale: "en-US", Exact: []string{"what is ontology"},
		Class: standardAnswerFactual, Text: "Ontology represents enterprise meaning through shared business objects, relationships, and rules so agents can reason within explicit data boundaries.", Sources: []string{overviewSource},
	},
	{
		IntentID: "iris.zh-CN", Locale: "zh-CN", Exact: []string{"iris 是什么", "什么是 iris"},
		Class: standardAnswerFactual, Text: "Iris 是 Minvia 产品体系中的智能交互能力，用于帮助用户与企业知识和智能体工作流进行自然交互。", Sources: []string{overviewSource},
	},
	{
		IntentID: "iris.en-US", Locale: "en-US", Exact: []string{"what is iris"},
		Class: standardAnswerFactual, Text: "Iris is Minvia's intelligent interaction capability for natural access to enterprise knowledge and agent workflows.", Sources: []string{overviewSource},
	},
	{
		IntentID: "integration.zh-CN", Locale: "zh-CN", Exact: []string{"怎么接入现有系统", "如何接入现有系统", "怎么连接现有系统"},
		Patterns: []string{`^(怎么|如何)(把\s*)?orrery\s*(接入|连接)(现有|已有)?(系统|业务系统)$`}, Class: standardAnswerFactual,
		Text: "Orrery 通常通过 MCP、受控 API 或现有系统适配器接入企业工具与数据。具体方案需要先确认系统类型、认证方式、读写范围和审计要求。", Sources: []string{integrationSource},
	},
	{
		IntentID: "integration.en-US", Locale: "en-US", Exact: []string{"how does orrery integrate with existing systems", "how can i integrate orrery"},
		Class: standardAnswerFactual, Text: "Orrery typically connects through MCP, governed APIs, or system adapters. The integration design depends on authentication, read/write scope, and audit requirements.", Sources: []string{integrationSource},
	},
	{
		IntentID: "deployment.zh-CN", Locale: "zh-CN", Exact: []string{"支持私有化部署吗", "支持本地部署吗", "有哪些部署方式", "支持 saas 吗"},
		Patterns: []string{`^支持(saas|私有化|本地)部署吗$`}, Class: standardAnswerFactual,
		Text: "支持云端托管、私有化和本地部署。最终形态取决于数据边界、网络条件、合规要求以及所需连接器。", Sources: []string{overviewSource},
	},
	{
		IntentID: "deployment.en-US", Locale: "en-US", Exact: []string{"do you support private deployment", "do you support on premises", "what deployment options are available"},
		Class: standardAnswerFactual, Text: "Public deployment options cover cloud-hosted, private, and local environments. The final design depends on data boundaries, network conditions, compliance, and connector needs.", Sources: []string{overviewSource},
	},
	{
		IntentID: "test-scope.zh-CN", Locale: "zh-CN", Exact: []string{"测试一次", "测试一下", "我想测试一下", "我想试一下", "试一下"},
		Patterns: []string{`^(我想)?(测试|试)(一次|一下)$`}, Class: standardAnswerClarification, Action: "clarify_test_scope",
		Text: "可以。你想测试产品咨询、部署方案，还是预约演示？",
	},
	{
		IntentID: "test-scope.en-US", Locale: "en-US", Exact: []string{"test it", "try it", "run a test", "i want to test it"},
		Class: standardAnswerClarification, Action: "clarify_test_scope", Text: "Sure. Would you like to test product questions, deployment guidance, or demo booking?",
	},
	{
		IntentID: "demo.zh-CN", Locale: "zh-CN", Exact: []string{"我想预约演示", "我想要预约一次演示", "申请演示", "预约 pov", "申请 proof of value", "商务合作"},
		Patterns: []string{`^(我(想|要|想要))?(请|麻烦)?(帮我|给我)?(预约|申请)(一下|一次|一个|个)?\s*(orrery\s*(的)?\s*)?(产品\s*)?演示(一下|吗|可以吗)?$`}, Class: standardAnswerLeadPrompt, Action: "open_lead_form",
		Text: "可以。请填写最少的联系信息、需求和明确同意，我们会把咨询线索提交给 Marketing 跟进。",
	},
	{
		IntentID: "demo.en-US", Locale: "en-US", Exact: []string{"book a demo", "request a demo", "request a proof of value", "partnership inquiry"},
		Class: standardAnswerLeadPrompt, Action: "open_lead_form", Text: "Please provide the minimum contact details, your request, and explicit consent. We will submit the inquiry to Marketing for follow-up.",
	},
	{
		IntentID: "contact.zh-CN", Locale: "zh-CN", Exact: []string{"怎么联系你们", "联系销售", "联系 minvia", "我想咨询"},
		Patterns: []string{`^(怎么|如何)联系(你们|minvia|销售)$`}, Class: standardAnswerLeadPrompt, Action: "open_lead_form",
		Text: "你可以打开咨询表单，留下至少一种联系方式、问题说明和明确同意。",
	},
	{
		IntentID: "contact.en-US", Locale: "en-US", Exact: []string{"contact sales", "contact minvia", "how can i contact you"},
		Class: standardAnswerLeadPrompt, Action: "open_lead_form", Text: "Open the consultation form and provide at least one contact method, your question, and explicit consent.",
	},
	{
		IntentID: "pricing-refusal.zh-CN", Locale: "zh-CN", Exact: []string{"给我确定报价", "承诺最终价格", "最低多少钱"},
		Class: standardAnswerRefusal, Text: "我不能承诺最终价格。报价需要根据部署方式、使用范围、集成和服务要求由商务人员确认。",
	},
	{
		IntentID: "pricing-refusal.en-US", Locale: "en-US", Exact: []string{"give me a guaranteed price", "what is the final price"},
		Class: standardAnswerRefusal, Text: "I cannot commit to a final price. Commercial terms depend on deployment, scope, integrations, and service requirements.",
	},
	{
		IntentID: "customer-data-refusal.zh-CN", Locale: "zh-CN", Exact: []string{"查询我的公司数据", "读取我的租户数据", "查看我的 mission"},
		Class: standardAnswerRefusal, Text: "这个官网顾问只能访问公开产品资料，不能读取任何公司的租户、Mission、任务、文件或客户数据。",
	},
	{
		IntentID: "customer-data-refusal.en-US", Locale: "en-US", Exact: []string{"show my company data", "read my tenant data", "show my missions"},
		Class: standardAnswerRefusal, Text: "This website advisor can use public product information only. It cannot access tenant data, Missions, tasks, files, or customer records.",
	},
	{
		IntentID: "roadmap-refusal.zh-CN", Locale: "zh-CN", Exact: []string{"告诉我内部路线图", "公开内部计划"},
		Class: standardAnswerRefusal, Text: "我只能说明已经公开的产品信息，不能披露内部路线图、未发布计划或保密信息。",
	},
	{
		IntentID: "roadmap-refusal.en-US", Locale: "en-US", Exact: []string{"show me the internal roadmap", "reveal unreleased plans"},
		Class: standardAnswerRefusal, Text: "I can discuss published product information only, not internal roadmaps, unreleased plans, or confidential material.",
	},
	{
		IntentID: "professional-advice-refusal.zh-CN", Locale: "zh-CN", Exact: []string{"给我法律建议", "给我医疗建议", "给我投资建议"},
		Class: standardAnswerRefusal, Text: "我可以提供一般性产品信息，但不能替代法律、医疗、投资或其他持证专业人士的意见。",
	},
	{
		IntentID: "professional-advice-refusal.en-US", Locale: "en-US", Exact: []string{"give me legal advice", "give me medical advice", "give me investment advice"},
		Class: standardAnswerRefusal, Text: "I can provide general product information, but not legal, medical, investment, or other licensed professional advice.",
	},
	{
		IntentID: "incomplete.zh-CN", Locale: "zh-CN", Exact: []string{"这个怎么做", "怎么弄", "可以吗", "怎么办"},
		Class: standardAnswerClarification, Text: "请再说明一下你指的是哪个产品、业务场景或操作目标，我再继续回答。",
	},
	{
		IntentID: "incomplete.en-US", Locale: "en-US", Exact: []string{"how do i do this", "can it do that", "what should i do"},
		Class: standardAnswerClarification, Text: "Please tell me which product, business scenario, or outcome you mean so I can answer precisely.",
	},
}

type bilingualFactualAnswer struct {
	ID         string
	ZHQuestion string
	ZHAnswer   string
	ENQuestion string
	ENAnswer   string
	Source     string
	ZHAliases  []string
	ENAliases  []string
}

var expandedWebsiteFAQDefinitions = []bilingualFactualAnswer{
	{ID: "mission-brief", ZHQuestion: "什么是 Mission Brief", ZHAnswer: "Mission Brief 用来说明业务目标、范围、背景、期望结果和验收条件，为后续任务拆分和参与者协作提供共同起点。", ENQuestion: "what is a mission brief", ENAnswer: "A Mission Brief states the business goal, scope, context, expected outcome, and acceptance conditions so task planning and participants start from the same definition.", Source: overviewSource},
	{ID: "mission-task-breakdown", ZHQuestion: "mission 如何拆分任务", ZHAnswer: "Mission 可拆成边界清楚、能够验收的任务，并为每项任务明确责任人、输入输出、依赖、约束和完成标准。", ENQuestion: "how does a mission break down work", ENAnswer: "A Mission can be divided into bounded, verifiable tasks with an owner, inputs, outputs, dependencies, constraints, and completion criteria.", Source: overviewSource},
	{ID: "mission-participants", ZHQuestion: "谁可以参与 mission", ZHAnswer: "人员和数字员工都可以参与 Mission；具体角色、职责、知识范围和工具权限由 Mission 配置与组织治理共同确定。", ENQuestion: "who can participate in a mission", ENAnswer: "People and digital workers can participate in a Mission. Their roles, responsibilities, knowledge scope, and tool permissions are governed by the Mission and organization settings.", Source: overviewSource},
	{ID: "task-inputs-outputs", ZHQuestion: "任务需要定义哪些输入和输出", ZHAnswer: "任务应明确必要资料、上下文和前置条件作为输入，并说明预期产物、格式、质量要求和交付位置作为输出。", ENQuestion: "what task inputs and outputs should be defined", ENAnswer: "A task should define required material, context, and prerequisites as inputs, plus the expected artifact, format, quality requirements, and delivery location as outputs.", Source: overviewSource},
	{ID: "acceptance-criteria", ZHQuestion: "如何定义任务验收标准", ZHAnswer: "验收标准应对应业务目标，写清可检查的完成条件、质量要求、必要证据和最终确认人，避免只用主观描述判断完成。", ENQuestion: "how are task acceptance criteria defined", ENAnswer: "Acceptance criteria should tie to the business goal and state verifiable completion conditions, quality requirements, required evidence, and the final approver.", Source: overviewSource},
	{ID: "execution-evidence", ZHQuestion: "什么是执行证据", ZHAnswer: "执行证据是用于说明任务如何完成的可检查记录，例如输入引用、工具调用结果、产物版本、质量检查和审批记录。", ENQuestion: "what is execution evidence", ENAnswer: "Execution evidence is reviewable proof of how work was completed, such as input references, tool results, artifact versions, quality checks, and approval records.", Source: overviewSource},
	{ID: "human-approval-checkpoints", ZHQuestion: "哪些环节可以设置人工审批", ZHAnswer: "可以在敏感工具调用、外部写入、关键决策、结果发布或最终验收前设置人工审批；具体节点取决于任务风险和系统权限。", ENQuestion: "where can human approval checkpoints be added", ENAnswer: "Human approval can be required before sensitive tool calls, external writes, key decisions, publication, or final acceptance, depending on task risk and system permissions.", Source: overviewSource},
	{ID: "quality-checks", ZHQuestion: "orrery 如何进行质量检查", ZHAnswer: "Orrery 可按任务约定检查产物完整性、格式、证据和验收条件，并把自动检查与人工复核结果保留在执行记录中。", ENQuestion: "how does orrery perform quality checks", ENAnswer: "Orrery can check artifact completeness, format, evidence, and acceptance conditions, while retaining automated and human review results in the execution record.", Source: overviewSource},
	{ID: "controlled-pause", ZHQuestion: "可以暂停数字员工的工作吗", ZHAnswer: "可以在配置允许的执行边界内暂停或停止数字员工工作，并保留当前状态和记录；具体控制能力取决于任务运行方式和连接器。", ENQuestion: "can digital worker activity be paused", ENAnswer: "Digital worker activity can be paused or stopped within configured execution boundaries while preserving state and records. Exact controls depend on the runtime and connector.", Source: overviewSource},
	{ID: "mission-collaboration", ZHQuestion: "mission 中如何协作讨论", ZHAnswer: "Mission 可围绕目标、任务、证据和待决事项组织讨论，让人员与数字员工在明确职责下同步进展、提出问题并记录决策。", ENQuestion: "how do participants collaborate in a mission", ENAnswer: "Mission discussions organize goals, tasks, evidence, and open decisions so people and digital workers can coordinate progress, raise issues, and record decisions within clear responsibilities.", Source: overviewSource},
	{ID: "website-knowledge-scope", ZHQuestion: "官网 ai 顾问可以读取哪些知识", ZHAnswer: "官网顾问只读取为官网接待审核发布的公开资料；登录客户可增加已审核的客户指南，但仍不能读取 Orrery 租户业务数据。", ENQuestion: "what knowledge can the website ai advisor read", ENAnswer: "The website advisor reads approved public reception material. Signed-in customers may also use approved customer guides, but the advisor still cannot access Orrery tenant business data.", Source: overviewSource},
	{ID: "knowledge-governance", ZHQuestion: "orrery 如何治理知识", ZHAnswer: "Orrery 通过知识范围、来源引用、版本、权限和审核状态控制智能体可读取的内容，并把知识变更纳入可追踪流程。", ENQuestion: "how does orrery govern knowledge", ENAnswer: "Orrery governs agent-readable content through knowledge scope, source references, versions, permissions, and review status, with traceable knowledge changes.", Source: overviewSource},
	{ID: "agent-working-memory", ZHQuestion: "数字员工如何使用工作记忆", ZHAnswer: "工作记忆用于保留当前任务或会话所需的上下文，范围和生命周期由配置控制，不等同于无限制的长期记忆。", ENQuestion: "how do digital workers use working memory", ENAnswer: "Working memory retains context needed for the current task or session. Its scope and lifetime are controlled and are not equivalent to unrestricted long-term memory.", Source: overviewSource},
	{ID: "website-knowledge-tiers", ZHQuestion: "登录前后的知识范围有什么区别", ZHAnswer: "未登录访客只使用公开产品知识；官网客户登录后可使用已审核的客户指南，但登录身份不会继承任何 Orrery 租户权限。", ENQuestion: "how does website knowledge access change after sign-in", ENAnswer: "Guests use public product knowledge only. Signed-in website customers may use approved customer guides, but website identity never inherits Orrery tenant permissions.", Source: overviewSource},
	{ID: "existing-knowledge-base", ZHQuestion: "可以接入现有知识库吗", ZHAnswer: "可以通过受控导入、检索接口或连接器使用现有知识库；接入前需要确认来源、权限、更新方式和可审计范围。", ENQuestion: "can orrery use an existing knowledge base", ENAnswer: "An existing knowledge base can be used through governed ingestion, search interfaces, or connectors after confirming sources, permissions, update flow, and audit scope.", Source: integrationSource},
	{ID: "mcp-resource-registration", ZHQuestion: "如何在 orrery 中注册 mcp 资源", ZHAnswer: "注册 MCP 资源时需要定义服务和工具、连接方式、权限范围、风险级别与可用性，再由管理员完成审核和绑定。", ENQuestion: "how are mcp resources registered in orrery", ENAnswer: "MCP registration defines the service and tools, connection method, permission scope, risk level, and availability before administrator review and binding.", Source: integrationSource},
	{ID: "tool-binding", ZHQuestion: "如何给数字员工绑定工具", ZHAnswer: "工具通过明确的工具引用和权限范围绑定到数字员工；未绑定或未授权的工具不能被该智能体调用。", ENQuestion: "how are tools bound to a digital worker", ENAnswer: "Tools are bound to a digital worker through explicit tool references and permission scopes. Unbound or unauthorized tools cannot be invoked by that agent.", Source: integrationSource},
	{ID: "connector-authentication", ZHQuestion: "连接器如何认证", ZHAnswer: "连接器可按目标系统使用 OAuth、令牌、密钥或其他受支持方式认证；凭据由服务端安全保存，不应传入官网对话。", ENQuestion: "how are connectors authenticated", ENAnswer: "Connectors use a target system's supported method such as OAuth, tokens, or keys. Credentials are stored server-side and must not be entered into website chat.", Source: integrationSource},
	{ID: "connector-read-write-scope", ZHQuestion: "如何限制连接器的读写权限", ZHAnswer: "连接器应按最小权限分别声明可读资源、可写动作和审批要求，并由 Orrery 与目标系统双方的权限共同约束。", ENQuestion: "how is connector read and write access limited", ENAnswer: "Connector access should declare least-privilege readable resources, writable actions, and approval requirements, constrained by both Orrery and target-system permissions.", Source: integrationSource},
	{ID: "integration-prerequisites", ZHQuestion: "接入现有系统前需要准备什么", ZHAnswer: "通常需要确认目标系统接口、认证方式、测试账号或环境、必要读写范围、数据边界和审计要求，再进行受控联调。", ENQuestion: "what is needed before integrating an existing system", ENAnswer: "Integration normally requires confirmed interfaces, authentication, a test account or environment, required read/write scope, data boundaries, and audit requirements before controlled testing.", Source: integrationSource},
	{ID: "source-system-retention", ZHQuestion: "接入 orrery 后还需要保留原系统吗", ZHAnswer: "通常需要。Orrery 连接并协调现有系统，不默认替代其业务数据和主记录职责；是否迁移由具体架构和治理要求决定。", ENQuestion: "do existing systems remain after adopting orrery", ENAnswer: "Usually yes. Orrery connects and coordinates existing systems rather than automatically replacing their business data or system-of-record role. Migration depends on the approved architecture.", Source: integrationSource},
	{ID: "scheduled-work", ZHQuestion: "orrery 支持定时任务吗", ZHAnswer: "支持按一次性时间或 Cron 计划触发受控任务，并记录每次运行的状态、结果和失败信息。", ENQuestion: "does orrery support scheduled work", ENAnswer: "Orrery supports controlled work triggered once or by a Cron schedule and records each run's status, result, and failure information.", Source: overviewSource},
	{ID: "recurring-work", ZHQuestion: "数字员工可以执行周期性工作吗", ZHAnswer: "可以把边界明确的重复工作配置为周期任务，由指定数字员工执行，并应用并发、重试、权限和人工审批策略。", ENQuestion: "can digital workers perform recurring work", ENAnswer: "Bounded repeatable work can run on a schedule through a designated digital worker with concurrency, retry, permission, and human-approval policies.", Source: overviewSource},
	{ID: "digital-worker-responsibilities", ZHQuestion: "如何定义数字员工的职责", ZHAnswer: "通过角色说明、责任边界、可读知识、可用工具、预期产物和禁止事项共同定义数字员工职责。", ENQuestion: "how are digital worker responsibilities defined", ENAnswer: "A digital worker's responsibilities are defined through its role, responsibility boundary, readable knowledge, available tools, expected outputs, and prohibited actions.", Source: overviewSource},
	{ID: "digital-worker-tool-permissions", ZHQuestion: "如何限制数字员工使用工具", ZHAnswer: "只向数字员工绑定完成职责所需的工具和权限，对高风险调用增加审批，并对调用过程保留记录。", ENQuestion: "how is digital worker tool access limited", ENAnswer: "Bind only the tools and permissions needed for the role, add approval to high-risk calls, and retain invocation records.", Source: overviewSource},
	{ID: "digital-worker-evaluation", ZHQuestion: "如何评估数字员工的表现", ZHAnswer: "可依据任务完成情况、产物质量、证据完整性、规则遵守和人工验收结果进行评估，评价标准应与具体职责一致。", ENQuestion: "how are digital workers evaluated", ENAnswer: "Digital workers can be evaluated by task completion, artifact quality, evidence completeness, policy compliance, and human acceptance against role-specific criteria.", Source: overviewSource},
	{ID: "data-minimization", ZHQuestion: "orrery 是否支持最小化使用数据", ZHAnswer: "支持按任务只开放必要的数据、字段和时间范围，并结合权限、脱敏和审计减少不必要的数据暴露。", ENQuestion: "does orrery support data minimization", ENAnswer: "Orrery can limit work to necessary data, fields, and time ranges, combining permissions, redaction, and audit controls to reduce unnecessary exposure.", Source: overviewSource},
	{ID: "data-boundaries", ZHQuestion: "如何设置数据访问边界", ZHAnswer: "数据边界由知识范围、资源权限、连接器读写范围、组织角色和任务上下文共同限定，具体规则需按企业配置。", ENQuestion: "how are data access boundaries set", ENAnswer: "Data boundaries are constrained by knowledge scope, resource permissions, connector read/write scope, organization roles, and task context, according to enterprise configuration.", Source: overviewSource},
	{ID: "deployment-selection", ZHQuestion: "如何选择部署方式", ZHAnswer: "应根据数据边界、网络连通、合规要求、运维能力、可用性目标和连接器位置，在云端托管、私有化或本地方案中选择。", ENQuestion: "how should an orrery deployment model be selected", ENAnswer: "Choose cloud-hosted, private, or local deployment based on data boundaries, network connectivity, compliance, operational capability, availability goals, and connector location.", Source: overviewSource},
	{ID: "private-deployment-prerequisites", ZHQuestion: "私有化部署需要准备什么", ZHAnswer: "通常需要准备计算与存储资源、数据库、域名和证书、网络和防火墙策略、密钥管理、备份监控以及目标系统连通条件。", ENQuestion: "what is needed for a private deployment", ENAnswer: "Private deployment normally requires compute and storage, databases, domains and certificates, network and firewall policy, secret management, backup and monitoring, and target-system connectivity.", Source: overviewSource},
	{ID: "sensitive-action-approval", ZHQuestion: "敏感操作可以要求人工确认吗", ZHAnswer: "可以。外部写入、发布、删除、付款或其他高风险动作可在执行前要求具备相应权限的人员明确批准。", ENQuestion: "can sensitive actions require human confirmation", ENAnswer: "Yes. External writes, publication, deletion, payment, or other high-risk actions can require explicit approval from an authorized person before execution.", Source: overviewSource},
	{ID: "ontology-vs-database", ZHQuestion: "ontology 和数据库有什么区别", ZHAnswer: "数据库主要保存数据；Ontology 进一步定义业务对象、属性、关系、规则和动作语义，帮助系统和智能体一致理解这些数据。", ENQuestion: "how is an ontology different from a database", ENAnswer: "A database primarily stores data. An Ontology also defines the meaning of business objects, properties, relations, rules, and actions so systems and agents interpret that data consistently.", Source: overviewSource},
	{ID: "ontology-objects-relations", ZHQuestion: "ontology 中的对象和关系是什么", ZHAnswer: "对象表示企业中的业务实体，关系表示实体之间有业务含义的连接；二者都需要明确类型、属性、来源和治理规则。", ENQuestion: "what are objects and relations in an ontology", ENAnswer: "Objects represent business entities and relations represent meaningful connections between them. Both require defined types, properties, sources, and governance rules.", Source: overviewSource},
	{ID: "iris-interaction-scope", ZHQuestion: "iris 可以做什么", ZHAnswer: "Iris 用于通过自然交互访问获准的企业知识和智能体工作流，具体可见内容与可执行操作受当前身份和权限限制。", ENQuestion: "what can iris do", ENAnswer: "Iris provides natural interaction with approved enterprise knowledge and agent workflows. Visible content and available actions remain limited by the current identity and permissions.", Source: overviewSource},
	{ID: "iris-prediction-boundary", ZHQuestion: "iris 是预测模型吗", ZHAnswer: "Iris 的公开定位是智能交互能力，不应默认视为预测模型；预测或评分需要另行配置经过治理的模型、数据和评估流程。", ENQuestion: "is iris a prediction model", ENAnswer: "Iris is publicly positioned as an intelligent interaction capability, not automatically as a prediction model. Prediction or scoring requires separately governed models, data, and evaluation.", Source: overviewSource},
	{ID: "pov-scenario-selection", ZHQuestion: "如何选择 pov 场景", ZHAnswer: "优先选择价值明确、边界可控、必要数据可获得、结果可衡量且能在真实流程中验证的场景。", ENQuestion: "how should a pov scenario be selected", ENAnswer: "Select a scenario with clear value, controlled scope, available required data, measurable outcomes, and a real workflow in which the result can be validated.", Source: overviewSource},
	{ID: "pov-acceptance", ZHQuestion: "pov 如何验收", ZHAnswer: "PoV 应在开始前约定业务指标、产物、质量标准、必要证据和验收人，结束时按同一标准复核结果与后续条件。", ENQuestion: "how is a pov accepted", ENAnswer: "Before a PoV starts, agree on business measures, artifacts, quality criteria, required evidence, and approvers, then review outcomes and rollout conditions against those same criteria.", Source: overviewSource},
}

func expandedWebsiteStandardAnswers() []standardAnswer {
	answers := make([]standardAnswer, 0, len(expandedWebsiteFAQDefinitions)*2)
	for _, definition := range expandedWebsiteFAQDefinitions {
		answers = append(answers,
			standardAnswer{
				IntentID: definition.ID + ".zh-CN", Locale: "zh-CN",
				Exact: append([]string{definition.ZHQuestion}, definition.ZHAliases...),
				Text:  definition.ZHAnswer, Sources: []string{definition.Source}, Class: standardAnswerFactual,
			},
			standardAnswer{
				IntentID: definition.ID + ".en-US", Locale: "en-US",
				Exact: append([]string{definition.ENQuestion}, definition.ENAliases...),
				Text:  definition.ENAnswer, Sources: []string{definition.Source}, Class: standardAnswerFactual,
			},
		)
	}
	return answers
}

var websiteStandardAnswers = append(append([]standardAnswer(nil), baseWebsiteStandardAnswers...), expandedWebsiteStandardAnswers()...)

func matchStandardAnswer(message, locale string) (standardAnswer, bool) {
	return matchStandardAnswerCatalog(websiteStandardAnswers, message, locale)
}

func matchStandardAnswerCatalog(catalog []standardAnswer, message, locale string) (standardAnswer, bool) {
	normalized := normalizeStandardAnswerText(message)
	if normalized == "" {
		return standardAnswer{}, false
	}
	preferred := normalizeStandardAnswerLocale(locale)
	for pass := 0; pass < 2; pass++ {
		for _, answer := range catalog {
			if (pass == 0) != (answer.Locale == preferred) {
				continue
			}
			for _, exact := range answer.Exact {
				if normalized == normalizeStandardAnswerText(exact) {
					return answer, true
				}
			}
		}
	}
	if utf8.RuneCountInString(normalized) > standardAnswerPatternMaxRunes {
		return standardAnswer{}, false
	}
	for pass := 0; pass < 2; pass++ {
		for _, answer := range catalog {
			if (pass == 0) != (answer.Locale == preferred) {
				continue
			}
			for _, pattern := range answer.Patterns {
				matched, err := regexp.MatchString(pattern, normalized)
				if err == nil && matched {
					return answer, true
				}
			}
		}
	}
	return standardAnswer{}, false
}

func validateStandardAnswerCatalog(catalog []standardAnswer) error {
	seenIntents := map[string]struct{}{}
	seenExact := map[string]string{}
	for _, answer := range catalog {
		if strings.TrimSpace(answer.IntentID) == "" {
			return fmt.Errorf("standard answer intent ID is required")
		}
		if _, exists := seenIntents[answer.IntentID]; exists {
			return fmt.Errorf("duplicate standard answer intent ID %q", answer.IntentID)
		}
		seenIntents[answer.IntentID] = struct{}{}
		if answer.Locale != "zh-CN" && answer.Locale != "en-US" {
			return fmt.Errorf("standard answer %q has unsupported locale %q", answer.IntentID, answer.Locale)
		}
		if strings.TrimSpace(answer.Text) == "" || utf8.RuneCountInString(answer.Text) > defaultMaxMessageRunes {
			return fmt.Errorf("standard answer %q has invalid text", answer.IntentID)
		}
		lowerText := strings.ToLower(answer.Text)
		for _, marker := range []string{"system prompt", "tenant_id", "access_token", "private workspace"} {
			if strings.Contains(lowerText, marker) {
				return fmt.Errorf("standard answer %q contains forbidden marker", answer.IntentID)
			}
		}
		if answer.Class == standardAnswerFactual && len(answer.Sources) == 0 {
			return fmt.Errorf("factual standard answer %q has no source", answer.IntentID)
		}
		for _, source := range answer.Sources {
			if !strings.HasPrefix(source, "minviabrain://") {
				return fmt.Errorf("standard answer %q has unapproved source", answer.IntentID)
			}
		}
		for _, exact := range answer.Exact {
			normalized := normalizeStandardAnswerText(exact)
			if normalized == "" {
				return fmt.Errorf("standard answer %q has empty exact phrase", answer.IntentID)
			}
			key := answer.Locale + "\x00" + normalized
			if existing := seenExact[key]; existing != "" {
				return fmt.Errorf("standard answers %q and %q share exact phrase", existing, answer.IntentID)
			}
			seenExact[key] = answer.IntentID
		}
		for _, pattern := range answer.Patterns {
			if !strings.HasPrefix(pattern, "^") || !strings.HasSuffix(pattern, "$") || utf8.RuneCountInString(pattern) > standardAnswerPatternMaxRunes {
				return fmt.Errorf("standard answer %q has invalid pattern %q", answer.IntentID, pattern)
			}
			if _, err := regexp.Compile(pattern); err != nil {
				return fmt.Errorf("standard answer %q pattern: %w", answer.IntentID, err)
			}
		}
	}
	return nil
}

func normalizeStandardAnswerText(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	value = strings.Join(strings.Fields(value), " ")
	value = strings.TrimSpace(strings.TrimRightFunc(value, func(r rune) bool {
		return unicode.IsSpace(r) || strings.ContainsRune("?？!！.。", r)
	}))
	return value
}

func normalizeStandardAnswerLocale(locale string) string {
	if strings.HasPrefix(strings.ToLower(strings.TrimSpace(locale)), "en") {
		return "en-US"
	}
	return "zh-CN"
}
