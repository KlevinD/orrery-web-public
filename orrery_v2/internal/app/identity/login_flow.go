package identity

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/google/uuid"
)

const MaxBrowserLoginFlowResponseBytes int64 = 1 << 20

const genericLoginFailureMessage = "登录失败。"

type LoginFlowRequest struct {
	ID     string
	Cookie string
}

type LoginFlowMessage struct {
	ID   int64  `json:"id"`
	Text string `json:"text"`
	Type string `json:"type"`
}

type LoginFlowLabel struct {
	Text string `json:"text"`
}

type LoginFlowNodeAttributes struct {
	Name         string `json:"name"`
	Type         string `json:"type"`
	Value        any    `json:"value,omitempty"`
	Required     bool   `json:"required"`
	Disabled     bool   `json:"disabled"`
	AutoComplete string `json:"autocomplete,omitempty"`
}

type LoginFlowNode struct {
	Type       string                  `json:"type"`
	Group      string                  `json:"group"`
	Attributes LoginFlowNodeAttributes `json:"attributes"`
	Messages   []LoginFlowMessage      `json:"messages"`
	Label      LoginFlowLabel          `json:"label"`
}

type BrowserLoginFlow struct {
	ID        string             `json:"id"`
	ExpiresAt string             `json:"expires_at,omitempty"`
	Action    string             `json:"action"`
	Method    string             `json:"method"`
	Nodes     []LoginFlowNode    `json:"nodes"`
	Messages  []LoginFlowMessage `json:"messages"`
}

type BrowserLoginFlowProvider interface {
	GetBrowserLoginFlow(context.Context, LoginFlowRequest) (BrowserLoginFlow, error)
}

func (p *KratosIdentityProvider) GetBrowserLoginFlow(ctx context.Context, req LoginFlowRequest) (BrowserLoginFlow, error) {
	if err := ctx.Err(); err != nil {
		return BrowserLoginFlow{}, err
	}
	flowID := strings.TrimSpace(req.ID)
	if _, err := uuid.Parse(flowID); err != nil {
		return BrowserLoginFlow{}, fmt.Errorf("%w: login flow id is invalid", ErrInvalidRequest)
	}
	if p == nil || p.publicURL == nil || p.httpClient == nil {
		return BrowserLoginFlow{}, fmt.Errorf("%w: kratos provider is not configured", ErrServiceDependency)
	}

	flowURL := p.resolvePublicPath("/self-service/login/flows")
	query := flowURL.Query()
	query.Set("id", flowID)
	flowURL.RawQuery = query.Encode()
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, flowURL.String(), nil)
	if err != nil {
		return BrowserLoginFlow{}, fmt.Errorf("%w: build kratos login flow request", ErrServiceUnavailable)
	}
	httpReq.Header.Set("Accept", "application/json")
	if cookie := p.kratosLoginFlowCookieHeader(req.Cookie); cookie != "" {
		httpReq.Header.Set("Cookie", cookie)
	}
	resp, err := p.httpClient.Do(httpReq)
	if err != nil {
		return BrowserLoginFlow{}, fmt.Errorf("%w: fetch kratos login flow: %v", ErrServiceUnavailable, err)
	}
	defer resp.Body.Close()

	switch resp.StatusCode {
	case http.StatusOK:
	case http.StatusForbidden:
		return BrowserLoginFlow{}, ErrFlowCookieInvalid
	case http.StatusNotFound:
		return BrowserLoginFlow{}, ErrFlowNotFound
	case http.StatusGone:
		return BrowserLoginFlow{}, ErrFlowExpired
	case http.StatusBadRequest:
		return BrowserLoginFlow{}, fmt.Errorf("%w: kratos login flow request was rejected", ErrInvalidRequest)
	default:
		return BrowserLoginFlow{}, fmt.Errorf("%w: kratos login flow returned status %d", ErrServiceUnavailable, resp.StatusCode)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, MaxBrowserLoginFlowResponseBytes+1))
	if err != nil || int64(len(body)) > MaxBrowserLoginFlowResponseBytes {
		return BrowserLoginFlow{}, fmt.Errorf("%w: kratos login flow response is invalid", ErrServiceUnavailable)
	}
	var wire kratosLoginFlowWire
	if err := json.Unmarshal(body, &wire); err != nil {
		return BrowserLoginFlow{}, fmt.Errorf("%w: decode kratos login flow: %v", ErrServiceUnavailable, err)
	}
	return p.sanitizeLoginFlow(flowID, wire)
}

