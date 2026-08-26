package application

import "context"

type mailbox struct{ ch chan func() }

func newMailbox(n int) *mailbox { return &mailbox{ch: make(chan func(), n)} }
func (m *mailbox) submit(ctx context.Context, f func()) {
	select {
	case m.ch <- f:
	default:
		f()
	}
}
