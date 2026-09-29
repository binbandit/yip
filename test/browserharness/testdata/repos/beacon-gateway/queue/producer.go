// Package queue publishes gateway requests for asynchronous processing.
package queue

import "sync"

// Message is one enqueued request.
type Message struct {
	RequestID string
	Path      string
}

// Producer is an in-memory stand-in for the beacon.requests topic.
type Producer struct {
	mu   sync.Mutex
	sent []Message
}

// Publish enqueues a message. Delivery retries happen in the retry worker.
func (p *Producer) Publish(m Message) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.sent = append(p.sent, m)
	return nil
}
