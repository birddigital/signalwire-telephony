package telephony

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"sync"
	"time"
)

// ============================================
// AUDIO STREAM BRIDGE
// Bidirectional audio routing between SignalWire and audio pipeline
// ============================================

// AudioStreamBridge manages bidirectional audio streaming between phone calls and AI
type AudioStreamBridge struct {
	// Active streaming sessions
	sessions map[string]*BridgeSession

	// Session management
	mu sync.RWMutex

	// Lifecycle
	ctx    context.Context
	cancel context.CancelFunc
}

// NewAudioStreamBridge creates a new audio stream bridge
func NewAudioStreamBridge() *AudioStreamBridge {
	ctx, cancel := context.WithCancel(context.Background())

	return &AudioStreamBridge{
		sessions: make(map[string]*BridgeSession),
		ctx:      ctx,
		cancel:   cancel,
	}
}

// ============================================
// BRIDGE SESSION
// ============================================

// BridgeSession links a SignalWire call to an audio pipeline
type BridgeSession struct {
	// Session identifiers
	ID           string `json:"id"`
	SessionID    string `json:"session_id"`    // External session ID

	// SignalWire connection
	SignalWireSession *SignalWireCallSession `json:"-"`

	// Audio buffers for bidirectional streaming
	phoneToAIChan  chan []byte // Audio FROM phone → TO AI
	aiToPhoneChan  chan []byte // Audio FROM AI → TO phone

	// Format conversion
	InputFormat   AudioFormat `json:"input_format"`   // From phone
	OutputFormat  AudioFormat `json:"output_format"`  // To phone

	// State
	Active        bool `json:"active"`
	Streaming     bool `json:"streaming"`

	// Metrics
	Metrics       *BridgeMetrics `json:"metrics"`

	// Lifecycle
	CreatedAt     time.Time `json:"created_at"`
	StartedAt     *time.Time `json:"started_at,omitempty"`
	EndedAt       *time.Time `json:"ended_at,omitempty"`
	ctx           context.Context
	cancel        context.CancelFunc
	mu            sync.RWMutex
}

// AudioFormat defines audio format specifications
type AudioFormat struct {
	SampleRate   int    `json:"sample_rate"`   // 8000 for telephony
	Channels     int    `json:"channels"`      // 1 for mono
	Encoding     string `json:"encoding"`      // "mulaw", "alaw", "pcm"
	BitDepth     int    `json:"bit_depth"`     // 8 for mulaw
}

// BridgeMetrics tracks streaming performance
type BridgeMetrics struct {
	// Audio flow
	PhoneToAIPacketsSent     int64 `json:"phone_to_ai_packets_sent"`
	PhoneToAIPacketsDropped  int64 `json:"phone_to_ai_packets_dropped"`
	AIToPhonePacketsSent     int64 `json:"ai_to_phone_packets_sent"`
	AIToPhonePacketsDropped  int64 `json:"ai_to_phone_packets_dropped"`

	// Latency (microseconds)
	AverageLatencyUs         int64 `json:"average_latency_us"`
	MaxLatencyUs             int64 `json:"max_latency_us"`

	// Throughput
	BytesReceived            int64 `json:"bytes_received"`
	BytesSent                int64 `json:"bytes_sent"`

	// Quality
	DroppedPackets           int64 `json:"dropped_packets"`
	Overruns                 int64 `json:"overruns"`
	Underruns                int64 `json:"underruns"`

	mu                       sync.RWMutex
}

// ============================================
// SESSION MANAGEMENT
// ============================================

// CreateSession creates a new bridge session
func (bridge *AudioStreamBridge) CreateSession(sessionID string) (*BridgeSession, error) {
	bridge.mu.Lock()
	defer bridge.mu.Unlock()

	// Check if session already exists
	if _, exists := bridge.sessions[sessionID]; exists {
		return nil, fmt.Errorf("session already exists: %s", sessionID)
	}

	ctx, cancel := context.WithCancel(bridge.ctx)

	session := &BridgeSession{
		ID:              sessionID,
		SessionID:       sessionID,
		phoneToAIChan:   make(chan []byte, 500),
		aiToPhoneChan:   make(chan []byte, 500),
		InputFormat:     AudioFormat{
			SampleRate: 8000,
			Channels:   1,
			Encoding:   "mulaw",
			BitDepth:   8,
		},
		OutputFormat:    AudioFormat{
			SampleRate: 8000,
			Channels:   1,
			Encoding:   "mulaw",
			BitDepth:   8,
		},
		Active:          true,
		Streaming:       false,
		Metrics:         &BridgeMetrics{},
		CreatedAt:       time.Now(),
		ctx:             ctx,
		cancel:          cancel,
	}

	bridge.sessions[sessionID] = session

	log.Printf("[AudioStreamBridge] Created session: %s", sessionID)
	return session, nil
}

