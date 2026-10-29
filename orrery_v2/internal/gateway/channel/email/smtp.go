package email

import (
	"context"
	"fmt"
	"net/mail"
	"strings"

	gomail "github.com/wneessen/go-mail"
)

const (
	TLSModeStartTLS = "starttls"
	TLSModeImplicit = "implicit"
)

type Message struct {
	From    string
	To      []string
	Cc      []string
	Subject string
	Text    string
	HTML    string
}

type Receipt struct {
	MessageID string
}

type Sender interface {
	Send(context.Context, Options, Message) (Receipt, error)
}

type SMTPSender struct{}

func (SMTPSender) Send(ctx context.Context, options Options, message Message) (Receipt, error) {
	if err := ValidateSMTPOptions(options); err != nil {
		return Receipt{}, err
	}
	mailMessage, err := buildSMTPMessage(message)
	if err != nil {
		return Receipt{}, err
	}
	clientOptions := []gomail.Option{
		gomail.WithPort(options.SMTPPort),
		gomail.WithUsername(strings.TrimSpace(options.Username)),
		gomail.WithPassword(options.Password),
	}
	switch strings.ToLower(strings.TrimSpace(options.TLSMode)) {
	case TLSModeImplicit:
		clientOptions = append(clientOptions, gomail.WithSSL())
	case TLSModeStartTLS, "":
		clientOptions = append(clientOptions, gomail.WithTLSPolicy(gomail.TLSMandatory))
	default:
		return Receipt{}, fmt.Errorf("unsupported SMTP TLS mode %q", options.TLSMode)
	}
	client, err := gomail.NewClient(strings.TrimSpace(options.SMTPHost), clientOptions...)
	if err != nil {
		return Receipt{}, fmt.Errorf("configure SMTP client: %w", err)
	}
	if err := client.DialAndSendWithContext(ctx, mailMessage); err != nil {
		return Receipt{}, fmt.Errorf("send SMTP message: %w", err)
	}
	return Receipt{MessageID: strings.Trim(mailMessage.GetMessageID(), "<>")}, nil
}

func ValidateSMTPOptions(options Options) error {
	if strings.TrimSpace(options.SMTPHost) == "" {
		return fmt.Errorf("SMTP host is required")
	}
	if options.SMTPPort < 1 || options.SMTPPort > 65535 {
		return fmt.Errorf("SMTP port must be between 1 and 65535")
	}
	if strings.TrimSpace(options.Username) == "" {
		return fmt.Errorf("SMTP username is required")
	}
	if options.Password == "" {
		return fmt.Errorf("SMTP password is required")
	}
	return nil
}

func ValidateMessage(message Message) error {
	_, err := buildSMTPMessage(message)
	return err
}

func buildSMTPMessage(message Message) (*gomail.Msg, error) {
	from, err := mailboxAddress(message.From)
	if err != nil {
		return nil, fmt.Errorf("invalid sender address: %w", err)
	}
	to, err := mailboxAddresses(message.To)
	if err != nil {
		return nil, fmt.Errorf("invalid to address: %w", err)
	}
	if len(to) == 0 {
		return nil, fmt.Errorf("at least one to address is required")
	}
	cc, err := mailboxAddresses(message.Cc)
	if err != nil {
		return nil, fmt.Errorf("invalid cc address: %w", err)
	}
	if strings.ContainsAny(message.Subject, "\r\n") {
		return nil, fmt.Errorf("subject must not contain line breaks")
	}
	if message.Text == "" && message.HTML == "" {
		return nil, fmt.Errorf("text or html body is required")
	}

	result := gomail.NewMsg()
	if err := result.From(from); err != nil {
		return nil, fmt.Errorf("set sender: %w", err)
	}
	if err := result.To(to...); err != nil {
		return nil, fmt.Errorf("set recipients: %w", err)
	}
	if len(cc) > 0 {
		if err := result.Cc(cc...); err != nil {
			return nil, fmt.Errorf("set cc recipients: %w", err)
		}
	}
	result.Subject(message.Subject)
	result.SetMessageID()
	if message.Text != "" {
		result.SetBodyString(gomail.TypeTextPlain, message.Text)
		if message.HTML != "" {
			result.AddAlternativeString(gomail.TypeTextHTML, message.HTML)
		}
	} else {
		result.SetBodyString(gomail.TypeTextHTML, message.HTML)
	}
	return result, nil
}

func mailboxAddresses(values []string) ([]string, error) {
	result := make([]string, 0, len(values))
	for _, value := range values {
		address, err := mailboxAddress(value)
		if err != nil {
			return nil, err
		}
		result = append(result, address)
	}
	return result, nil
}

func mailboxAddress(value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", fmt.Errorf("mailbox address is required")
	}
	parsed, err := mail.ParseAddress(value)
	if err != nil || parsed.Name != "" || !strings.EqualFold(parsed.Address, value) {
		return "", fmt.Errorf("%q is not a mailbox address", value)
	}
	return parsed.Address, nil
}
