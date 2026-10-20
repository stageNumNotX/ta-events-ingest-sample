// Package pubsub publishes canonical events to Google Pub/Sub, or to process
// logs when running locally without cloud credentials.
package pubsub

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	gpubsub "cloud.google.com/go/pubsub"

	"ta-events-ingest/internal/ingest"
)

const (
	maxInflightPublishes = 64
	publishAckTimeout    = 10 * time.Second
)

var errPublishBacklog = errors.New("publish backlog full")

// TopicPublisher publishes canonical events on a Pub/Sub topic.
// One client and one topic per process; client-side batching is used.
type TopicPublisher struct {
	client   *gpubsub.Client
	topic    *gpubsub.Topic
	onResult func(err error)
	sem      chan struct{}
	wg       sync.WaitGroup
}

// New creates a publisher for project/topic. onResult (optional) is called
// with each delivery result for metrics/logging.
func New(ctx context.Context, projectID, topicID string, onResult func(err error)) (*TopicPublisher, error) {
	client, err := gpubsub.NewClient(ctx, projectID)
	if err != nil {
		return nil, fmt.Errorf("create pubsub client: %w", err)
	}
	topic := client.Topic(topicID)
	topic.PublishSettings.NumGoroutines = 8
	topic.PublishSettings.CountThreshold = 1
	topic.PublishSettings.Timeout = publishAckTimeout
	topic.PublishSettings.FlowControlSettings = gpubsub.FlowControlSettings{
		MaxOutstandingMessages: maxInflightPublishes,
		MaxOutstandingBytes:    4 << 20,
		LimitExceededBehavior:  gpubsub.FlowControlBlock,
	}
	return &TopicPublisher{
		client:   client,
		topic:    topic,
		onResult: onResult,
		sem:      make(chan struct{}, maxInflightPublishes),
	}, nil
}

// Publish does not block the request path: the client buffers the message and
// ACK wait runs in a bounded pool with a timeout (not tied to the HTTP request
// that already returned 202).
func (p *TopicPublisher) Publish(ev ingest.CanonicalEvent) error {
	data, err := json.Marshal(ev)
	if err != nil {
		return fmt.Errorf("marshal canonical event: %w", err)
	}

	res := p.topic.Publish(context.Background(), &gpubsub.Message{
		Data: data,
		Attributes: map[string]string{
			"event_name": ev.EventName,
		},
	})

	p.wg.Add(1)
	select {
	case p.sem <- struct{}{}:
	default:
		p.wg.Done()
		if p.onResult != nil {
			p.onResult(errPublishBacklog)
		}
		return nil
	}

	go func() {
		defer p.wg.Done()
		defer func() { <-p.sem }()
		ctx, cancel := context.WithTimeout(context.Background(), publishAckTimeout)
		defer cancel()
		_, err := res.Get(ctx)
		if p.onResult != nil {
			p.onResult(err)
		}
	}()

	return nil
}

// Stop flushes buffered messages, waits for in-flight ACK goroutines, and
// closes the client.
func (p *TopicPublisher) Stop() {
	p.topic.Stop()
	p.wg.Wait()
	_ = p.client.Close()
}