// GetSession retrieves a session by ID
func (bridge *AudioStreamBridge) GetSession(sessionID string) *BridgeSession {
	bridge.mu.RLock()
	defer bridge.mu.RUnlock()

	return bridge.sessions[sessionID]
}

// LinkSignalWireSession links a SignalWire call session to a bridge session
func (bridge *AudioStreamBridge) LinkSignalWireSession(sessionID string, swSession *SignalWireCallSession) error {
	bridge.mu.Lock()
	defer bridge.mu.Unlock()

	session, exists := bridge.sessions[sessionID]
	if !exists {
		return fmt.Errorf("session not found: %s", sessionID)
	}

	session.mu.Lock()
	session.SignalWireSession = swSession
	session.mu.Unlock()

	log.Printf("[AudioStreamBridge] Linked SignalWire session %s to bridge %s",
		swSession.ID, sessionID)

	// Start bidirectional audio routing
	go bridge.routePhoneToAI(session)
	go bridge.routeAIToPhone(session)

	return nil
}

// ============================================
// BIDIRECTIONAL AUDIO ROUTING
// ============================================

// routePhoneToAI routes audio from phone call to AI pipeline
func (bridge *AudioStreamBridge) routePhoneToAI(session *BridgeSession) {
	session.mu.RLock()
	swSession := session.SignalWireSession
	session.mu.RUnlock()

	if swSession == nil {
		log.Printf("[AudioStreamBridge] No SignalWire session linked for %s", session.ID)
		return
	}

	log.Printf("[AudioStreamBridge] Starting phone → AI audio routing: %s", session.ID)

	// Mark streaming as active
	session.mu.Lock()
	session.Streaming = true
	now := time.Now()
	session.StartedAt = &now
	session.mu.Unlock()

	defer func() {
		session.mu.Lock()
		session.Streaming = false
		endTime := time.Now()
		session.EndedAt = &endTime
		session.mu.Unlock()
	}()

	for {
		select {
		case <-session.ctx.Done():
			log.Printf("[AudioStreamBridge] Stopping phone → AI routing: %s", session.ID)
			return

		case audioChunk := <-swSession.AudioInChan:
			startTime := time.Now()

			// Validate audio data
			if len(audioChunk) == 0 {
				continue
			}

			// Process audio format if needed
			processedAudio, err := bridge.processIncomingAudio(audioChunk, session)
			if err != nil {
				log.Printf("[AudioStreamBridge] Audio processing error: %v", err)
				continue
			}

			// Send to AI pipeline (non-blocking)
			select {
			case session.phoneToAIChan <- processedAudio:
				session.Metrics.mu.Lock()
				session.Metrics.PhoneToAIPacketsSent++
				session.Metrics.BytesReceived += int64(len(audioChunk))
				session.Metrics.mu.Unlock()

				// Track latency
				latency := time.Since(startTime).Microseconds()
				session.updateLatency(latency)

			case <-time.After(10 * time.Millisecond):
				// Channel full, drop packet
				session.Metrics.mu.Lock()
				session.Metrics.PhoneToAIPacketsDropped++
				session.Metrics.DroppedPackets++
				session.Metrics.mu.Unlock()

				log.Printf("[AudioStreamBridge] Phone → AI channel full, dropped packet")
			}
		}
	}
}

