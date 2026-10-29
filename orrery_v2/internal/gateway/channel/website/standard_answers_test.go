package website

import (
	"net/http"
	"strings"
	"testing"
)

func TestStandardAnswerCatalogCoversPublicCustomerServiceIntents(t *testing.T) {
	tests := []struct {
		message string
		locale  string
		class   standardAnswerClass
		action  string
	}{
		{message: "你好！", locale: "zh-CN", class: standardAnswerConversational},
		{message: "hello", locale: "en-US", class: standardAnswerConversational},
		{message: "谢谢", locale: "zh-CN", class: standardAnswerConversational},
		{message: "再见", locale: "zh-CN", class: standardAnswerConversational},
		{message: "重新开始", locale: "zh-CN", class: standardAnswerConversational},
		{message: "你能做什么？", locale: "zh-CN", class: standardAnswerFactual},
		{message: "Minvia 是什么？", locale: "zh-CN", class: standardAnswerFactual},
		{message: "Minvia是什么？", locale: "zh-CN", class: standardAnswerFactual},
		{message: "Orrery 是什么？", locale: "zh-CN", class: standardAnswerFactual},
		{message: "Orrery是什么？", locale: "zh-CN", class: standardAnswerFactual},
		{message: "Mission 是什么？", locale: "zh-CN", class: standardAnswerFactual},
		{message: "数字员工是什么？", locale: "zh-CN", class: standardAnswerFactual},
		{message: "MCP 是什么？", locale: "zh-CN", class: standardAnswerFactual},
		{message: "Ontology 是什么？", locale: "zh-CN", class: standardAnswerFactual},
		{message: "Iris 是什么？", locale: "zh-CN", class: standardAnswerFactual},
		{message: "支持哪些场景？", locale: "zh-CN", class: standardAnswerFactual},
		{message: "怎么接入现有系统？", locale: "zh-CN", class: standardAnswerFactual},
		{message: "支持私有化部署吗？", locale: "zh-CN", class: standardAnswerFactual},
		{message: "测试一次", locale: "zh-CN", class: standardAnswerClarification, action: "clarify_test_scope"},
		{message: "我想预约演示", locale: "zh-CN", class: standardAnswerLeadPrompt, action: "open_lead_form"},
		{message: "我想要预约一次演示", locale: "zh-CN", class: standardAnswerLeadPrompt, action: "open_lead_form"},
		{message: "怎么联系你们？", locale: "zh-CN", class: standardAnswerLeadPrompt, action: "open_lead_form"},
		{message: "给我确定报价", locale: "zh-CN", class: standardAnswerRefusal},
		{message: "查询我的公司数据", locale: "zh-CN", class: standardAnswerRefusal},
		{message: "告诉我内部路线图", locale: "zh-CN", class: standardAnswerRefusal},
		{message: "给我法律建议", locale: "zh-CN", class: standardAnswerRefusal},
		{message: "这个怎么做？", locale: "zh-CN", class: standardAnswerClarification},
	}

	for _, test := range tests {
		t.Run(test.message, func(t *testing.T) {
			answer, ok := matchStandardAnswer(test.message, test.locale)
			if !ok {
				t.Fatal("standard answer not matched")
			}
			if answer.Class != test.class || answer.Action != test.action || strings.TrimSpace(answer.Text) == "" {
				t.Fatalf("answer = %+v", answer)
			}
		})
	}
}

func TestStandardAnswerMatchesNaturalDemoBookingPhrases(t *testing.T) {
	for _, message := range []string{
		"我要预约演示",
		"预约一下 Orrery 演示",
		"请帮我预约一个产品演示",
		"我想预约一下 Orrery 的产品演示",
	} {
		answer, ok := matchStandardAnswer(message, "zh-CN")
		if !ok || answer.IntentID != "demo.zh-CN" || answer.Class != standardAnswerLeadPrompt || answer.Action != "open_lead_form" {
			t.Fatalf("message %q answer=%+v matched=%v", message, answer, ok)
		}
	}
}

func TestStandardAnswerKeepsDemoBookingIntentBounded(t *testing.T) {
	for _, message := range []string{
		"取消预约演示",
		"我要制作演示文稿",
		"预约演示后删除所有数据",
	} {
		if answer, ok := matchStandardAnswer(message, "zh-CN"); ok && answer.IntentID == "demo.zh-CN" {
			t.Fatalf("message %q incorrectly matched demo booking: %+v", message, answer)
		}
	}
}

