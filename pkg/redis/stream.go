package redis

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"time"

	"github.com/redis/go-redis/v9"
)

const (
	CommandTTL = 5 * time.Minute
)

func (c *Client) PublishEBPFCommand(ctx context.Context, targetNode, commandType string, payload *EBPFCommandPayload) error {
	payloadData, err := json.Marshal(payload)
	if err != nil {
		return err
	}

	command := &ControlCommand{
		CommandID:   fmt.Sprintf("%s-%d", commandType, time.Now().UnixNano()),
		CommandType: commandType,
		TargetNode:  targetNode,
		Payload:     string(payloadData),
		Timestamp:   time.Now(),
		TTL:         CommandTTL,
	}

	return c.PublishCommand(ctx, command)
}

func (c *Client) PublishCommand(ctx context.Context, command *ControlCommand) error {
	values := map[string]interface{}{
		"commandId":   command.CommandID,
		"commandType": command.CommandType,
		"payload":     command.Payload,
		"timestamp":   command.Timestamp.UnixNano(),
		"ttl":         command.TTL.Milliseconds(),
	}

	streamKey := fmt.Sprintf("control:node:%s", command.TargetNode)

	_, err := c.client.XAdd(ctx, &redis.XAddArgs{
		Stream: streamKey,
		Values: values,
	}).Result()

	return err
}

type ControlBusConsumer struct {
	client        *redis.Client
	consumerID    string
	streamKey     string
	consumerGroup string
	targetNode    string
	handler       CommandHandler
	ctx           context.Context
	cancel        context.CancelFunc
}

type CommandHandler func(ctx context.Context, command *ControlCommand) error

func NewControlBusConsumer(client *redis.Client, consumerID, targetNode string, handler CommandHandler) *ControlBusConsumer {
	ctx, cancel := context.WithCancel(context.Background())
	streamKey := fmt.Sprintf("control:node:%s", targetNode)
	return &ControlBusConsumer{
		client:        client,
		consumerID:    consumerID,
		streamKey:     streamKey,
		consumerGroup: fmt.Sprintf("agent-%s", targetNode),
		targetNode:    targetNode,
		handler:       handler,
		ctx:           ctx,
		cancel:        cancel,
	}
}

func (c *ControlBusConsumer) Start() error {
	if err := c.createConsumerGroup(); err != nil {
		return err
	}

	go c.consumeLoop()
	return nil
}

func (c *ControlBusConsumer) Stop() {
	c.cancel()
}

func (c *ControlBusConsumer) createConsumerGroup() error {
	err := c.client.XGroupCreateMkStream(c.ctx, c.streamKey, c.consumerGroup, "0").Err()
	if err != nil {
		if err.Error() == "BUSYGROUP Consumer Group name already exists" {
			return nil
		}
		return err
	}
	return nil
}

func (c *ControlBusConsumer) consumeLoop() {
	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-c.ctx.Done():
			return
		case <-ticker.C:
			c.consumeMessages()
		}
	}
}

func (c *ControlBusConsumer) consumeMessages() {
	streams, err := c.client.XReadGroup(c.ctx, &redis.XReadGroupArgs{
		Group:    c.consumerGroup,
		Consumer: c.consumerID,
		Streams:  []string{c.streamKey, ">"},
		Count:    10,
		Block:    0,
	}).Result()

	if err != nil {
		return
	}

	if len(streams) == 0 {
		return
	}

	var messageIDs []string
	for _, stream := range streams {
		for _, message := range stream.Messages {
			if c.processMessage(message) {
				messageIDs = append(messageIDs, message.ID)
			}
		}
	}

	if len(messageIDs) > 0 {
		c.ackMessages(messageIDs)
	}
}

func (c *ControlBusConsumer) processMessage(message redis.XMessage) bool {
	timestampStr, _ := message.Values["timestamp"].(string)
	timestamp, _ := strconv.ParseInt(timestampStr, 10, 64)

	command := &ControlCommand{
		CommandID:   message.ID,
		CommandType: message.Values["commandType"].(string),
		Timestamp:   time.Unix(0, timestamp),
	}

	if payload, ok := message.Values["payload"].(string); ok {
		command.Payload = payload
	}

	if c.handler != nil {
		if err := c.handler(c.ctx, command); err != nil {
			return false
		}
	}

	return true
}

func (c *ControlBusConsumer) ackMessages(messageIDs []string) {
	if len(messageIDs) > 0 {
		c.client.XAck(c.ctx, c.streamKey, c.consumerGroup, messageIDs...)
	}
}