// routeAIToPhone routes audio from AI pipeline to phone call
func (bridge *AudioStreamBridge) routeAIToPhone(session *BridgeSession) {
	session.mu.RLock()
	swSession := session.SignalWireSession
	session.mu.RUnlock()

	if swSession == nil {
		log.Printf("[AudioStreamBridge] No SignalWire session linked for %s", session.ID)
		return
	}

	log.Printf("[AudioStreamBridge] Starting AI → phone audio routing: %s", session.ID)

	for {
		select {
		case <-session.ctx.Done():
			log.Printf("[AudioStreamBridge] Stopping AI → phone routing: %s", session.ID)
			return

		case audioChunk := <-session.aiToPhoneChan:
			startTime := time.Now()

			// Validate audio data
			if len(audioChunk) == 0 {
				continue
			}

			// Convert audio format if needed
			processedAudio, err := bridge.processOutgoingAudio(audioChunk, session)
			if err != nil {
				log.Printf("[AudioStreamBridge] Audio conversion error: %v", err)
				continue
			}

			// Send to SignalWire session (non-blocking)
			select {
			case swSession.AudioOutChan <- processedAudio:
				session.Metrics.mu.Lock()
				session.Metrics.AIToPhonePacketsSent++
				session.Metrics.BytesSent += int64(len(processedAudio))
				session.Metrics.mu.Unlock()

				// Track latency
				latency := time.Since(startTime).Microseconds()
				session.updateLatency(latency)

			case <-time.After(10 * time.Millisecond):
				// Channel full, drop packet
				session.Metrics.mu.Lock()
				session.Metrics.AIToPhonePacketsDropped++
				session.Metrics.DroppedPackets++
				session.Metrics.mu.Unlock()

				log.Printf("[AudioStreamBridge] AI → phone channel full, dropped packet")
			}
		}
	}
}

// ============================================
// AUDIO FORMAT CONVERSION
// ============================================

// processIncomingAudio processes audio from phone (8kHz mulaw → pipeline format)
func (bridge *AudioStreamBridge) processIncomingAudio(audioData []byte, session *BridgeSession) ([]byte, error) {
	// Currently, we pass through mulaw audio directly
	// The audio pipeline handles mulaw decoding

	// TODO: Add resampling if pipeline requires different sample rate
	// TODO: Add codec conversion if needed

	return audioData, nil
}

// processOutgoingAudio processes audio from AI (pipeline format → 8kHz mulaw)
func (bridge *AudioStreamBridge) processOutgoingAudio(audioData []byte, session *BridgeSession) ([]byte, error) {
	// Currently, we assume TTS generates mulaw audio
	// If TTS outputs different format, conversion happens here

	// TODO: Add resampling if TTS outputs different sample rate
	// TODO: Add codec conversion if needed

	return audioData, nil
}

// ============================================
// CHANNEL ACCESS
// ============================================

// GetPhoneToAIChannel returns the channel for phone → AI audio
func (bridge *AudioStreamBridge) GetPhoneToAIChannel(sessionID string) (<-chan []byte, error) {
	session := bridge.GetSession(sessionID)
	if session == nil {
		return nil, fmt.Errorf("session not found: %s", sessionID)
	}

	return session.phoneToAIChan, nil
}

// GetAIToPhoneChannel returns the channel for AI → phone audio
func (bridge *AudioStreamBridge) GetAIToPhoneChannel(sessionID string) (chan<- []byte, error) {
	session := bridge.GetSession(sessionID)
	if session == nil {
		return nil, fmt.Errorf("session not found: %s", sessionID)
	}

	return session.aiToPhoneChan, nil
}

// ============================================
// METRICS & MONITORING
// ============================================

// updateLatency updates latency metrics
func (session *BridgeSession) updateLatency(latencyUs int64) {
	session.Metrics.mu.Lock()
	defer session.Metrics.mu.Unlock()

	// Calculate rolling average
	if session.Metrics.AverageLatencyUs == 0 {
		session.Metrics.AverageLatencyUs = latencyUs
	} else {
		// Exponential moving average (alpha = 0.1)
		session.Metrics.AverageLatencyUs = (session.Metrics.AverageLatencyUs*9 + latencyUs) / 10
	}

	// Track max latency
	if latencyUs > session.Metrics.MaxLatencyUs {
		session.Metrics.MaxLatencyUs = latencyUs
	}
}

