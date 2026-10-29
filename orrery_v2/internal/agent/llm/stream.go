package llm

import (
	"io"
	"sync"
)

type streamItem struct {
	message *Message
	err     error
}

type messagePipe struct {
	items      chan streamItem
	readerDone chan struct{}
	writerDone chan struct{}
	readerOnce sync.Once
	writerOnce sync.Once
	sendMu     sync.Mutex
}

// MessageStream has one receiver. Close can be called concurrently with Recv
// and interrupts a blocked producer without invoking provider callbacks.
type MessageStream struct {
	pipe   *messagePipe
	array  []*Message
	next   int
	mu     sync.Mutex
	closed bool
}

type MessageSender struct{ pipe *messagePipe }

func NewMessageStream(messages []*Message) *MessageStream {
	return &MessageStream{array: append([]*Message(nil), messages...)}
}

func MessagePipe(capacity int) (*MessageStream, *MessageSender) {
	if capacity < 0 {
		capacity = 0
	}
	p := &messagePipe{items: make(chan streamItem, capacity), readerDone: make(chan struct{}), writerDone: make(chan struct{})}
	return &MessageStream{pipe: p}, &MessageSender{pipe: p}
}

func (stream *MessageStream) Recv() (*Message, error) {
	if stream == nil {
		return nil, io.EOF
	}
	if stream.pipe == nil {
		stream.mu.Lock()
		defer stream.mu.Unlock()
		if stream.closed || stream.next >= len(stream.array) {
			return nil, io.EOF
		}
		message := stream.array[stream.next]
		stream.next++
		return message, nil
	}
	p := stream.pipe
	select {
	case <-p.readerDone:
		return nil, io.EOF
	default:
	}
	select {
	case <-p.readerDone:
		return nil, io.EOF
	case item, ok := <-p.items:
		if !ok {
			return nil, io.EOF
		}
		return item.message, item.err
	}
}

func (stream *MessageStream) Close() {
	if stream == nil {
		return
	}
	if stream.pipe != nil {
		stream.pipe.readerOnce.Do(func() { close(stream.pipe.readerDone) })
		return
	}
	stream.mu.Lock()
	stream.closed = true
	stream.mu.Unlock()
}

// Send reports whether the reader or writer has already closed the stream.
func (writer *MessageSender) Send(message *Message, err error) bool {
	if writer == nil || writer.pipe == nil {
		return true
	}
	p := writer.pipe
	p.sendMu.Lock()
	defer p.sendMu.Unlock()
	select {
	case <-p.readerDone:
		return true
	case <-p.writerDone:
		return true
	default:
	}
	select {
	case <-p.readerDone:
		return true
	case <-p.writerDone:
		return true
	case p.items <- streamItem{message: message, err: err}:
		return false
	}
}

func (writer *MessageSender) Close() {
	if writer == nil || writer.pipe == nil {
		return
	}
	p := writer.pipe
	p.writerOnce.Do(func() {
		close(p.writerDone)
		p.sendMu.Lock()
		close(p.items)
		p.sendMu.Unlock()
	})
}
