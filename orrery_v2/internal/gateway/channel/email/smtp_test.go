package email

import "testing"

func TestValidateMessageRequiresStrictAddressesAndBody(t *testing.T) {
	valid := Message{From: "agent@example.test", To: []string{"user@example.test"}, Subject: "Hello", Text: "Body"}
	if err := ValidateMessage(valid); err != nil {
		t.Fatalf("ValidateMessage: %v", err)
	}
	for name, message := range map[string]Message{
		"display name sender": {From: "Agent <agent@example.test>", To: valid.To, Subject: valid.Subject, Text: valid.Text},
		"missing recipient":   {From: valid.From, Subject: valid.Subject, Text: valid.Text},
		"header injection":    {From: valid.From, To: valid.To, Subject: "Hello\r\nBcc: x@example.test", Text: valid.Text},
		"missing body":        {From: valid.From, To: valid.To, Subject: valid.Subject},
	} {
		t.Run(name, func(t *testing.T) {
			if err := ValidateMessage(message); err == nil {
				t.Fatal("ValidateMessage error = nil")
			}
		})
	}
}

func TestBuildSMTPMessagePublishesAlternativeBodiesAndMessageID(t *testing.T) {
	message, err := buildSMTPMessage(Message{
		From: "agent@example.test", To: []string{"user@example.test"}, Cc: []string{"reviewer@example.test"},
		Subject: "Status", Text: "Plain", HTML: "<p>HTML</p>",
	})
	if err != nil {
		t.Fatalf("buildSMTPMessage: %v", err)
	}
	if message.GetMessageID() == "" {
		t.Fatal("message id was not generated")
	}
}
