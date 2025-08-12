package mq

import (
	"fmt"
	"log"

	"github.com/streadway/amqp"
)

// RabbitMQClient 封装了 RabbitMQ 的连接和通道
type RabbitMQClient struct {
	conn    *amqp.Connection
	channel *amqp.Channel
}

// NewRabbitMQClient 创建一个新的 RabbitMQ 客户端实例
func NewRabbitMQClient(amqpURL string) (*RabbitMQClient, error) {
	conn, err := amqp.Dial(amqpURL)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to RabbitMQ: %w", err)
	}

	ch, err := conn.Channel()
	if err != nil {
		conn.Close()
		return nil, fmt.Errorf("failed to open a channel: %w", err)
	}

	return &RabbitMQClient{
		conn:    conn,
		channel: ch,
	}, nil
}

// DeclareQueue 声明一个队列
func (c *RabbitMQClient) DeclareQueue(queueName string) (amqp.Queue, error) {
	return c.channel.QueueDeclare(
		queueName, // name
		true,      // durable
		false,     // delete when unused
		false,     // exclusive
		false,     // no-wait
		nil,       // arguments
	)
}

// Publish a message to a specific queue
func (c *RabbitMQClient) Publish(queueName string, body []byte) error {
	return c.channel.Publish(
		"",        // exchange (default)
		queueName, // routing key (queue name)
		false,     // mandatory
		false,     // immediate
		amqp.Publishing{
			ContentType:  "application/json",
			Body:         body,
			DeliveryMode: amqp.Persistent, // make message persistent
		},
	)
}

// Consume messages from a specific queue
func (c *RabbitMQClient) Consume(queueName string, handler func(msg amqp.Delivery)) error {
	msgs, err := c.channel.Consume(
		queueName,
		"",    // consumer
		false, // auto-ack (we will manually ack)
		false, // exclusive
		false, // no-local
		false, // no-wait
		nil,   // args
	)
	if err != nil {
		return fmt.Errorf("failed to register a consumer: %w", err)
	}

	go func() {
		for msg := range msgs {
			handler(msg)
		}
	}()

	log.Printf(" [*] Waiting for messages on queue '%s'. To exit press CTRL+C", queueName)
	return nil
}

// Close the channel and connection
func (c *RabbitMQClient) Close() {
	if c.channel != nil {
		c.channel.Close()
	}
	if c.conn != nil {
		c.conn.Close()
	}
}
