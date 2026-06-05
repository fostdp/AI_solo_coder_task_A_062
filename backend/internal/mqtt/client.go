package mqtt

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"

	"gas-drainage-system/internal/database"

	mqtt "github.com/eclipse/paho.mqtt.golang"
)

type Broadcaster interface {
	Broadcast(msgType string, payload interface{})
}

const (
	ackTimeout   = 5 * time.Second
	maxRetries   = 3
	retryDelay   = 2 * time.Second
	cleanupInterval = 30 * time.Second
)

type pendingCommand struct {
	TargetType string
	TargetID   int
	Command    interface{}
	PublishAt  time.Time
	Retries    int
	AckChan    chan bool
}

type Client struct {
	client   mqtt.Client
	db       *database.DB
	hub      Broadcaster
	pending  map[string]*pendingCommand
	pendingMu sync.RWMutex
}

func NewClient(brokerURL, clientID string) *Client {
	opts := mqtt.NewClientOptions()
	opts.AddBroker(brokerURL)
	opts.SetClientID(clientID)
	opts.SetAutoReconnect(true)
	opts.SetConnectRetry(true)
	opts.SetConnectRetryInterval(5 * time.Second)
	opts.SetKeepAlive(30 * time.Second)
	return &Client{
		client:  mqtt.NewClient(opts),
		pending: make(map[string]*pendingCommand),
	}
}

func (c *Client) SetDB(db *database.DB) {
	c.db = db
}

func (c *Client) SetHub(hub Broadcaster) {
	c.hub = hub
}

func (c *Client) Connect() error {
	token := c.client.Connect()
	token.Wait()
	return token.Error()
}

func commandKey(targetType string, targetID int) string {
	return fmt.Sprintf("%s:%d", targetType, targetID)
}

func (c *Client) PublishCommand(targetType string, targetID int, command interface{}) error {
	topic := fmt.Sprintf("gas/plc/%s/%d/command", targetType, targetID)
	payload, err := json.Marshal(command)
	if err != nil {
		return fmt.Errorf("marshal command: %w", err)
	}
	token := c.client.Publish(topic, 1, false, payload)
	token.Wait()
	return token.Error()
}

func (c *Client) PublishCommandWithAck(ctx context.Context, targetType string, targetID int, command interface{}) error {
	key := commandKey(targetType, targetID)

	ackChan := make(chan bool, 1)
	pc := &pendingCommand{
		TargetType: targetType,
		TargetID:   targetID,
		Command:    command,
		PublishAt:  time.Now(),
		Retries:    0,
		AckChan:    ackChan,
	}

	c.pendingMu.Lock()
	c.pending[key] = pc
	c.pendingMu.Unlock()

	defer func() {
		c.pendingMu.Lock()
		delete(c.pending, key)
		c.pendingMu.Unlock()
	}()

	for attempt := 0; attempt <= maxRetries; attempt++ {
		if attempt > 0 {
			log.Printf("MQTT command retry %d/%d for %s", attempt, maxRetries, key)
			select {
			case <-ctx.Done():
				return fmt.Errorf("command %s cancelled during retry: %w", key, ctx.Err())
			case <-time.After(retryDelay):
			}
		}

		if err := c.PublishCommand(targetType, targetID, command); err != nil {
			log.Printf("MQTT publish error for %s: %v", key, err)
			continue
		}

		c.hub.Broadcast("plc_command_status", map[string]interface{}{
			"targetType": targetType,
			"targetID":   targetID,
			"status":     "sent",
			"attempt":    attempt + 1,
		})

		select {
		case acked := <-ackChan:
			if acked {
				c.hub.Broadcast("plc_command_status", map[string]interface{}{
					"targetType": targetType,
					"targetID":   targetID,
					"status":     "acked",
					"attempt":    attempt + 1,
				})
				return nil
			}
			log.Printf("PLC NACK for %s, retrying", key)
		case <-time.After(ackTimeout):
			log.Printf("MQTT ACK timeout for %s (attempt %d/%d)", key, attempt+1, maxRetries)
			c.hub.Broadcast("plc_command_status", map[string]interface{}{
				"targetType": targetType,
				"targetID":   targetID,
				"status":     "timeout",
				"attempt":    attempt + 1,
			})
		case <-ctx.Done():
			return fmt.Errorf("command %s cancelled: %w", key, ctx.Err())
		}
	}

	return fmt.Errorf("command %s failed after %d retries", key, maxRetries)
}

func (c *Client) SubscribeFeedback() error {
	token := c.client.Subscribe("gas/plc/+/+/feedback", 1, func(_ mqtt.Client, msg mqtt.Message) {
		parts := strings.Split(msg.Topic(), "/")
		if len(parts) < 5 {
			log.Printf("invalid feedback topic: %s", msg.Topic())
			return
		}
		targetType := parts[2]
		targetIDStr := parts[3]

		var feedback struct {
			Success      bool    `json:"success"`
			ActualValue  float64 `json:"actual_value"`
			CommandType  string  `json:"command_type"`
			CommandValue float64 `json:"command_value"`
		}
		if err := json.Unmarshal(msg.Payload(), &feedback); err != nil {
			log.Printf("parse feedback error: %v", err)
			return
		}

		var targetID int
		fmt.Sscanf(targetIDStr, "%d", &targetID)
		key := commandKey(targetType, targetID)

		c.pendingMu.RLock()
		pc, exists := c.pending[key]
		c.pendingMu.RUnlock()

		if exists {
			select {
			case pc.AckChan <- feedback.Success:
			default:
			}
		}

		status := "acked"
		if !feedback.Success {
			status = "failed"
			log.Printf("PLC command failed for %s", key)
		}

		c.hub.Broadcast("plc_feedback", map[string]interface{}{
			"targetType":   targetType,
			"targetID":     targetIDStr,
			"success":      feedback.Success,
			"actualValue":  feedback.ActualValue,
			"commandType":  feedback.CommandType,
			"commandValue": feedback.CommandValue,
			"status":       status,
		})
	})
	token.Wait()
	return token.Error()
}

func (c *Client) cleanupPending() {
	c.pendingMu.Lock()
	defer c.pendingMu.Unlock()
	now := time.Now()
	for key, pc := range c.pending {
		if now.Sub(pc.PublishAt) > ackTimeout*time.Duration(maxRetries+1)+retryDelay*time.Duration(maxRetries) {
			delete(c.pending, key)
			log.Printf("cleaned up stale pending command: %s", key)
		}
	}
}

func (c *Client) Start() {
	if err := c.Connect(); err != nil {
		log.Printf("MQTT connect error: %v", err)
		return
	}
	if err := c.SubscribeFeedback(); err != nil {
		log.Printf("MQTT subscribe error: %v", err)
	}
	go func() {
		ticker := time.NewTicker(cleanupInterval)
		defer ticker.Stop()
		for range ticker.C {
			c.cleanupPending()
		}
	}()
	log.Println("MQTT client started with ACK tracking")
}
