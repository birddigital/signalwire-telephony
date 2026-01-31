package main

import (
	"fmt"
	"log"
	"os"

	"github.com/birddigital/signalwire-telephony/pkg/messaging"
	"github.com/birddigital/signalwire-telephony/pkg/signalwire"
)

func main() {
	// Load environment variables
	projectID := os.Getenv("SIGNALWIRE_PROJECT_ID")
	token := os.Getenv("SIGNALWIRE_TOKEN")
	space := os.Getenv("SIGNALWIRE_SPACE")

	if projectID == "" || token == "" || space == "" {
		log.Fatal("Missing required environment variables")
	}

	// Initialize SignalWire client
	client := signalwire.NewClient(projectID, token, space)

	// Create message service
	msgSvc := messaging.NewMessageService(client)

	// Send broadcast to multiple recipients
	from := "+15551234567"
	recipients := []string{
		"+15559876543",
		"+15551122333",
		"+15554455666",
	}
	message := "Hello! This is a broadcast message."

	fmt.Printf("Sending broadcast to %d recipients...\n", len(recipients))

	messages, errors := msgSvc.SendBroadcast(from, recipients, message)

	fmt.Printf("Sent: %d messages\n", len(messages))
	if len(errors) > 0 {
		fmt.Printf("Errors: %d\n", len(errors))
		for _, err := range errors {
			fmt.Printf("  - %v\n", err)
		}
	}

	for _, msg := range messages {
		fmt.Printf("Message SID: %s (Status: %s)\n", msg.SID, msg.Status)
	}
}
