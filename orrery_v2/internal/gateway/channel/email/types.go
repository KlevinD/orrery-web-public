package email

type InboundEmail struct {
	MessageID string
	ThreadID  string
	From      string
	Subject   string
	Body      string
}

type OutboundEmail struct {
	To        string
	InReplyTo string
	Subject   string
	Body      string
}
