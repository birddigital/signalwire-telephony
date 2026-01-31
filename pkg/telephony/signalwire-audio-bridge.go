package telephony

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/websocket"
)

// ============================================
// SIGNALWIRE AUDIO BRIDGE
// Real-time WebSocket audio streaming for phone calls
// ============================================

// SignalWireAudioBridge manages WebSocket connections for SignalWire calls
type SignalWireAudioBridge struct {
	// Active call sessions
	calls map[string]*SignalWireCallSession

	// Session management
	mu sync.RWMutex

	// Configuration
	projectID      string
	authToken      string
	spaceURL       string
	websocketBase  string

	// Audio routing
	audioRouter    *AudioStreamBridge

	// Lifecycle
	ctx            context.Context
	cancel         context.CancelFunc
}

// NewSignalWireAudioBridge creates a new audio bridge
func NewSignalWireAudioBridge(projectID, authToken, space string, audioRouter *AudioStreamBridge) *SignalWireAudioBridge {
	ctx, cancel := context.WithCancel(context.Background())

	return &SignalWireAudioBridge{
		calls:         make(map[string]*SignalWireCallSession),
		projectID:     projectID,
		authToken:     authToken,
		spaceURL:      fmt.Sprintf("https://%s", space),
		websocketBase: fmt.Sprintf("wss://%s", space),
		audioRouter:   audioRouter,
		ctx:           ctx,
		cancel:        cancel,
	}
}

// ============================================
// WEBSOCKET UPGRADE & CONNECTION HANDLING
// ============================================

var signalWireUpgrader = websocket.Upgrader{
	ReadBufferSize:  4096,
	WriteBufferSize: 4096,
	CheckOrigin: func(r *http.Request) bool {
		// Allow SignalWire servers
		return true
	},
}

// HandleWebSocketConnection handles incoming WebSocket connections from SignalWire
func (bridge *SignalWireAudioBridge) HandleWebSocketConnection(w http.ResponseWriter, r *http.Request) {
	// Extract session ID from URL path
	sessionID := r.URL.Query().Get("session_id")
	if sessionID == "" {
		http.Error(w, "session_id required", http.StatusBadRequest)
		return
	}

	// Validate session exists in audio router
	session := bridge.audioRouter.GetSession(sessionID)
	if session == nil {
		http.Error(w, "session not found", http.StatusNotFound)
		return
	}

	log.Printf("[SignalWireBridge] Incoming WebSocket connection for session: %s", sessionID)

	// Upgrade HTTP to WebSocket
	conn, err := signalWireUpgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Printf("[SignalWireBridge] WebSocket upgrade failed: %v", err)
		return
	}

	// Create SignalWire call session
	callSession := &SignalWireCallSession{
		ID:              uuid.New().String(),
		SessionID:       sessionID,
		SignalWireCallSID: r.URL.Query().Get("call_sid"),
		Conn:            conn,
		ConnectedAt:     time.Now(),
		AudioInChan:     make(chan []byte, 100),
		AudioOutChan:    make(chan []byte, 100),
		EventChan:       make(map[string]interface{}),
		ctx:             bridge.ctx,
		mu:              sync.RWMutex{},
	}

	// Register call session
	bridge.mu.Lock()
	bridge.calls[callSession.ID] = callSession
	bridge.mu.Unlock()

	log.Printf("[SignalWireBridge] Call session established: %s (signalwire_sid: %s)",
		callSession.ID, callSession.SignalWireCallSID)

	// Start bidirectional audio streaming
	go callSession.readPump()
	go callSession.writePump()

	// Link with audio router
	bridge.audioRouter.LinkSignalWireSession(sessionID, callSession)

	// Send connection established event
	callSession.SendEvent("connected", map[string]interface{}{
		"session_id":      sessionID,
		"call_session_id": callSession.ID,
		"timestamp":       time.Now().Unix(),
	})
}

// ============================================
// SIGNALWIRE CALL SESSION
// ============================================

// SignalWireCallSession represents an active SignalWire WebSocket connection
type SignalWireCallSession struct {
	// Session identifiers
	ID                string `json:"id"`
	SessionID         string `json:"session_id"`         // Links to AudioStreamSession
	SignalWireCallSID string `json:"signalwire_call_sid"`

	// WebSocket connection
	Conn *websocket.Conn

	// Timing
	ConnectedAt     time.Time `json:"connected_at"`
	LastActivityAt  time.Time `json:"last_activity_at"`

	// Audio channels (bidirectional)
	AudioInChan  chan []byte // Audio FROM SignalWire (phone mic)
	AudioOutChan chan []byte // Audio TO SignalWire (phone speaker)

	// Event handling
	EventChan map[string]interface{} `json:"-"`

	// State
	Closed      bool `json:"closed"`
	ClosedCount int  `json:"closed_count"`

	// Lifecycle
	ctx context.Context
	mu  sync.RWMutex
}

