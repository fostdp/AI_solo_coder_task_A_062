package mqtt

import (
	"encoding/json"
	"fmt"
	"log"
	"strings"
	"time"

	"gas-drainage-system/internal/database"

	mqtt "github.com/eclipse/paho.mqtt.golang"
)

type Broadcaster interface {
	Broadcast(msgType string, payload interface{})
}

type Client struct {
	client mqtt.Client
	db     *database.DB
	hub    Broadcaster
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
		client: mqtt.NewClient(opts),
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

		if !feedback.Success {
			log.Printf("PLC command failed for %s/%s", targetType, targetIDStr)
			return
		}

		c.hub.Broadcast("plc_feedback", map[string]interface{}{
			"targetType":   targetType,
			"targetID":     targetIDStr,
			"success":      feedback.Success,
			"actualValue":  feedback.ActualValue,
			"commandType":  feedback.CommandType,
			"commandValue": feedback.CommandValue,
		})
	})
	token.Wait()
	return token.Error()
}

func (c *Client) Start() {
	if err := c.Connect(); err != nil {
		log.Printf("MQTT connect error: %v", err)
		return
	}
	if err := c.SubscribeFeedback(); err != nil {
		log.Printf("MQTT subscribe error: %v", err)
	}
	log.Println("MQTT client started")
}
