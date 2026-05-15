package queue

import (
	"context"
	"fmt"

	amqp "github.com/rabbitmq/amqp091-go"
)

// RabbitMQConsumer consumes messages from a single RabbitMQ queue.
type RabbitMQConsumer struct {
	conn     *amqp.Connection
	ch       *amqp.Channel
	queue    string
}

// NewRabbitMQConsumer connects to RabbitMQ, declares a durable queue, and
// optionally binds it to a topic exchange (binding is skipped when exchange is "").
func NewRabbitMQConsumer(url, queue, exchange string) (*RabbitMQConsumer, error) {
	conn, err := amqp.Dial(url)
	if err != nil {
		return nil, fmt.Errorf("rabbitmq consumer: dial: %w", err)
	}
	ch, err := conn.Channel()
	if err != nil {
		conn.Close()
		return nil, fmt.Errorf("rabbitmq consumer: channel: %w", err)
	}
	if _, err := ch.QueueDeclare(
		queue,
		true,  // durable
		false, // auto-delete
		false, // exclusive
		false, // no-wait
		nil,
	); err != nil {
		ch.Close()
		conn.Close()
		return nil, fmt.Errorf("rabbitmq consumer: declare queue %q: %w", queue, err)
	}
	if exchange != "" {
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
			return nil, fmt.Errorf("rabbitmq consumer: declare exchange %q: %w", exchange, err)
		}
		if err := ch.QueueBind(queue, "#", exchange, false, nil); err != nil {
			ch.Close()
			conn.Close()
			return nil, fmt.Errorf("rabbitmq consumer: bind queue %q to %q: %w", queue, exchange, err)
		}
	}
	return &RabbitMQConsumer{conn: conn, ch: ch, queue: queue}, nil
}

// Consume blocks and calls handler for each message. It acks on success and
// nacks (with requeue) on error. Returns when ctx is cancelled or the channel
// closes.
func (c *RabbitMQConsumer) Consume(ctx context.Context, handler func(event string, body []byte) error) error {
	msgs, err := c.ch.Consume(
		c.queue,
		"",    // consumer tag
		false, // auto-ack
		false, // exclusive
		false, // no-local
		false, // no-wait
		nil,
	)
	if err != nil {
		return fmt.Errorf("rabbitmq consumer: consume: %w", err)
	}
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case msg, ok := <-msgs:
			if !ok {
				return fmt.Errorf("rabbitmq consumer: delivery channel closed")
			}
			if err := handler(msg.RoutingKey, msg.Body); err != nil {
				_ = msg.Nack(false, true) // requeue on error
			} else {
				_ = msg.Ack(false)
			}
		}
	}
}

// Close releases the channel and connection.
func (c *RabbitMQConsumer) Close() error {
	if err := c.ch.Close(); err != nil {
		return err
	}
	return c.conn.Close()
}
