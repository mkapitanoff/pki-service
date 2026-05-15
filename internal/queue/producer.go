package queue

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"

	amqp "github.com/rabbitmq/amqp091-go"
)

// EventProducer publishes domain events.
type EventProducer interface {
	Publish(ctx context.Context, event string, payload any) error
}

// RabbitMQProducer publishes events to a RabbitMQ topic exchange.
type RabbitMQProducer struct {
	conn     *amqp.Connection
	ch       *amqp.Channel
	exchange string
	mu       sync.Mutex
}

// NewRabbitMQProducer connects to RabbitMQ and declares a durable topic exchange.
func NewRabbitMQProducer(url, exchange string) (*RabbitMQProducer, error) {
	conn, err := amqp.Dial(url)
	if err != nil {
		return nil, fmt.Errorf("rabbitmq producer: dial: %w", err)
	}
	ch, err := conn.Channel()
	if err != nil {
		conn.Close()
		return nil, fmt.Errorf("rabbitmq producer: channel: %w", err)
	}
	if err := ch.ExchangeDeclare(
		exchange,
		"topic",
		true,  // durable
		false, // auto-delete
		false, // internal
		false, // no-wait
		nil,
	); err != nil {
		ch.Close()
		conn.Close()
		return nil, fmt.Errorf("rabbitmq producer: declare exchange %q: %w", exchange, err)
	}
	return &RabbitMQProducer{conn: conn, ch: ch, exchange: exchange}, nil
}

// Publish marshals payload to JSON and publishes to the exchange with routingKey = event.
func (p *RabbitMQProducer) Publish(ctx context.Context, event string, payload any) error {
	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("rabbitmq producer: marshal: %w", err)
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.ch.PublishWithContext(ctx,
		p.exchange,
		event, // routing key
		false, // mandatory
		false, // immediate
		amqp.Publishing{
			ContentType:  "application/json",
			DeliveryMode: amqp.Persistent,
			Body:         body,
		},
	)
}

// Close releases channel and connection.
func (p *RabbitMQProducer) Close() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if err := p.ch.Close(); err != nil {
		return err
	}
	return p.conn.Close()
}

// MockProducer records published events for use in tests.
type MockProducer struct {
	mu     sync.Mutex
	Events []PublishedEvent
}

// PublishedEvent is a single recorded event from MockProducer.
type PublishedEvent struct {
	Event   string
	Payload any
}

func (m *MockProducer) Publish(_ context.Context, event string, payload any) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.Events = append(m.Events, PublishedEvent{Event: event, Payload: payload})
	return nil
}
