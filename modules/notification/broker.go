package notification

import (
	"sync"
	"uuid"
)

// eventBuffer is how many events one subscriber holds before the broker
// stops trying. A subscriber that falls behind has the table behind it —
// the stream is a convenience, the inbox is the record — so a dropped
// event costs a client-side catch-up through the list, not a message.
const eventBuffer = 32

// Broker is the live tail the watch procedure reads: the in-process
// registry of open streams, keyed by the account each stream watches for.
// It is deliberately not durable — the durable record is the table, the
// broker is only what saves a reconnecting client the wait until its next
// poll — and it is deliberately not a message broker: there is one
// publisher (the service, after a create commits) and one event shape.
type Broker struct {
	mu   sync.Mutex
	subs map[uuid.UUID]map[chan Notification]struct{}
}

// NewBroker builds an empty broker.
func NewBroker() *Broker {
	return &Broker{subs: map[uuid.UUID]map[chan Notification]struct{}{}}
}

// Subscribe registers one stream and answers the channel it reads with the
// function that closes it. The cancel is idempotent and safe to defer.
func (b *Broker) Subscribe(userID uuid.UUID) (<-chan Notification, func()) {
	ch := make(chan Notification, eventBuffer)

	b.mu.Lock()
	if b.subs[userID] == nil {
		b.subs[userID] = map[chan Notification]struct{}{}
	}
	b.subs[userID][ch] = struct{}{}
	b.mu.Unlock()

	cancel := func() {
		b.mu.Lock()
		defer b.mu.Unlock()
		if subs, ok := b.subs[userID]; ok {
			delete(subs, ch)
			if len(subs) == 0 {
				delete(b.subs, userID)
			}
		}
		close(ch)
	}
	return ch, cancel
}

// PublishAll hands one notification to every open stream — the global
// audience, which names nobody and therefore excludes nobody.
func (b *Broker) PublishAll(n Notification) {
	b.mu.Lock()
	defer b.mu.Unlock()
	for _, subs := range b.subs {
		for ch := range subs {
			send(ch, n)
		}
	}
}

// PublishTo hands one notification to the streams of the accounts the
// audience names.
func (b *Broker) PublishTo(userIDs []uuid.UUID, n Notification) {
	b.mu.Lock()
	defer b.mu.Unlock()
	for _, id := range userIDs {
		for ch := range b.subs[id] {
			send(ch, n)
		}
	}
}

// send delivers without blocking. A subscriber whose buffer is full has
// missed the event on purpose: the durable record is the table, and a
// blocked publisher would hold every other stream hostage to the slowest
// reader.
func send(ch chan Notification, n Notification) {
	select {
	case ch <- n:
	default:
	}
}
