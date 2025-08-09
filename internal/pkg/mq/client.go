package mq

import (
	"log"

	"github.com/streadway/amqp"
)

type RabbitMQClient struct {
	conn    *amqp.Connection
	channel *amqp.Channel
	queue   amqp.Queue
}

func NewRabbitMQClient(amqpURL, queueName string) (*RabbitMQClient, error) {
	conn, err := amqp.Dial(amqpURL)
	if err != nil {
		return nil, err
	}

	ch, err := conn.Channel()
	if err != nil {
		conn.Close()
		return nil, err
	}

	queue, err := ch.QueueDeclare(
		queueName, // 队列名称
		true,      // 持久化
		false,     // 非独占
		false,     // 非自动删除
		false,     // 不等待
		nil,       // 参数
	)
	if err != nil {
		ch.Close()
		conn.Close()
		return nil, err
	}

	return &RabbitMQClient{
		conn:    conn,
		channel: ch,
		queue:   queue,
	}, nil
}

func (c *RabbitMQClient) Publish(body []byte) error {
	return c.channel.Publish(
		"",           // exchange
		c.queue.Name, // routing key
		false,        // mandatory
		false,        // immediate
		amqp.Publishing{
			ContentType: "application/json",
			Body:        body,
			// Make message persistent
			DeliveryMode: amqp.Persistent,
		},
	)
}

func (c *RabbitMQClient) Consume(handler func(msg amqp.Delivery)) error {
	msgs, err := c.channel.Consume(
		c.queue.Name,
		"",    // consumer
		false, // auto-ack
		false, // exclusive
		false, // no-local
		false, // no-wait
		nil,   // args
	)
	if err != nil {
		return err
	}

	forever := make(chan bool)
	go func() {
		for msg := range msgs {
			handler(msg)
		}
	}()

	log.Printf(" [*] Waiting for messages. To exit press CTRL+C")
	<-forever
	return nil
}

func (c *RabbitMQClient) Close() {
	c.channel.Close()
	c.conn.Close()
}
