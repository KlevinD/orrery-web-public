package website

import "strings"

type WebsiteCommercialPolicyDecision string

const (
	CommercialPolicyAllow           WebsiteCommercialPolicyDecision = "allow_public_answer"
	CommercialPolicyPublicPrinciple WebsiteCommercialPolicyDecision = "allow_public_principle_with_lead"
	CommercialPolicyRefuse          WebsiteCommercialPolicyDecision = "refuse_and_lead"
	CommercialPolicyBlockCandidate  WebsiteCommercialPolicyDecision = "block_candidate"
)

type WebsiteCommercialPolicyResult struct {
	Decision WebsiteCommercialPolicyDecision
	Text     string
	Action   string
}

func evaluateWebsiteCommercialPolicy(question, answer, locale string) WebsiteCommercialPolicyResult {
	question = strings.ToLower(strings.TrimSpace(question))
	answer = strings.ToLower(strings.TrimSpace(answer))
	if containsWebsitePolicyPhrase(question, websiteProtectedQuestionSubjects) && containsWebsitePolicyPhrase(question, websiteProtectedQuestionActions) ||
		containsWebsitePolicyPhrase(answer, websiteBlockedAnswerPhrases) {
		return websiteProtectedPolicyResult(CommercialPolicyRefuse, locale)
	}
	if answer != "" && containsWebsitePolicyPhrase(answer, websiteSecretAnswerPhrases) {
		return websiteProtectedPolicyResult(CommercialPolicyBlockCandidate, locale)
	}
	commercialQuestion := strings.ReplaceAll(question, "task contract", "")
	if containsWebsitePolicyPhrase(commercialQuestion, websiteCommercialQuestionPhrases) || containsWebsitePolicyPhrase(answer, websiteCommitmentAnswerPhrases) {
		return websiteProtectedPolicyResult(CommercialPolicyPublicPrinciple, locale)
	}
	return WebsiteCommercialPolicyResult{Decision: CommercialPolicyAllow}
}

func websiteProtectedPolicyResult(decision WebsiteCommercialPolicyDecision, locale string) WebsiteCommercialPolicyResult {
	english := normalizeStandardAnswerLocale(locale) == "en-US"
	if decision == CommercialPolicyRefuse || decision == CommercialPolicyBlockCandidate {
		text := "我不能提供客户数据、内部路线图、凭据、私有地址或未公开信息。你可以提交咨询信息，由 Minvia 顾问在合适的保密边界内进一步沟通。"
		if english {
			text = "I cannot provide customer data, internal roadmaps, credentials, private addresses, or unpublished information. You can submit an enquiry for follow-up within the appropriate confidentiality boundary."
		}
		return WebsiteCommercialPolicyResult{Decision: decision, Text: text, Action: "open_lead_form"}
	}
	text := "具体价格、合同条款、交付范围、竞品结论及客户案例需要结合实际需求，由 Minvia 顾问依据已公开信息确认。你可以提交咨询信息，我们会安排进一步沟通。"
	if english {
		text = "Specific pricing, contract terms, delivery scope, competitor conclusions, and customer cases depend on the actual requirements and must be confirmed by a Minvia advisor using approved public information. You can submit an enquiry for follow-up."
	}
	return WebsiteCommercialPolicyResult{Decision: decision, Text: text, Action: "open_lead_form"}
}

func containsWebsitePolicyPhrase(value string, phrases []string) bool {
	for _, phrase := range phrases {
		if strings.Contains(value, phrase) {
			return true
		}
	}
	return false
}

var websiteProtectedQuestionSubjects = []string{
	"客户数据", "租户数据", "其他客户", "未公开路线图", "内部路线图", "生产环境 token", "生产环境token",
	"密码", "凭据", "私有地址", "private address", "customer data", "tenant data", "internal roadmap",
	"unpublished roadmap", "credential", "password", "api key", "apikey", "access token",
}

var websiteProtectedQuestionActions = []string{
	"给我", "发给我", "告诉我", "读取", "导出", "下载", "展示", "披露", "提供给我", "访问其他", "获取其他", "拿到",
	"give me", "send me", "tell me", "read other", "read customer", "read tenant", "export", "download", "show me",
	"disclose", "provide me", "access other", "access customer", "access tenant", "retrieve other", "retrieve customer", "retrieve tenant",
}

var websiteCommercialQuestionPhrases = []string{
	"具体价格", "价格是多少", "价格多少", "多少钱", "折扣是多少", "折扣多少", "怎么收费", "如何收费", "收费标准",
	"给个报价", "给我报价", "报个价", "报价是多少", "承诺合同", "合同 sla", "合同条款", "签约条款", "上线时间",
	"交付时间", "具体交付范围", "保证", "承诺", "竞争对手", "竞品", "客户案例",
	"specific price", "how much", "pricing details", "discount", "quote price", "contract terms", "commit to", "go-live date",
	"delivery date", "delivery commitment", "guarantee", "competitor", "customer case",
}

var websiteSecretAnswerPhrases = []string{
	"sk-", "api_key", "api-key", "access_token", "bearer ", "private key", "begin private key",
}

var websiteBlockedAnswerPhrases = []string{
	"客户租户数据", "其他客户的数据", "未公开路线图", "内部路线图", "生产环境密码", "生产环境 token",
	"customer tenant data", "unpublished roadmap", "internal roadmap", "production password",
}

var websiteCommitmentAnswerPhrases = []string{
	"保证节省", "保证准确", "保证上线", "承诺上线", "承诺交付", "保证合规", "guarantee savings", "guaranteed accuracy",
	"guaranteed delivery", "guaranteed compliance",
}