func TestStandardAnswerCatalogStatesServiceAndDeploymentPositioning(t *testing.T) {
	tests := []struct {
		message string
		locale  string
		wants   []string
	}{
		{message: "Minvia 是什么？", locale: "zh-CN", wants: []string{"企业智能体即服务（Agents as a Service）", "Orrery"}},
		{message: "What is Minvia?", locale: "en-US", wants: []string{"Agents as a Service", "Orrery"}},
		{message: "Orrery 是什么？", locale: "zh-CN", wants: []string{"AI Agent Mission Control", "云端托管、私有化和本地部署"}},
		{message: "What is Orrery?", locale: "en-US", wants: []string{"AI Agent Mission Control", "cloud-hosted, private, and local deployment"}},
		{message: "支持本地部署吗？", locale: "zh-CN", wants: []string{"支持云端托管、私有化和本地部署"}},
	}

	for _, test := range tests {
		t.Run(test.locale+"/"+test.message, func(t *testing.T) {
			answer, ok := matchStandardAnswer(test.message, test.locale)
			if !ok {
				t.Fatal("standard answer not matched")
			}
			for _, want := range test.wants {
				if !strings.Contains(answer.Text, want) {
					t.Fatalf("answer %q does not contain %q", answer.Text, want)
				}
			}
			if strings.Contains(answer.Text, "SaaS") {
				t.Fatalf("answer retains retired SaaS wording: %q", answer.Text)
			}
		})
	}
}

func TestStandardAnswerCatalogCoversBasicPresalesIntents(t *testing.T) {
	tests := []struct {
		message  string
		locale   string
		intentID string
	}{
		{message: "Orrery 和普通聊天机器人有什么区别？", locale: "zh-CN", intentID: "chatbot-difference.zh-CN"},
		{message: "How is Orrery different from a chatbot?", locale: "en-US", intentID: "chatbot-difference.en-US"},
		{message: "什么场景适合使用 Orrery？", locale: "zh-CN", intentID: "suitable-scenarios.zh-CN"},
		{message: "What scenarios are suitable for Orrery?", locale: "en-US", intentID: "suitable-scenarios.en-US"},
		{message: "怎么开始使用 Orrery？", locale: "zh-CN", intentID: "getting-started.zh-CN"},
		{message: "How do I get started with Orrery?", locale: "en-US", intentID: "getting-started.en-US"},
		{message: "什么是 PoV？", locale: "zh-CN", intentID: "pov-definition.zh-CN"},
		{message: "What is a Proof of Value?", locale: "en-US", intentID: "pov-definition.en-US"},
		{message: "PoV 需要我们投入什么？", locale: "zh-CN", intentID: "pov-inputs.zh-CN"},
		{message: "What do we need to provide for a PoV?", locale: "en-US", intentID: "pov-inputs.en-US"},
		{message: "Orrery 会替换现有系统吗？", locale: "zh-CN", intentID: "replace-existing-systems.zh-CN"},
		{message: "Does Orrery replace existing systems?", locale: "en-US", intentID: "replace-existing-systems.en-US"},
		{message: "真人可以和数字员工一起工作吗？", locale: "zh-CN", intentID: "human-collaboration.zh-CN"},
		{message: "Can people work with digital workers?", locale: "en-US", intentID: "human-collaboration.en-US"},
		{message: "什么是 Task Contract？", locale: "zh-CN", intentID: "task-contract.zh-CN"},
		{message: "What is a Task Contract?", locale: "en-US", intentID: "task-contract.en-US"},
		{message: "Orrery 如何保证交付质量？", locale: "zh-CN", intentID: "delivery-quality.zh-CN"},
		{message: "How does Orrery support delivery quality?", locale: "en-US", intentID: "delivery-quality.en-US"},
		{message: "Orrery 如何控制权限？", locale: "zh-CN", intentID: "permissions-approvals.zh-CN"},
		{message: "How does Orrery control permissions?", locale: "en-US", intentID: "permissions-approvals.en-US"},
		{message: "Orrery 的操作可以审计吗？", locale: "zh-CN", intentID: "auditability.zh-CN"},
		{message: "Are Orrery operations auditable?", locale: "en-US", intentID: "auditability.en-US"},
		{message: "Orrery 支持 API 接入吗？", locale: "zh-CN", intentID: "api-access.zh-CN"},
		{message: "Does Orrery support API integration?", locale: "en-US", intentID: "api-access.en-US"},
	}

	for _, test := range tests {
		t.Run(test.intentID, func(t *testing.T) {
			answer, ok := matchStandardAnswer(test.message, test.locale)
			if !ok {
				t.Fatal("standard answer not matched")
			}
			if answer.IntentID != test.intentID || answer.Class != standardAnswerFactual || strings.TrimSpace(answer.Text) == "" {
				t.Fatalf("answer = %+v", answer)
			}
			if len(answer.Sources) == 0 || !strings.HasPrefix(answer.Sources[0], "minviabrain://") {
				t.Fatalf("answer has no approved source: %+v", answer)
			}
		})
	}
}

