package messaging

import (
	"fmt"
)

// MessageService handles SMS messaging operations
type MessageService struct {
	signalwireClient SignalWireClientInterface
}

// SignalWireClientInterface defines the interface for SignalWire client
type SignalWireClientInterface interface {
	SendSMS(from, to, message string) (*SMSMessage, error)
}

// SMSMessage represents an SMS message
type SMSMessage struct {
	SID       string `json:"sid"`
	From      string `json:"from"`
	To        string `json:"to"`
	Body      string `json:"body"`
	Status    string `json:"status"`
	Direction string `json:"direction"`
	Price     string `json:"price"`
}

// NewMessageService creates a new message service
func NewMessageService(client SignalWireClientInterface) *MessageService {
	return &MessageService{
		signalwireClient: client,
	}
}

// SendBroadcast sends a message to multiple recipients
func (m *MessageService) SendBroadcast(from string, recipients []string, message string) ([]*SMSMessage, []error) {
	var messages []*SMSMessage
	var errors []error

	for _, to := range recipients {
		msg, err := m.signalwireClient.SendSMS(from, to, message)
		if err != nil {
			errors = append(errors, fmt.Errorf("failed to send to %s: %w", to, err))
			continue
		}
		messages = append(messages, msg)
	}

	return messages, errors
}

// SendTemplate sends a message with template variables
func (m *MessageService) SendTemplate(from, to string, template string, vars map[string]string) (*SMSMessage, error) {
	body := template
	for key, value := range vars {
		body = fmt.Sprintf("%s{{.%s}}%s", body, key, value)
	}

	return m.signalwireClient.SendSMS(from, to, body)
}
