# AI Agent Integration Guide

Build AI-powered phone agents with real-time voice conversations.

## Architecture

```
┌─────────────┐
│ SignalWire  │
│ Phone Call  │
└──────┬──────┘
       │ WebSocket (mulaw 8kHz)
       ▼
┌─────────────────────────────────┐
│ SignalWireAudioBridge           │
│ - WebSocket connection           │
│ - SignalWire protocol handling   │
└──────┬──────────────────────────┘
       │ Audio channels
       ▼
┌─────────────────────────────────┐
│ AudioStreamBridge               │
│ - phoneToAIChan (caller → AI)    │
│ - aiToPhoneChan (AI → caller)    │
└──────┬──────────────────────────┘
       │
       ▼
┌─────────────────────────────────┐
│ Your AI Pipeline                │
│ 1. Transcribe (Deepgram)        │
│ 2. Generate response (Claude)   │
│ 3. Synthesize speech (TTS)      │
└─────────────────────────────────┘
```

## Quick Start

```go
package main

import (
    "log"
    "net/http"

    "github.com/birddigital/signalwire-telephony/pkg/telephony"
    sw "github.com/birddigital/signalwire-telephony/pkg/signalwire"
)

func main() {
    // Initialize
    client := sw.NewClient(projectID, token, space)
    bridge := telephony.NewAudioStreamBridge()
    audioServer := telephony.NewSignalWireAudioBridge(projectID, token, space, bridge)

    // Register handlers
    handlers := telephony.NewCallHandlers(audioServer, bridge)
    mux := http.NewServeMux()
    handlers.RegisterRoutes(mux)

    // Start AI audio processor
    go processAudioForAI(bridge)

    log.Println("Starting server on :8080")
    log.Fatal(http.ListenAndServe(":8080", mux))
}
```

## Audio Processing Loop

```go
func processAudioForAI(bridge *telephony.AudioStreamBridge) {
    for {
        // Wait for active call session
        sessionID := bridge.WaitForSession()

        // Get audio channels
        phoneToAI, _ := bridge.GetPhoneToAIChannel(sessionID)
        aiToPhone, _ := bridge.GetAIToPhoneChannel(sessionID)

        // Start processing goroutine
        go handleCall(sessionID, phoneToAI, aiToPhone)
    }
}

func handleCall(sessionID string, input <-chan []byte, output chan<- []byte) {
    for audioChunk := range input {
        // 1. Transcribe audio
        transcript := transcribe(audioChunk)
        if transcript == "" {
            continue
        }

        log.Printf("User: %s", transcript)

        // 2. Get AI response
        response := getAIResponse(transcript)
        log.Printf("AI: %s", response)

        // 3. Synthesize speech
        ttsAudio := synthesize(response)

        // 4. Send to caller
        select {
        case output <- ttsAudio:
            // Sent successfully
        case <-time.After(10 * time.Millisecond):
            log.Printf("Channel full, dropped packet")
        }
    }
}
```

## Integrating Services

### Deepgram Transcription

```go
import "github.com/deepgram-devs/deepgram-go-sdk/pkg/api/live"

dg := deepgram.NewClient(deepgramAPIKey)
stream, _ := dg.Live()

stream.OnMessage(func(msg deepgram.LiveMessage) {
    if msg.Channel.Alternatives[0].Transcript != "" {
        transcript = msg.Channel.Alternatives[0].Transcript
    }
})

// Send audio to Deepgram
stream.Send(audioChunk)
```

### Claude AI

```go
import anthropic "github.com/anthropics/anthropic-go/v2"

client := anthropic.NewClient(anthropicAPIKey)
resp, _ := client.Messages.New(context.Background(), anthropic.MessageNewParams{
    Model:     anthropic.F(claudeModelSonnet),
    MaxTokens: anthropic.Int(500),
    Messages: anthropic.F([]anthropic.MessageParam{
        anthropic.NewUserMessage(anthropic.NewTextBlock(transcript)),
    }),
})

response := resp.Content[0].Text
```

### ElevenLabs TTS

```go
import "github.com/elevenlabs/elevenlabs-go"

client := elevenlabs.NewClient(elevenlabsAPIKey)
audio, _ := client.TextToSpeech(context.Background(), voiceID, elevenlabs.TextToSpeechRequest{
    Text:  response,
    ModelID: "eleven_multilingual_v2",
})

// Convert to mulaw 8kHz for telephony
ttsAudio := convertToMulaw8kHz(audio)
```

## Session Management

### Track Active Calls

```go
type CallSession struct {
    SessionID  string
    CallSID    string
    StartTime  time.Time
    Transcript []string
    Metadata   map[string]string
}

var activeSessions = sync.Map{}

// On call start
func handleIncomingCall(w http.ResponseWriter, r *http.Request) {
    sessionID := uuid.New().String()
    callSID := r.FormValue("CallSid")

    session := &CallSession{
        SessionID: sessionID,
        CallSID:   callSID,
        StartTime: time.Now(),
        Metadata:  make(map[string]string),
    }

    activeSessions.Store(sessionID, session)
}

// On call end
func handleCallEnd(w http.ResponseWriter, r *http.Request) {
    callSID := r.FormValue("CallSid")

    activeSessions.Range(func(key, value interface{}) bool {
        session := value.(*CallSession)
        if session.CallSID == callSID {
            // Save transcript, analytics, etc.
            saveSession(session)
            activeSessions.Delete(key)
            return false
        }
        return true
    })
}
```

## Performance Tips

### Reduce Latency

1. **Stream audio continuously**: Don't wait for silence detection
2. **Use streaming TTS**: Start sending audio before generation completes
3. **Optimize buffer sizes**: Tune channel buffer sizes for your use case
4. **Use connection pooling**: Reuse HTTP clients and API connections

```go
// Optimized buffer sizes
bridge := telephony.NewAudioStreamBridgeWithBuffers(1000) // Increase from default 500
```

### Handle Backpressure

```go
for audioChunk := range input {
    select {
    case output <- ttsAudio:
        // Normal flow
    case <-time.After(10 * time.Millisecond):
        // Channel full - handle gracefully
        log.Printf("Warning: Output channel saturated")
        // Consider: dropping packet, buffering, or slowing down input
    }
}
```

## Monitoring

### Track Call Metrics

```go
type CallMetrics struct {
    SessionID       string
    AudioPacketsIn  int
    AudioPacketsOut int
    LatencyAvg      time.Duration
    Transcripts     int
    AIResponses     int
    Errors          []error
}

func recordMetrics(sessionID string) {
    metrics, _ := bridge.GetMetrics(sessionID)
    log.Printf("Session %s: %d in, %d out, latency %v",
        sessionID,
        metrics.PhoneToAIPacketsSent,
        metrics.AIToPhonePacketsSent,
        metrics.AvgLatency,
    )
}
```

## Testing

### Test Without Real Calls

```go
// Simulate a phone call
func TestAudioFlow(t *testing.T) {
    bridge := telephony.NewAudioStreamBridge()
    sessionID := "test-session"

    bridge.CreateSession(sessionID)
    phoneToAI, _ := bridge.GetPhoneToAIChannel(sessionID)
    aiToPhone, _ := bridge.GetAIToPhoneChannel(sessionID)

    // Simulate caller audio
    go func() {
        for i := 0; i < 10; i++ {
            phoneToAI <- []byte("fake audio data")
        }
    }()

    // Verify AI receives audio
    count := 0
    for range phoneToAI {
        count++
        if count == 10 {
            break
        }
    }

    assert.Equal(t, 10, count)
}
```

## Full Example

See `examples/ai-agent/main.go` for a complete working example.