// ============================================
// BIDIRECTIONAL AUDIO STREAMING
// ============================================

// readPump reads messages from SignalWire WebSocket
func (cs *SignalWireCallSession) readPump() {
	defer func() {
		cs.Close()
	}()

	// Set read deadline
	cs.Conn.SetReadDeadline(time.Now().Add(60 * time.Second))

	// Configure ping handler
	cs.Conn.SetPingHandler(func(string) error {
		cs.Conn.SetReadDeadline(time.Now().Add(60 * time.Second))
		cs.mu.Lock()
		cs.LastActivityAt = time.Now()
		cs.mu.Unlock()
		return nil
	})

	for {
		_, message, err := cs.Conn.ReadMessage()
		if err != nil {
			if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseAbnormalClosure) {
				log.Printf("[SignalWireSession] Read error: %v", err)
			}
			break
		}

		// Update activity timestamp
		cs.mu.Lock()
		cs.LastActivityAt = time.Now()
		cs.mu.Unlock()

		// Process message
		if err := cs.handleSignalWireMessage(message); err != nil {
			log.Printf("[SignalWireSession] Message handling error: %v", err)
		}
	}
}

// writePump writes audio data to SignalWire WebSocket
func (cs *SignalWireCallSession) writePump() {
	ticker := time.NewTicker(54 * time.Millisecond) // ~20ms audio chunks at 8kHz
	defer ticker.Stop()
	defer func() {
		cs.Conn.Close()
	}()

	for {
		select {
		case <-cs.ctx.Done():
			return

		case audioChunk, ok := <-cs.AudioOutChan:
			if !ok {
				// Channel closed
				return
			}

			// Send audio to SignalWire
			if err := cs.streamAudioToSignalWire(audioChunk); err != nil {
				log.Printf("[SignalWireSession] Audio send error: %v", err)
				return
			}

		case <-ticker.C:
			// Send keepalive ping
			if err := cs.Conn.WriteMessage(websocket.PingMessage, nil); err != nil {
				return
			}
		}
	}
}

// handleSignalWireMessage processes incoming SignalWire messages
func (cs *SignalWireCallSession) handleSignalWireMessage(data []byte) error {
	var msg map[string]interface{}
	if err := json.Unmarshal(data, &msg); err != nil {
		return fmt.Errorf("failed to parse message: %w", err)
	}

	msgType, ok := msg["event"].(string)
	if !ok {
		return fmt.Errorf("message missing event type")
	}

	switch msgType {
	case "connected":
		log.Printf("[SignalWireSession] Connected event: %+v", msg)
		cs.handleConnectedEvent(msg)

	case "start":
		log.Printf("[SignalWireSession] Start event: %+v", msg)
		cs.handleStartEvent(msg)

	case "media":
		// Audio media from phone call
		return cs.handleMediaEvent(msg)

	case "stop":
		log.Printf("[SignalWireSession] Stop event: %+v", msg)
		cs.handleStopEvent(msg)

	case "closed":
		log.Printf("[SignalWireSession] Closed event: %+v", msg)
		cs.Close()

	default:
		log.Printf("[SignalWireSession] Unknown event type: %s", msgType)
	}

	return nil
}

// ============================================
// SIGNALWIRE EVENT HANDLERS
// ============================================

// handleConnectedEvent handles connection established event
func (cs *SignalWireCallSession) handleConnectedEvent(msg map[string]interface{}) {
	log.Printf("[SignalWireSession] Call connected: %s", cs.SignalWireCallSID)

	cs.SendEvent("connection_ready", map[string]interface{}{
		"call_sid":  cs.SignalWireCallSID,
		"timestamp": time.Now().Unix(),
	})
}

// handleStartEvent handles stream start event
func (cs *SignalWireCallSession) handleStartEvent(msg map[string]interface{}) {
	log.Printf("[SignalWireSession] Media stream started: %s", cs.SignalWireCallSID)

	cs.SendEvent("stream_started", map[string]interface{}{
		"call_sid":  cs.SignalWireCallSID,
		"timestamp": time.Now().Unix(),
	})
}