func TestStandardAnswerCatalogContainsSeventyOneBilingualLogicalIntents(t *testing.T) {
	localesByIntent := map[string]map[string]bool{}
	for _, answer := range websiteStandardAnswers {
		separator := strings.LastIndex(answer.IntentID, ".")
		if separator < 1 {
			t.Fatalf("intent %q has no locale suffix", answer.IntentID)
		}
		logicalID := answer.IntentID[:separator]
		if localesByIntent[logicalID] == nil {
			localesByIntent[logicalID] = map[string]bool{}
		}
		localesByIntent[logicalID][answer.Locale] = true
	}
	if len(localesByIntent) != 71 {
		t.Fatalf("logical intents = %d, want 71", len(localesByIntent))
	}
	for logicalID, locales := range localesByIntent {
		if !locales["zh-CN"] || !locales["en-US"] || len(locales) != 2 {
			t.Fatalf("intent %q locales = %+v", logicalID, locales)
		}
	}
}

func TestStandardAnswerCatalogCoversExpandedFAQIntents(t *testing.T) {
	tests := []struct {
		logicalID string
		zh        string
		en        string
	}{
		{logicalID: "mission-brief", zh: "什么是 Mission Brief？", en: "What is a Mission Brief?"},
		{logicalID: "mission-task-breakdown", zh: "Mission 如何拆分任务？", en: "How does a Mission break down work?"},
		{logicalID: "mission-participants", zh: "谁可以参与 Mission？", en: "Who can participate in a Mission?"},
		{logicalID: "task-inputs-outputs", zh: "任务需要定义哪些输入和输出？", en: "What task inputs and outputs should be defined?"},
		{logicalID: "acceptance-criteria", zh: "如何定义任务验收标准？", en: "How are task acceptance criteria defined?"},
		{logicalID: "execution-evidence", zh: "什么是执行证据？", en: "What is execution evidence?"},
		{logicalID: "human-approval-checkpoints", zh: "哪些环节可以设置人工审批？", en: "Where can human approval checkpoints be added?"},
		{logicalID: "quality-checks", zh: "Orrery 如何进行质量检查？", en: "How does Orrery perform quality checks?"},
		{logicalID: "controlled-pause", zh: "可以暂停数字员工的工作吗？", en: "Can digital worker activity be paused?"},
		{logicalID: "mission-collaboration", zh: "Mission 中如何协作讨论？", en: "How do participants collaborate in a Mission?"},
		{logicalID: "website-knowledge-scope", zh: "官网 AI 顾问可以读取哪些知识？", en: "What knowledge can the website AI advisor read?"},
		{logicalID: "knowledge-governance", zh: "Orrery 如何治理知识？", en: "How does Orrery govern knowledge?"},
		{logicalID: "agent-working-memory", zh: "数字员工如何使用工作记忆？", en: "How do digital workers use working memory?"},
		{logicalID: "website-knowledge-tiers", zh: "登录前后的知识范围有什么区别？", en: "How does website knowledge access change after sign-in?"},
		{logicalID: "existing-knowledge-base", zh: "可以接入现有知识库吗？", en: "Can Orrery use an existing knowledge base?"},
		{logicalID: "mcp-resource-registration", zh: "如何在 Orrery 中注册 MCP 资源？", en: "How are MCP resources registered in Orrery?"},
		{logicalID: "tool-binding", zh: "如何给数字员工绑定工具？", en: "How are tools bound to a digital worker?"},
		{logicalID: "connector-authentication", zh: "连接器如何认证？", en: "How are connectors authenticated?"},
		{logicalID: "connector-read-write-scope", zh: "如何限制连接器的读写权限？", en: "How is connector read and write access limited?"},
		{logicalID: "integration-prerequisites", zh: "接入现有系统前需要准备什么？", en: "What is needed before integrating an existing system?"},
		{logicalID: "source-system-retention", zh: "接入 Orrery 后还需要保留原系统吗？", en: "Do existing systems remain after adopting Orrery?"},
		{logicalID: "scheduled-work", zh: "Orrery 支持定时任务吗？", en: "Does Orrery support scheduled work?"},
		{logicalID: "recurring-work", zh: "数字员工可以执行周期性工作吗？", en: "Can digital workers perform recurring work?"},
		{logicalID: "digital-worker-responsibilities", zh: "如何定义数字员工的职责？", en: "How are digital worker responsibilities defined?"},
		{logicalID: "digital-worker-tool-permissions", zh: "如何限制数字员工使用工具？", en: "How is digital worker tool access limited?"},
		{logicalID: "digital-worker-evaluation", zh: "如何评估数字员工的表现？", en: "How are digital workers evaluated?"},
		{logicalID: "data-minimization", zh: "Orrery 是否支持最小化使用数据？", en: "Does Orrery support data minimization?"},
		{logicalID: "data-boundaries", zh: "如何设置数据访问边界？", en: "How are data access boundaries set?"},
		{logicalID: "deployment-selection", zh: "如何选择部署方式？", en: "How should an Orrery deployment model be selected?"},
		{logicalID: "private-deployment-prerequisites", zh: "私有化部署需要准备什么？", en: "What is needed for a private deployment?"},
		{logicalID: "sensitive-action-approval", zh: "敏感操作可以要求人工确认吗？", en: "Can sensitive actions require human confirmation?"},
		{logicalID: "ontology-vs-database", zh: "Ontology 和数据库有什么区别？", en: "How is an Ontology different from a database?"},
		{logicalID: "ontology-objects-relations", zh: "Ontology 中的对象和关系是什么？", en: "What are objects and relations in an Ontology?"},
		{logicalID: "iris-interaction-scope", zh: "Iris 可以做什么？", en: "What can Iris do?"},
		{logicalID: "iris-prediction-boundary", zh: "Iris 是预测模型吗？", en: "Is Iris a prediction model?"},
		{logicalID: "pov-scenario-selection", zh: "如何选择 PoV 场景？", en: "How should a PoV scenario be selected?"},
		{logicalID: "pov-acceptance", zh: "PoV 如何验收？", en: "How is a PoV accepted?"},
	}

	for _, test := range tests {
		for _, localized := range []struct {
			locale  string
			message string
		}{{locale: "zh-CN", message: test.zh}, {locale: "en-US", message: test.en}} {
			t.Run(test.logicalID+"/"+localized.locale, func(t *testing.T) {
				answer, ok := matchStandardAnswer(localized.message, localized.locale)
				if !ok || answer.IntentID != test.logicalID+"."+localized.locale || answer.Class != standardAnswerFactual {
					t.Fatalf("answer = %+v matched=%v", answer, ok)
				}
				if len(answer.Sources) == 0 || !strings.HasPrefix(answer.Sources[0], "minviabrain://") {
					t.Fatalf("answer has no approved source: %+v", answer)
				}
			})
		}
	}
}