// GetMetrics returns current bridge metrics for a session
func (bridge *AudioStreamBridge) GetMetrics(sessionID string) (*BridgeMetrics, error) {
	session := bridge.GetSession(sessionID)
	if session == nil {
		return nil, fmt.Errorf("session not found: %s", sessionID)
	}

	session.Metrics.mu.RLock()
	defer session.Metrics.mu.RUnlock()

	// Create a copy without the mutex
	metricsCopy := BridgeMetrics{
		PhoneToAIPacketsSent:    session.Metrics.PhoneToAIPacketsSent,
		PhoneToAIPacketsDropped: session.Metrics.PhoneToAIPacketsDropped,
		AIToPhonePacketsSent:    session.Metrics.AIToPhonePacketsSent,
		AIToPhonePacketsDropped: session.Metrics.AIToPhonePacketsDropped,
		AverageLatencyUs:        session.Metrics.AverageLatencyUs,
		MaxLatencyUs:            session.Metrics.MaxLatencyUs,
		BytesReceived:           session.Metrics.BytesReceived,
		BytesSent:               session.Metrics.BytesSent,
		DroppedPackets:          session.Metrics.DroppedPackets,
		Overruns:                session.Metrics.Overruns,
		Underruns:               session.Metrics.Underruns,
	}
	return &metricsCopy, nil
}

// GetSessionStatus returns the status of a bridge session
func (bridge *AudioStreamBridge) GetSessionStatus(sessionID string) (map[string]interface{}, error) {
	session := bridge.GetSession(sessionID)
	if session == nil {
		return nil, fmt.Errorf("session not found: %s", sessionID)
	}

	session.mu.RLock()
	defer session.mu.RUnlock()

	status := map[string]interface{}{
		"id":              session.ID,
		"session_id":      session.SessionID,
		"active":          session.Active,
		"streaming":       session.Streaming,
		"created_at":      session.CreatedAt,
		"started_at":      session.StartedAt,
		"ended_at":        session.EndedAt,
		"input_format":    session.InputFormat,
		"output_format":   session.OutputFormat,
	}

	return status, nil
}

// ============================================
// BRIDGE SESSION INTERFACE METHODS
// Methods to satisfy transcription.BridgeSessionInterface
// ============================================

// GetSessionID returns the session ID
func (s *BridgeSession) GetSessionID() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.SessionID
}

// GetPhoneToAIChannel returns the audio channel from phone to AI
func (s *BridgeSession) GetPhoneToAIChannel() <-chan []byte {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.phoneToAIChan
}

// GetContext returns the session context
func (s *BridgeSession) GetContext() context.Context {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.ctx
}

// IsActive returns whether the session is active
func (s *BridgeSession) IsActive() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.Active
}

// ============================================
// SESSION LIFECYCLE
// ============================================

// CloseSession closes a bridge session
func (bridge *AudioStreamBridge) CloseSession(sessionID string) error {
	bridge.mu.Lock()
	defer bridge.mu.Unlock()

	session, exists := bridge.sessions[sessionID]
	if !exists {
		return fmt.Errorf("session not found: %s", sessionID)
	}

	session.mu.Lock()
	session.Active = false
	session.cancel()
	session.mu.Unlock()

	// Close channels
	close(session.phoneToAIChan)
	close(session.aiToPhoneChan)

	delete(bridge.sessions, sessionID)

	log.Printf("[AudioStreamBridge] Closed session: %s", sessionID)
	return nil
}

// Close closes the audio stream bridge and all sessions
func (bridge *AudioStreamBridge) Close() error {
	bridge.cancel()

	bridge.mu.Lock()
	defer bridge.mu.Unlock()

	// Close all sessions
	for sessionID := range bridge.sessions {
		bridge.CloseSession(sessionID)
	}

	log.Printf("[AudioStreamBridge] Audio stream bridge closed")
	return nil
}

// ============================================
// SIGNALWIRE WEBSOCKET SERVER
// ============================================

// SignalWireWebSocketServer serves SignalWire WebSocket connections
type SignalWireWebSocketServer struct {
	bridge *SignalWireAudioBridge
}

// NewSignalWireWebSocketServer creates a new WebSocket server
func NewSignalWireWebSocketServer(bridge *SignalWireAudioBridge) *SignalWireWebSocketServer {
	return &SignalWireWebSocketServer{
		bridge: bridge,
	}
}

// ServeHTTP handles HTTP requests for WebSocket upgrade
func (server *SignalWireWebSocketServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	server.bridge.HandleWebSocketConnection(w, r)
}
