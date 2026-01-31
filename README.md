# SignalWire Telephony SDK for Go

Production-ready Go SDK for SignalWire voice, SMS, and real-time communication.

## Features

- **Voice Calls**: Inbound/outbound calling with real-time audio streaming
- **SMS Messaging**: Send and receive text messages
- **WebSocket Streaming**: Bidirectional audio for AI agents
- **Call Control**: Hangup, recording, status monitoring
- **WebRTC**: Browser-based calling support
- **TwiML/LaML**: XML generation for call flows

## Installation

```bash
go get github.com/birddigital/signalwire-telephony
```

## Quick Start

### SMS Messaging

```go
package main

import (
    "fmt"
    "log"
    "github.com/birddigital/signalwire-telephony/pkg/signalwire"
)

func main() {
    // Initialize client
    client := signalwire.NewClient(
        "your-project-id",
        "your-auth-token",
        "your-space.signalwire.com",
    )

    // Send SMS
    msg, err := client.SendSMS("+15551234567", "+15559876543", "Hello from SignalWire!")
    if err != nil {
        log.Fatal(err)
    }

    fmt.Printf("Message sent! SID: %s\n", msg.SID)
}
```

### Voice Call with AI

```go
package main

import (
    "log"
    "github.com/birddigital/signalwire-telephony/pkg/telephony"
)

func main() {
    // Create audio bridge for real-time streaming
    bridge := telephony.NewAudioStreamBridge()

    // Start WebSocket server
    server := telephony.NewSignalWireAudioBridge(
        "project-id",
        "auth-token",
        "space.signalwire.com",
        bridge,
    )

    // Register HTTP handlers
    handlers := telephony.NewCallHandlers(server, bridge)
    handlers.RegisterRoutes(mux)

    log.Println("Server ready on :8080")
}
```

## Documentation

- [SMS Guide](docs/SMS_GUIDE.md)
- [Voice Calls](docs/VOICE_GUIDE.md)
- [AI Integration](docs/AI_INTEGRATION.md)
- [API Reference](docs/API_REFERENCE.md)

## License

MIT