func (p *KratosIdentityProvider) sanitizeLoginFlow(requestedID string, wire kratosLoginFlowWire) (BrowserLoginFlow, error) {
	wire.ID = strings.TrimSpace(wire.ID)
	if wire.ID != requestedID {
		return BrowserLoginFlow{}, fmt.Errorf("%w: kratos login flow id did not match", ErrInvalidRequest)
	}
	method := strings.ToUpper(strings.TrimSpace(wire.UI.Method))
	if method != http.MethodPost {
		return BrowserLoginFlow{}, fmt.Errorf("%w: kratos login flow method is unsupported", ErrInvalidRequest)
	}
	action, err := url.Parse(strings.TrimSpace(wire.UI.Action))
	if err != nil || !sameURLOrigin(action, p.publicURL) {
		return BrowserLoginFlow{}, fmt.Errorf("%w: kratos login flow action is invalid", ErrInvalidRequest)
	}

	nodes := make([]LoginFlowNode, 0, len(wire.UI.Nodes))
	hasCredentialInput := false
	hasSubmitInput := false
	for _, node := range wire.UI.Nodes {
		if strings.ToLower(strings.TrimSpace(node.Type)) != "input" {
			continue
		}
		inputType := strings.ToLower(strings.TrimSpace(node.Attributes.Type))
		if !supportedKratosInputType(inputType) {
			continue
		}
		name := strings.TrimSpace(node.Attributes.Name)
		if name == "" {
			continue
		}
		nodes = append(nodes, LoginFlowNode{
			Type:  "input",
			Group: strings.TrimSpace(node.Group),
			Attributes: LoginFlowNodeAttributes{
				Name:         name,
				Type:         inputType,
				Value:        safeLoginFlowValue(node.Attributes.Value),
				Required:     node.Attributes.Required,
				Disabled:     node.Attributes.Disabled,
				AutoComplete: strings.TrimSpace(node.Attributes.AutoComplete),
			},
			Messages: sanitizeLoginFlowMessages(node.Messages),
			Label:    LoginFlowLabel{Text: strings.TrimSpace(node.Meta.Label.Text)},
		})
		if !node.Attributes.Disabled {
			hasCredentialInput = hasCredentialInput || isKratosCredentialInputType(inputType)
			hasSubmitInput = hasSubmitInput || inputType == "submit" || inputType == "button"
		}
	}
	if !hasCredentialInput || !hasSubmitInput {
		return BrowserLoginFlow{}, fmt.Errorf("%w: kratos login flow does not contain a usable credential form", ErrInvalidRequest)
	}
	return BrowserLoginFlow{
		ID:        wire.ID,
		ExpiresAt: strings.TrimSpace(wire.ExpiresAt),
		Action:    action.String(),
		Method:    method,
		Nodes:     nodes,
		Messages:  sanitizeLoginFlowMessages(wire.UI.Messages),
	}, nil
}

func isKratosCredentialInputType(inputType string) bool {
	switch inputType {
	case "text", "email", "password", "number", "tel":
		return true
	default:
		return false
	}
}

func sameURLOrigin(candidate, expected *url.URL) bool {
	return candidate != nil && expected != nil &&
		strings.EqualFold(candidate.Scheme, expected.Scheme) &&
		strings.EqualFold(candidate.Host, expected.Host)
}

func supportedKratosInputType(inputType string) bool {
	switch inputType {
	case "hidden", "text", "email", "password", "number", "tel", "submit", "button":
		return true
	default:
		return false
	}
}

func safeLoginFlowValue(value any) any {
	switch typed := value.(type) {
	case nil, string, bool, float64:
		return typed
	default:
		return nil
	}
}

func sanitizeLoginFlowMessages(messages []kratosLoginFlowMessageWire) []LoginFlowMessage {
	out := make([]LoginFlowMessage, 0, len(messages))
	for _, message := range messages {
		text := strings.TrimSpace(message.Text)
		if text == "" {
			continue
		}
		messageType := strings.ToLower(strings.TrimSpace(message.Type))
		if messageType == "error" {
			text = genericLoginFailureMessage
		}
		out = append(out, LoginFlowMessage{ID: message.ID, Text: text, Type: messageType})
	}
	return out
}

type kratosLoginFlowWire struct {
	ID        string `json:"id"`
	ExpiresAt string `json:"expires_at"`
	UI        struct {
		Action   string                       `json:"action"`
		Method   string                       `json:"method"`
		Nodes    []kratosLoginFlowNodeWire    `json:"nodes"`
		Messages []kratosLoginFlowMessageWire `json:"messages"`
	} `json:"ui"`
}

type kratosLoginFlowNodeWire struct {
	Type       string `json:"type"`
	Group      string `json:"group"`
	Attributes struct {
		Name         string `json:"name"`
		Type         string `json:"type"`
		Value        any    `json:"value"`
		Required     bool   `json:"required"`
		Disabled     bool   `json:"disabled"`
		AutoComplete string `json:"autocomplete"`
	} `json:"attributes"`
	Messages []kratosLoginFlowMessageWire `json:"messages"`
	Meta     struct {
		Label struct {
			Text string `json:"text"`
		} `json:"label"`
	} `json:"meta"`
}

type kratosLoginFlowMessageWire struct {
	ID   int64  `json:"id"`
	Text string `json:"text"`
	Type string `json:"type"`
}

var _ BrowserLoginFlowProvider = (*KratosIdentityProvider)(nil)
