package command_dispatcher

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

type CommandEvent struct {
	TargetType string
	TargetID   int
	Command    interface{}
}

type pendingCommand struct {
	TargetType string
	TargetID   int
	Command    interface{}
	PublishAt  time.Time
	Retries    int
	AckChan    chan bool
}

const (
	ackTimeout      = 5 * time.Second
	maxRetries      = 3
	retryDelay      = 2 * time.Second
	cleanupInterval = 30 * time.Second
)

type CommandDispatcher struct {
	client    mqtt.Client
	db        *database.DB
	hub       Broadcaster
	pending   map[string]*pendingCommand
	pendingMu sync.RWMutex
	CommandCh chan *CommandEvent
}

func NewCommandDispatcher(brokerURL, clientID string, db *database.DB, hub Broadcaster) *CommandDispatcher {
	opts := mqtt.NewClientOptions()
	opts.AddBroker(brokerURL)
	opts.SetClientID(clientID)
	opts.SetAutoReconnect(true)
	opts.SetConnectRetry(true)
	opts.SetConnectRetryInterval(5 * time.Second)
	opts.SetKeepAlive(30 * time.Second)
	return &CommandDispatcher{
		client:    mqtt.NewClient(opts),
		db:        db,
		hub:       hub,
		pending:   make(map[string]*pendingCommand),
		CommandCh: make(chan *CommandEvent, 512),
	}
}

func (d *CommandDispatcher) Start(ctx context.Context) {
	token := d.client.Connect()
	token.Wait()
	if token.Error() != nil {
		log.Printf("MQTT connect error: %v", token.Error())
		return
	}

	subToken := d.client.Subscribe("gas/plc/+/+/feedback", 1, func(_ mqtt.Client, msg mqtt.Message) {
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

		d.pendingMu.RLock()
		pc, exists := d.pending[key]
		d.pendingMu.RUnlock()

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

		d.hub.Broadcast("plc_feedback", map[string]interface{}{
			"targetType":   targetType,
			"targetID":     targetIDStr,
			"success":      feedback.Success,
			"actualValue":  feedback.ActualValue,
			"commandType":  feedback.CommandType,
			"commandValue": feedback.CommandValue,
			"status":       status,
		})
	})
	subToken.Wait()
	if subToken.Error() != nil {
		log.Printf("MQTT subscribe error: %v", subToken.Error())
	}

	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case evt := <-d.CommandCh:
				d.dispatchWithAck(ctx, evt)
			}
		}
	}()

	go func() {
		ticker := time.NewTicker(cleanupInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				d.cleanupPending()
			}
		}
	}()

	log.Println("Command dispatcher started with ACK tracking")
}

func (d *CommandDispatcher) dispatchWithAck(ctx context.Context, evt *CommandEvent) {
	key := commandKey(evt.TargetType, evt.TargetID)

	ackChan := make(chan bool, 1)
	pc := &pendingCommand{
		TargetType: evt.TargetType,
		TargetID:   evt.TargetID,
		Command:    evt.Command,
		PublishAt:  time.Now(),
		Retries:    0,
		AckChan:    ackChan,
	}

	d.pendingMu.Lock()
	d.pending[key] = pc
	d.pendingMu.Unlock()

	defer func() {
		d.pendingMu.Lock()
		delete(d.pending, key)
		d.pendingMu.Unlock()
	}()

	topic := fmt.Sprintf("gas/plc/%s/%d/command", evt.TargetType, evt.TargetID)
	cmdJSON, _ := json.Marshal(evt.Command)
	_, _ = d.db.Pool.Exec(ctx,
		"INSERT INTO plc_commands (target_type, target_id, command_type, command_value, status, mqtt_topic, created_at) VALUES ($1, $2, $3, $4, $5, $6, $7)",
		evt.TargetType, evt.TargetID, "", 0, "sent", topic, time.Now(),
	)

	for attempt := 0; attempt <= maxRetries; attempt++ {
		if attempt > 0 {
			log.Printf("MQTT command retry %d/%d for %s", attempt, maxRetries, key)
			select {
			case <-ctx.Done():
				return
			case <-time.After(retryDelay):
			}
		}

		if err := d.publishCommand(evt.TargetType, evt.TargetID, evt.Command); err != nil {
			log.Printf("MQTT publish error for %s: %v", key, err)
			continue
		}

		d.hub.Broadcast("plc_command_status", map[string]interface{}{
			"targetType": evt.TargetType,
			"targetID":   evt.TargetID,
			"status":     "sent",
			"attempt":    attempt + 1,
		})

		select {
		case acked := <-ackChan:
			if acked {
				d.hub.Broadcast("plc_command_status", map[string]interface{}{
					"targetType": evt.TargetType,
					"targetID":   evt.TargetID,
					"status":     "acked",
					"attempt":    attempt + 1,
				})
				_, _ = d.db.Pool.Exec(ctx,
					"UPDATE plc_commands SET status = $1, result = $2, executed_at = $3 WHERE target_type = $4 AND target_id = $5 AND status = $6 ORDER BY created_at DESC LIMIT 1",
					"acked", string(cmdJSON), time.Now(), evt.TargetType, evt.TargetID, "sent",
				)
				return
			}
			log.Printf("PLC NACK for %s, retrying", key)
		case <-time.After(ackTimeout):
			log.Printf("MQTT ACK timeout for %s (attempt %d/%d)", key, attempt+1, maxRetries)
			d.hub.Broadcast("plc_command_status", map[string]interface{}{
				"targetType": evt.TargetType,
				"targetID":   evt.TargetID,
				"status":     "timeout",
				"attempt":    attempt + 1,
			})
		case <-ctx.Done():
			return
		}
	}

	_, _ = d.db.Pool.Exec(ctx,
		"UPDATE plc_commands SET status = $1, result = $2 WHERE target_type = $3 AND target_id = $4 AND status = $5 ORDER BY created_at DESC LIMIT 1",
		"timeout", fmt.Sprintf("failed after %d retries", maxRetries), evt.TargetType, evt.TargetID, "sent",
	)

	log.Printf("command %s failed after %d retries", key, maxRetries)
}

func commandKey(targetType string, targetID int) string {
	return fmt.Sprintf("%s:%d", targetType, targetID)
}

func (d *CommandDispatcher) publishCommand(targetType string, targetID int, command interface{}) error {
	topic := fmt.Sprintf("gas/plc/%s/%d/command", targetType, targetID)
	payload, err := json.Marshal(command)
	if err != nil {
		return fmt.Errorf("marshal command: %w", err)
	}
	token := d.client.Publish(topic, 1, false, payload)
	token.Wait()
	return token.Error()
}

func (d *CommandDispatcher) Dispatch(ctx context.Context, targetType string, targetID int, command interface{}) error {
	evt := &CommandEvent{
		TargetType: targetType,
		TargetID:   targetID,
		Command:    command,
	}
	select {
	case d.CommandCh <- evt:
		return nil
	case <-ctx.Done():
		return fmt.Errorf("dispatch command cancelled: %w", ctx.Err())
	}
}

func (d *CommandDispatcher) cleanupPending() {
	d.pendingMu.Lock()
	defer d.pendingMu.Unlock()
	now := time.Now()
	for key, pc := range d.pending {
		if now.Sub(pc.PublishAt) > ackTimeout*time.Duration(maxRetries+1)+retryDelay*time.Duration(maxRetries) {
			delete(d.pending, key)
			log.Printf("cleaned up stale pending command: %s", key)
		}
	}
}
