# Voice Calling Guide

## Making Calls

```go
call, err := client.MakeCall(
    "+15551234567",           // From
    "+15559876543",           // To
    "https://your-server.com/webhook",  // Webhook URL
    true,                     // Record
)
```

## Handling Incoming Calls

### 1. Create HTTP Handler

```go
func handleIncomingCall(w http.ResponseWriter, r *http.Request) {
    from := r.FormValue("From")
    to := r.FormValue("To")

    log.Printf("Incoming call from %s to %s", from, to)

    // Generate TwiML with WebSocket streaming
    twiml := client.GenerateStreamTwiML(
        "wss://your-server.com/stream/" + sessionID,
    )

    w.Header().Set("Content-Type", "application/xml")
    w.Write([]byte(twiml))
}
```

### 2. Setup WebSocket Streaming

```go
import "github.com/birddigital/signalwire-telephony/pkg/telephony"

bridge := telephony.NewAudioStreamBridge()
server := telephony.NewSignalWireAudioBridge(projectID, token, space, bridge)

// Register HTTP routes
handlers := telephony.NewCallHandlers(server, bridge)
handlers.RegisterRoutes(mux)
```

## Real-Time Audio Streaming

### Getting Audio Channels

```go
// Phone → AI channel (receive audio from caller)
phoneToAIChan, err := bridge.GetPhoneToAIChannel(sessionID)

// AI → Phone channel (send audio to caller)
aiToPhoneChan, err := bridge.GetAIToPhoneChannel(sessionID)
```

### Processing Audio

```go
go func() {
    for audioChunk := range phoneToAIChan {
        // Transcribe (Deepgram, Whisper, etc.)
        transcript := transcribe(audioChunk)

        // Get AI response
        response := ai.Generate(transcript)

        // Synthesize speech (ElevenLabs, etc.)
        ttsAudio := synthesize(response)

        // Send back to caller
        aiToPhoneChan <- ttsAudio
    }
}()
```

## Call Control

### Hangup

```go
err := client.HangupCall(callSID)
```

### Get Call Status

```go
call, err := client.GetCall(callSID)
log.Printf("Call status: %s", call.Status)
```

### Recordings

```go
recording, err := client.GetRecording(recordingSID)
// Save to file
ioutil.WriteFile("call.mp3", recording, 0644)
```

## Webhook Events

SignalWire sends webhook events for call state changes:

| Event | Description |
|-------|-------------|
| `ringing` | Call is ringing |
| `answered` | Call was answered |
| `completed` | Call ended |
| `busy` | Line is busy |
| `no-answer` | No answer |
| `failed` | Call failed |

```go
func handleCallStatus(w http.ResponseWriter, r *http.Request) {
    callSID := r.FormValue("CallSid")
    status := r.FormValue("CallStatus")

    log.Printf("Call %s status: %s", callSID, status)

    w.Write([]byte("<Response></Response>"))
}
```
