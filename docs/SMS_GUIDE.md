# SMS Messaging Guide

## Quick Start

```go
package main

import (
    "log"
    "github.com/birddigital/signalwire-telephony/pkg/signalwire"
)

func main() {
    client := signalwire.NewClient(
        "project-id",
        "auth-token",
        "space.signalwire.com",
    )

    msg, err := client.SendSMS("+15551234567", "+15559876543", "Hello!")
    if err != nil {
        log.Fatal(err)
    }

    log.Printf("Message sent: %s", msg.SID)
}
```

## Broadcast Messaging

```go
import "github.com/birddigital/signalwire-telephony/pkg/messaging"

msgSvc := messaging.NewMessageService(client)

recipients := []string{"+1555111222", "+1555333444"}
messages, errors := msgSvc.SendBroadcast("+15551234567", recipients, "Broadcast message")
```

## Webhook Handling

When SignalWire receives an SMS, it sends a webhook to your configured URL:

```go
func handleSMSWebhook(w http.ResponseWriter, r *http.Request) {
    from := r.FormValue("From")
    to := r.FormValue("To")
    body := r.FormValue("Body")

    log.Printf("SMS from %s: %s", from, body)

    // Send reply
    msg, err := client.SendSMS(to, from, "Message received!")
    if err != nil {
        log.Fatal(err)
    }

    w.Write([]byte("OK"))
}
```

## Environment Variables

```bash
export SIGNALWIRE_PROJECT_ID="your-project-id"
export SIGNALWIRE_AUTH_TOKEN="your-token"
export SIGNALWIRE_SPACE="your-space.signalwire.com"
```
