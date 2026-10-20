package runtimepub

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"time"

	"cloud.google.com/go/pubsub"

	"ta-events-ingest/internal/ingest"
)

type Topic struct {
	client *pubsub.Client
	topic  *pubsub.Topic
}

func NewTopic(ctx context.Context, projectID, topicID string) (*Topic, error) {
	if projectID == "" || topicID == "" {
		return nil, fmt.Errorf("project and topic are required")
	}
	client, err := pubsub.NewClient(ctx, projectID)
	if err != nil {
		return nil, fmt.Errorf("pubsub client: %w", err)
	}
	topic := client.Topic(topicID)
	// Bounded RPC workers, separate from the in-process queue workers.
	// CountThreshold=1 so a 3-event click is not waiting for a 20-message batch.
	topic.PublishSettings.NumGoroutines = 8
	topic.PublishSettings.CountThreshold = 1
	topic.PublishSettings.ByteThreshold = 1 << 20
	topic.PublishSettings.DelayThreshold = 10 * time.Millisecond
	topic.PublishSettings.Timeout = publishTimeout
	topic.PublishSettings.FlowControlSettings = pubsub.FlowControlSettings{
		MaxOutstandingMessages: 64,
		MaxOutstandingBytes:    4 << 20,
		LimitExceededBehavior:  pubsub.FlowControlBlock,
	}
	return &Topic{client: client, topic: topic}, nil
}

func marshalCanonical(ev ingest.CanonicalEvent) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(ev); err != nil {
		return nil, err
	}
	return bytes.TrimSpace(buf.Bytes()), nil
}

func eventAttributes(ev ingest.CanonicalEvent) map[string]string {
	out := map[string]string{"event_name": ev.EventName}
	if ev.ClickID != "" {
		out["click_id"] = ev.ClickID
	}
	if ev.ScenarioID != "" {
		out["scenario_id"] = ev.ScenarioID
	}
	if ev.SmartlinkVariantID != "" {
		out["smartlink_variant_id"] = ev.SmartlinkVariantID
	}
	return out
}

func (t *Topic) Publish(ctx context.Context, ev ingest.CanonicalEvent) error {
	data, err := marshalCanonical(ev)
	if err != nil {
		return fmt.Errorf("marshal event: %w", err)
	}
	// Flow-control acquire must not share the Get timeout: a cancelled Publish
	// ctx can stall the empty ordering key. Wait for ACK on ctx only.
	// Attributes carry ids only — never redirect_to / URL / secrets.
	res := t.topic.Publish(context.Background(), &pubsub.Message{
		Data:       data,
		Attributes: eventAttributes(ev),
	})
	_, err = res.Get(ctx)
	return err
}

func (t *Topic) Stop() {
	if t == nil {
		return
	}
	if t.topic != nil {
		t.topic.Stop()
	}
	if t.client != nil {
		_ = t.client.Close()
	}
}