func TestStandardAnswerMatchesBoundedConversationalAliases(t *testing.T) {
	for _, message := range []string{"你好啊", "你好呀", "您好啊", "您好呀", "哈喽", "哈罗", "嗨喽", "在吗"} {
		answer, ok := matchStandardAnswer(message, "zh-CN")
		if !ok || answer.IntentID != "greeting.zh-CN" {
			t.Fatalf("message %q answer=%+v matched=%v", message, answer, ok)
		}
	}
	for _, message := range []string{"hello there", "hi there", "hey there"} {
		answer, ok := matchStandardAnswer(message, "en-US")
		if !ok || answer.IntentID != "greeting.en-US" {
			t.Fatalf("message %q answer=%+v matched=%v", message, answer, ok)
		}
	}
	if answer, ok := matchStandardAnswer("你好啊，Orrery 怎么接入 CRM？", "zh-CN"); ok {
		t.Fatalf("compound greeting matched: %+v", answer)
	}
}

func TestStandardAnswerDoesNotMatchCompoundPresalesQuestion(t *testing.T) {
	for _, test := range []struct {
		message string
		locale  string
	}{
		{message: "Orrery 和聊天机器人有什么区别，怎么接入 CRM？", locale: "zh-CN"},
		{message: "How is Orrery different from a chatbot, and how does it integrate with CRM?", locale: "en-US"},
	} {
		if answer, ok := matchStandardAnswer(test.message, test.locale); ok {
			t.Fatalf("compound question matched a standard answer: %+v", answer)
		}
	}
}

func TestStandardAnswerDoesNotSubstringMatchCompoundQuestion(t *testing.T) {
	if answer, ok := matchStandardAnswer("你好，Orrery 怎么接入现有系统？", "zh-CN"); ok {
		t.Fatalf("compound question matched greeting: %+v", answer)
	}
}

