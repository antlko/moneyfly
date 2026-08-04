package api

import "sync"

// syncEvent tells a device that something changed. It carries no payload beyond
// "pull from here" — the change log is the only transport for data.
type syncEvent struct {
	Seq      int64  `json:"seq"`
	DeviceID string `json:"deviceId"`
}

// broker fans sync notifications out to a user's connected devices.
//
// Subscriptions are per user, never global: a notification must not so much as
// reveal that another account exists.
type broker struct {
	mu   sync.Mutex
	subs map[string]map[chan syncEvent]struct{}
}

func newBroker() *broker {
	return &broker{subs: make(map[string]map[chan syncEvent]struct{})}
}

// subscribe returns a channel of events for a user and a function to release it.
func (b *broker) subscribe(userID string) (<-chan syncEvent, func()) {
	// Buffered by one: an event only means "there is something new", so a
	// subscriber that is mid-pull already has a pending wake-up and needs no
	// second one.
	ch := make(chan syncEvent, 1)

	b.mu.Lock()
	if b.subs[userID] == nil {
		b.subs[userID] = make(map[chan syncEvent]struct{})
	}
	b.subs[userID][ch] = struct{}{}
	b.mu.Unlock()

	return ch, func() {
		b.mu.Lock()
		defer b.mu.Unlock()
		if set, ok := b.subs[userID]; ok {
			delete(set, ch)
			if len(set) == 0 {
				delete(b.subs, userID)
			}
		}
		close(ch)
	}
}

// publish notifies a user's other devices.
//
// Sends are non-blocking and dropped when a subscriber's buffer is full. That is
// safe precisely because the event carries no data: a dropped notification means
// the device pulls one moment later, on the next event or its poll fallback. A
// blocking send here would let one stalled reader hold up a write.
func (b *broker) publish(userID string, ev syncEvent) {
	b.mu.Lock()
	defer b.mu.Unlock()

	for ch := range b.subs[userID] {
		select {
		case ch <- ev:
		default:
		}
	}
}

// subscriberCount reports how many streams a user has open (used by tests).
func (b *broker) subscriberCount(userID string) int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return len(b.subs[userID])
}