// handleMediaEvent handles incoming audio media
func (cs *SignalWireCallSession) handleMediaEvent(msg map[string]interface{}) error {
	// Extract media payload
	media, ok := msg["media"].(map[string]interface{})
	if !ok {
		return fmt.Errorf("media event missing payload")
	}

	// Track sequence number
	track, ok := media["track"].(string)
	if !ok {
		return fmt.Errorf("media event missing track")
	}

	// Only process inbound audio (from phone)
	if track != "inbound" {
		return nil
	}

	// Extract base64 audio data
	payload, ok := media["payload"].(string)
	if !ok {
		return fmt.Errorf("media event missing payload")
	}

	// Decode base64 audio
	audioData, err := base64.StdEncoding.DecodeString(payload)
	if err != nil {
		return fmt.Errorf("failed to decode audio payload: %w", err)
	}

	// Send to audio input channel (non-blocking)
	select {
	case cs.AudioInChan <- audioData:
	case <-time.After(10 * time.Millisecond):
		// Channel full, drop chunk
		log.Printf("[SignalWireSession] Audio input channel full, dropping chunk")
	}

	return nil
}

// handleStopEvent handles stream stop event
func (cs *SignalWireCallSession) handleStopEvent(msg map[string]interface{}) {
	log.Printf("[SignalWireSession] Media stream stopped: %s", cs.SignalWireCallSID)

	cs.SendEvent("stream_stopped", map[string]interface{}{
		"call_sid":  cs.SignalWireCallSID,
		"timestamp": time.Now().Unix(),
	})
}

// ============================================
// AUDIO STREAMING
// ============================================

// streamAudioToSignalWire sends audio data to SignalWire WebSocket
func (cs *SignalWireCallSession) streamAudioToSignalWire(audioData []byte) error {
	cs.mu.RLock()
	if cs.Closed {
		cs.mu.RUnlock()
		return fmt.Errorf("session closed")
	}
	cs.mu.RUnlock()

	// Encode audio as base64
	encoded := base64.StdEncoding.EncodeToString(audioData)

	// Construct SignalWire media message
	msg := map[string]interface{}{
		"event": "media",
		"media": map[string]interface{}{
			"track":   "outbound",
			"payload": encoded,
		},
	}

	// Serialize message
	data, err := json.Marshal(msg)
	if err != nil {
		return fmt.Errorf("failed to marshal media message: %w", err)
	}

	// Send to WebSocket
	cs.mu.Lock()
	err = cs.Conn.WriteMessage(websocket.TextMessage, data)
	cs.mu.Unlock()

	if err != nil {
		return fmt.Errorf("failed to send media message: %w", err)
	}

	return nil
}

// SendEvent sends control event to SignalWire
func (cs *SignalWireCallSession) SendEvent(eventType string, data map[string]interface{}) error {
	cs.mu.RLock()
	if cs.Closed {
		cs.mu.RUnlock()
		return fmt.Errorf("session closed")
	}
	cs.mu.RUnlock()

	msg := map[string]interface{}{
		"event": eventType,
	}
	for k, v := range data {
		msg[k] = v
	}

	jsonData, err := json.Marshal(msg)
	if err != nil {
		return fmt.Errorf("failed to marshal event: %w", err)
	}

	cs.mu.Lock()
	err = cs.Conn.WriteMessage(websocket.TextMessage, jsonData)
	cs.mu.Unlock()

	return err
}

// Close closes the SignalWire session
func (cs *SignalWireCallSession) Close() error {
	cs.mu.Lock()
	defer cs.mu.Unlock()

	if cs.Closed {
		return nil
	}

	cs.Closed = true
	cs.ClosedCount++

	// Close channels
	close(cs.AudioInChan)
	close(cs.AudioOutChan)

	// Close WebSocket connection
	if cs.Conn != nil {
		cs.Conn.WriteMessage(
			websocket.CloseMessage,
			websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""),
		)
		cs.Conn.Close()
	}

	log.Printf("[SignalWireSession] Closed: %s", cs.ID)
	return nil
}

// ============================================
// BRIDGE MANAGEMENT
// ============================================

// GetCallSession retrieves a call session by ID
func (bridge *SignalWireAudioBridge) GetCallSession(callSessionID string) *SignalWireCallSession {
	bridge.mu.RLock()
	defer bridge.mu.RUnlock()

	return bridge.calls[callSessionID]
}

// GetCallSessionBySignalWireSID retrieves call session by SignalWire Call SID
func (bridge *SignalWireAudioBridge) GetCallSessionBySignalWireSID(signalWireSID string) *SignalWireCallSession {
	bridge.mu.RLock()
	defer bridge.mu.RUnlock()

	for _, session := range bridge.calls {
		if session.SignalWireCallSID == signalWireSID {
			return session
		}
	}

	return nil
}

// Close closes the audio bridge and all active sessions
func (bridge *SignalWireAudioBridge) Close() error {
	bridge.cancel()

	bridge.mu.Lock()
	defer bridge.mu.Unlock()

	// Close all call sessions
	for _, session := range bridge.calls {
		session.Close()
	}

	bridge.calls = make(map[string]*SignalWireCallSession)

	log.Printf("[SignalWireBridge] Audio bridge closed")
	return nil
}