func TestStandardAnswerCatalogIsValidated(t *testing.T) {
	if err := validateStandardAnswerCatalog(websiteStandardAnswers); err != nil {
		t.Fatalf("catalog validation failed: %v", err)
	}
	for _, answer := range websiteStandardAnswers {
		if answer.Class == standardAnswerFactual {
			if len(answer.Sources) == 0 {
				t.Fatalf("factual answer %q has no sources", answer.IntentID)
			}
			for _, source := range answer.Sources {
				if !strings.HasPrefix(source, "minviabrain://") {
					t.Fatalf("factual answer %q has unapproved source %q", answer.IntentID, source)
				}
			}
		}
		for _, pattern := range answer.Patterns {
			if !strings.HasPrefix(pattern, "^") || !strings.HasSuffix(pattern, "$") {
				t.Fatalf("answer %q pattern is not anchored: %q", answer.IntentID, pattern)
			}
		}
	}
}

func TestHandlerStreamsDeterministicAnswerWithoutCallingGateway(t *testing.T) {
	gateway := &fakeGateway{}
	handler := NewHandler(gateway, Options{AllowedOrigins: []string{"https://www.minvia.cn"}})
	session := createSession(t, handler, "https://www.minvia.cn")

	rec := sendMessage(handler, session.SessionID, session.AccessToken, "https://www.minvia.cn", `{"message":"我想预约演示","language":"zh-CN"}`)

	if rec.Code != http.StatusOK || gateway.inputCount() != 0 {
		t.Fatalf("status = %d gateway calls = %d body = %s", rec.Code, gateway.inputCount(), rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"type":"assistant_text_delta"`) ||
		!strings.Contains(rec.Body.String(), `"type":"turn_completed"`) ||
		!strings.Contains(rec.Body.String(), `"action":"open_lead_form"`) {
		t.Fatalf("deterministic stream = %s", rec.Body.String())
	}
}

func TestHandlerKeepsTestScopeAndNaturalDemoBookingDeterministic(t *testing.T) {
	gateway := &fakeGateway{}
	handler := NewHandler(gateway, Options{AllowedOrigins: []string{"https://www.minvia.cn"}})
	session := createSession(t, handler, "https://www.minvia.cn")

	testScope := sendMessage(handler, session.SessionID, session.AccessToken, "https://www.minvia.cn", `{"message":"测试一次","language":"zh-CN"}`)
	demo := sendMessage(handler, session.SessionID, session.AccessToken, "https://www.minvia.cn", `{"message":"我想要预约一次演示","language":"zh-CN"}`)

	if testScope.Code != http.StatusOK || demo.Code != http.StatusOK || gateway.inputCount() != 0 {
		t.Fatalf("test status=%d demo status=%d gateway calls=%d test=%s demo=%s", testScope.Code, demo.Code, gateway.inputCount(), testScope.Body.String(), demo.Body.String())
	}
	if completed := completedPayload(t, testScope.Body.String()); completed["route"] != "standard_answer" || completed["human_verification_required"] != false {
		t.Fatalf("test completion=%+v body=%s", completed, testScope.Body.String())
	}
	if !strings.Contains(testScope.Body.String(), `"action":"clarify_test_scope"`) {
		t.Fatalf("test stream=%s", testScope.Body.String())
	}
	if completed := completedPayload(t, demo.Body.String()); completed["route"] != "standard_answer" || completed["human_verification_required"] != false {
		t.Fatalf("demo completion=%+v body=%s", completed, demo.Body.String())
	}
	if !strings.Contains(demo.Body.String(), `"action":"open_lead_form"`) {
		t.Fatalf("demo stream=%s", demo.Body.String())
	}
}

func TestHandlerRoutesCompoundQuestionToGateway(t *testing.T) {
	gateway := &fakeGateway{}
	handler := NewHandler(gateway, Options{AllowedOrigins: []string{"https://www.minvia.cn"}})
	session := createSession(t, handler, "https://www.minvia.cn")

	rec := sendMessage(handler, session.SessionID, session.AccessToken, "https://www.minvia.cn", `{"message":"你好，Orrery 怎么接入现有系统？","language":"zh-CN"}`)

	if rec.Code != http.StatusOK || gateway.inputCount() != 1 {
		t.Fatalf("status = %d gateway calls = %d body = %s", rec.Code, gateway.inputCount(), rec.Body.String())
	}
}
