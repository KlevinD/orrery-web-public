package feishu

type URLVerification struct {
	Token     string
	Challenge string
}

type WebhookHandler struct {
	client  Client
	options Options
}

func NewWebhookHandler(client Client, opts Options) *WebhookHandler {
	return &WebhookHandler{client: client, options: opts}
}

func (h *WebhookHandler) VerifyURL(req URLVerification) (string, bool) {
	if h.options.VerifyToken != "" && req.Token != h.options.VerifyToken {
		return "", false
	}
	return req.Challenge, true
}
