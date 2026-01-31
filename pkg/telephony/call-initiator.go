package telephony

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ============================================
// SIGNALWIRE CALL INITIATOR
// Production-ready outbound calling system
// ============================================

// CallInitiator manages SignalWire API interactions for outbound calls
type CallInitiator struct {
	projectID    string
	authToken    string
	space        string
	baseURL      string
	httpClient   *http.Client
	db           *pgxpool.Pool

	// Active call tracking
	activeCalls sync.Map // callSID -> *CallSession
	callsMutex  sync.RWMutex
}

// NewCallInitiator creates a new SignalWire call initiator
func NewCallInitiator(projectID, authToken, space string, db *pgxpool.Pool) *CallInitiator {
	return &CallInitiator{
		projectID:  projectID,
		authToken:  authToken,
		space:      space,
		baseURL:    fmt.Sprintf("https://%s/api/laml/2010-04-01", space),
		httpClient: &http.Client{Timeout: 30 * time.Second},
		db:         db,
	}
}

// ============================================
// CALL CONFIGURATION
// ============================================

// CallConfig defines configuration for initiating a call
type CallConfig struct {
	// Phone Numbers (E.164 format required)
	From string `json:"from"` // Your SignalWire number
	To   string `json:"to"`   // Target number

	// Campaign Context
	CampaignID uuid.UUID `json:"campaign_id,omitempty"`
	TargetID   uuid.UUID `json:"target_id,omitempty"`
	AgencyID   uuid.UUID `json:"agency_id"`

	// Voice Settings
	VoiceID           string  `json:"voice_id,omitempty"`
	VoiceStability    float64 `json:"voice_stability,omitempty"`
	VoiceSimilarity   float64 `json:"voice_similarity,omitempty"`
	SpeakingRate      float64 `json:"speaking_rate,omitempty"`
	Pitch             float64 `json:"pitch,omitempty"`

	// Call Settings
	RingTimeout      int  `json:"ring_timeout,omitempty"`       // Seconds to ring before timeout
	MaxDuration      int  `json:"max_duration,omitempty"`       // Max call duration in seconds
	RecordCall       bool `json:"record_call,omitempty"`        // Enable recording
	RecordStereo     bool `json:"record_stereo,omitempty"`      // Stereo recording
	TranscribeCall   bool `json:"transcribe_call,omitempty"`    // Enable transcription
	DetectVoicemail  bool `json:"detect_voicemail,omitempty"`   // Enable AMD

	// Callback URLs (webhooks)
	AnswerURL          string `json:"answer_url"`           // Called when answered
	StatusCallbackURL  string `json:"status_callback_url"`  // Status updates
	RecordingCallback  string `json:"recording_callback"`   // Recording ready

	// AI Conversation
	ConversationGoal string `json:"conversation_goal,omitempty"` // quote, claim, appointment
	SystemPrompt     string `json:"system_prompt,omitempty"`     // AI system prompt
	GreetingScript   string `json:"greeting_script,omitempty"`   // Initial greeting

	// Metadata
	Metadata map[string]interface{} `json:"metadata,omitempty"`
}

// CallState represents the current state of a call
type CallState string

const (
	StateQueued      CallState = "queued"
	StateInitiated   CallState = "initiated"
	StateRinging     CallState = "ringing"
	StateAnswered    CallState = "answered"
	StateInProgress  CallState = "in_progress"
	StateCompleted   CallState = "completed"
	StateFailed      CallState = "failed"
	StateNoAnswer    CallState = "no_answer"
	StateBusy        CallState = "busy"
	StateCancelled   CallState = "cancelled"
)

// CallStatus represents the overall outcome
type CallStatus string

const (
	StatusInitiated  CallStatus = "initiated"
	StatusRinging    CallStatus = "ringing"
	StatusInProgress CallStatus = "in_progress"
	StatusCompleted  CallStatus = "completed"
	StatusFailed     CallStatus = "failed"
	StatusNoAnswer   CallStatus = "no_answer"
	StatusBusy       CallStatus = "busy"
	StatusCancelled  CallStatus = "cancelled"
)

// CallOutcome represents the final result
type CallOutcome string

const (
	OutcomeCompleted        CallOutcome = "completed"
	OutcomeVoicemailDetected CallOutcome = "voicemail_detected"
	OutcomeUserHungUp       CallOutcome = "user_hung_up"
	OutcomeTimeout          CallOutcome = "timeout"
	OutcomeError            CallOutcome = "error"
	OutcomeNoAnswer         CallOutcome = "no_answer"
	OutcomeBusy             CallOutcome = "busy"
)

// ============================================
// CALL SESSION
// ============================================

// CallSession tracks an active call instance
type CallSession struct {
	ID              uuid.UUID              `json:"id"`
	SignalWireCallSID string               `json:"signalwire_call_sid"`
	CampaignID      *uuid.UUID             `json:"campaign_id,omitempty"`
	TargetID        *uuid.UUID             `json:"target_id,omitempty"`
	AgencyID        uuid.UUID              `json:"agency_id"`

	// Call Details
	FromNumber      string                 `json:"from_number"`
	ToNumber        string                 `json:"to_number"`

	// State Machine
	Status          CallStatus             `json:"status"`
	State           CallState              `json:"state"`

	// Timing
	InitiatedAt     time.Time              `json:"initiated_at"`
	RingingAt       *time.Time             `json:"ringing_at,omitempty"`
	AnsweredAt      *time.Time             `json:"answered_at,omitempty"`
	CompletedAt     *time.Time             `json:"completed_at,omitempty"`

	DurationSeconds int                    `json:"duration_seconds,omitempty"`
	TalkTimeSeconds int                    `json:"talk_time_seconds,omitempty"`
	RingTimeSeconds int                    `json:"ring_time_seconds,omitempty"`

	// Outcome
	Outcome         CallOutcome            `json:"outcome,omitempty"`
	OutcomeReason   string                 `json:"outcome_reason,omitempty"`

	// Recording
	RecordingURL    string                 `json:"recording_url,omitempty"`
	RecordingDuration int                  `json:"recording_duration,omitempty"`

	// Transcription
	TranscriptURL   string                 `json:"transcript_url,omitempty"`
	TranscriptText  string                 `json:"transcript_text,omitempty"`

	// Voicemail Detection
	VoicemailDetected bool                 `json:"voicemail_detected"`
	VoicemailMessageLeft bool              `json:"voicemail_message_left"`

	// Quality Metrics
	AudioQuality    float64                `json:"audio_quality,omitempty"`
	Confidence      float64                `json:"confidence,omitempty"`

	// Cost
	CostUSD         float64                `json:"cost_usd,omitempty"`

	// Error Handling
	ErrorCode       string                 `json:"error_code,omitempty"`
	ErrorMessage    string                 `json:"error_message,omitempty"`

	// Metadata
	Metadata        map[string]interface{} `json:"metadata,omitempty"`

	// Internal
	Config          *CallConfig            `json:"-"`
	CreatedAt       time.Time              `json:"created_at"`
	UpdatedAt       time.Time              `json:"updated_at"`

	mu sync.RWMutex
}

// ============================================
// CALL INITIATION
// ============================================

// InitiateCall starts an outbound call
func (ci *CallInitiator) InitiateCall(ctx context.Context, config CallConfig) (*CallSession, error) {
	// Validate configuration
	if err := ci.validateConfig(&config); err != nil {
		return nil, fmt.Errorf("invalid config: %w", err)
	}

	// Create call session in database
	sessionID := uuid.New()
	session := &CallSession{
		ID:          sessionID,
		AgencyID:    config.AgencyID,
		CampaignID:  nilUUIDToPtr(config.CampaignID),
		TargetID:    nilUUIDToPtr(config.TargetID),
		FromNumber:  config.From,
		ToNumber:    config.To,
		Status:      StatusInitiated,
		State:       StateQueued,
		InitiatedAt: time.Now(),
		CreatedAt:   time.Now(),
		UpdatedAt:   time.Now(),
		Config:      &config,
		Metadata:    config.Metadata,
	}

	// Insert into database
	if err := ci.insertCallSession(ctx, session); err != nil {
		return nil, fmt.Errorf("failed to create session: %w", err)
	}

	// Make SignalWire API call
	swCall, err := ci.makeSignalWireCall(ctx, config, sessionID)
	if err != nil {
		// Update session with error
		session.Status = StatusFailed
		session.State = StateFailed
		session.Outcome = OutcomeError
		session.ErrorMessage = err.Error()
		ci.updateCallSession(ctx, session)
		return nil, fmt.Errorf("SignalWire API error: %w", err)
	}

	// Update session with SignalWire SID
	session.SignalWireCallSID = swCall.SID
	session.State = StateInitiated
	ci.updateCallSession(ctx, session)

	// Track active call
	ci.activeCalls.Store(swCall.SID, session)

	return session, nil
}

// makeSignalWireCall makes the actual API call to SignalWire
func (ci *CallInitiator) makeSignalWireCall(ctx context.Context, config CallConfig, sessionID uuid.UUID) (*SignalWireCallResponse, error) {
	reqURL := fmt.Sprintf("%s/Accounts/%s/Calls.json", ci.baseURL, ci.projectID)

	// Build form data
	formData := url.Values{}
	formData.Set("From", config.From)
	formData.Set("To", config.To)
	formData.Set("Url", config.AnswerURL)
	formData.Set("Method", "POST")

	// Optional settings
	if config.StatusCallbackURL != "" {
		formData.Set("StatusCallback", config.StatusCallbackURL)
		formData.Set("StatusCallbackEvent", "initiated,ringing,answered,completed")
		formData.Set("StatusCallbackMethod", "POST")
	}

	if config.RecordCall {
		formData.Set("Record", "true")
		if config.RecordStereo {
			formData.Set("RecordingChannels", "dual")
		}
		if config.RecordingCallback != "" {
			formData.Set("RecordingStatusCallback", config.RecordingCallback)
		}
	}

	if config.RingTimeout > 0 {
		formData.Set("Timeout", fmt.Sprintf("%d", config.RingTimeout))
	} else {
		formData.Set("Timeout", "30") // Default 30 seconds
	}

	if config.DetectVoicemail {
		formData.Set("MachineDetection", "DetectMessageEnd")
		formData.Set("MachineDetectionTimeout", "5000")  // 5 seconds
		formData.Set("MachineDetectionSpeechThreshold", "2500")
		formData.Set("MachineDetectionSpeechEndThreshold", "1200")
		formData.Set("MachineDetectionSilenceTimeout", "2000")
	}

	// Add custom parameters
	formData.Set("SipHeaders", fmt.Sprintf("X-Session-ID=%s", sessionID.String()))
	if config.CampaignID != uuid.Nil {
		formData.Set("SipHeaders", fmt.Sprintf("X-Campaign-ID=%s", config.CampaignID.String()))
	}

	// Create HTTP request
	req, err := http.NewRequestWithContext(ctx, "POST", reqURL, strings.NewReader(formData.Encode()))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.SetBasicAuth(ci.projectID, ci.authToken)

	// Execute request
	resp, err := ci.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	// Read response
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %w", err)
	}

	if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("API error (%d): %s", resp.StatusCode, string(body))
	}

	// Parse response
	var swCall SignalWireCallResponse
	if err := json.Unmarshal(body, &swCall); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	return &swCall, nil
}

// SignalWireCallResponse represents the API response
type SignalWireCallResponse struct {
	SID              string    `json:"sid"`
	From             string    `json:"from"`
	To               string    `json:"to"`
	Status           string    `json:"status"`
	Direction        string    `json:"direction"`
	StartTime        time.Time `json:"start_time"`
	Price            string    `json:"price,omitempty"`
	AnsweredBy       string    `json:"answered_by,omitempty"`
	CallerName       string    `json:"caller_name,omitempty"`
}

// ============================================
// CALL STATE MANAGEMENT
// ============================================

// UpdateCallState updates the state of an active call
func (ci *CallInitiator) UpdateCallState(ctx context.Context, callSID string, newState CallState, metadata map[string]interface{}) error {
	// Get session
	sessionRaw, ok := ci.activeCalls.Load(callSID)
	if !ok {
		// Try to load from database
		session, err := ci.getCallSessionBySID(ctx, callSID)
		if err != nil {
			return fmt.Errorf("call not found: %s", callSID)
		}
		sessionRaw = session
	}

	session := sessionRaw.(*CallSession)
	session.mu.Lock()
	defer session.mu.Unlock()

	now := time.Now()
	session.State = newState
	session.UpdatedAt = now

	// Update timing based on state
	switch newState {
	case StateRinging:
		session.Status = StatusRinging
		session.RingingAt = &now
		if session.InitiatedAt.Unix() > 0 {
			// Calculate time to ring
			ringDelay := now.Sub(session.InitiatedAt).Seconds()
			if ringDelay > 0 {
				session.Metadata["ring_delay_seconds"] = ringDelay
			}
		}

	case StateAnswered:
		session.Status = StatusInProgress
		session.AnsweredAt = &now
		if session.RingingAt != nil {
			session.RingTimeSeconds = int(now.Sub(*session.RingingAt).Seconds())
		}

	case StateCompleted:
		session.Status = StatusCompleted
		session.CompletedAt = &now
		session.Outcome = OutcomeCompleted
		if session.AnsweredAt != nil {
			session.TalkTimeSeconds = int(now.Sub(*session.AnsweredAt).Seconds())
			session.DurationSeconds = session.RingTimeSeconds + session.TalkTimeSeconds
		} else {
			session.DurationSeconds = int(now.Sub(session.InitiatedAt).Seconds())
		}

	case StateFailed:
		session.Status = StatusFailed
		session.CompletedAt = &now
		session.Outcome = OutcomeError
		session.DurationSeconds = int(now.Sub(session.InitiatedAt).Seconds())

	case StateNoAnswer:
		session.Status = StatusNoAnswer
		session.CompletedAt = &now
		session.Outcome = OutcomeNoAnswer
		session.DurationSeconds = int(now.Sub(session.InitiatedAt).Seconds())

	case StateBusy:
		session.Status = StatusBusy
		session.CompletedAt = &now
		session.Outcome = OutcomeBusy
		session.DurationSeconds = int(now.Sub(session.InitiatedAt).Seconds())
	}

	// Merge metadata
	if metadata != nil {
		for k, v := range metadata {
			session.Metadata[k] = v
		}
	}

	// Update in database
	if err := ci.updateCallSession(ctx, session); err != nil {
		return fmt.Errorf("failed to update session: %w", err)
	}

	return nil
}

// MarkVoicemailDetected marks a call as having detected voicemail
func (ci *CallInitiator) MarkVoicemailDetected(ctx context.Context, callSID string, messageLeft bool) error {
	sessionRaw, ok := ci.activeCalls.Load(callSID)
	if !ok {
		return fmt.Errorf("call not found: %s", callSID)
	}

	session := sessionRaw.(*CallSession)
	session.mu.Lock()
	defer session.mu.Unlock()

	session.VoicemailDetected = true
	session.VoicemailMessageLeft = messageLeft
	session.Outcome = OutcomeVoicemailDetected
	session.UpdatedAt = time.Now()

	return ci.updateCallSession(ctx, session)
}

// SetCallRecording updates recording information
func (ci *CallInitiator) SetCallRecording(ctx context.Context, callSID, recordingURL string, duration int) error {
	sessionRaw, ok := ci.activeCalls.Load(callSID)
	if !ok {
		return fmt.Errorf("call not found: %s", callSID)
	}

	session := sessionRaw.(*CallSession)
	session.mu.Lock()
	defer session.mu.Unlock()

	session.RecordingURL = recordingURL
	session.RecordingDuration = duration
	session.UpdatedAt = time.Now()

	return ci.updateCallSession(ctx, session)
}

// SetCallTranscript updates transcript information
func (ci *CallInitiator) SetCallTranscript(ctx context.Context, callSID, transcriptURL, transcriptText string) error {
	sessionRaw, ok := ci.activeCalls.Load(callSID)
	if !ok {
		return fmt.Errorf("call not found: %s", callSID)
	}

	session := sessionRaw.(*CallSession)
	session.mu.Lock()
	defer session.mu.Unlock()

	session.TranscriptURL = transcriptURL
	session.TranscriptText = transcriptText
	session.UpdatedAt = time.Now()

	return ci.updateCallSession(ctx, session)
}

// ============================================
// CALL CONTROL
// ============================================

// HangupCall terminates an active call
func (ci *CallInitiator) HangupCall(ctx context.Context, callSID string) error {
	reqURL := fmt.Sprintf("%s/Accounts/%s/Calls/%s.json", ci.baseURL, ci.projectID, callSID)

	formData := url.Values{}
	formData.Set("Status", "completed")

	req, err := http.NewRequestWithContext(ctx, "POST", reqURL, strings.NewReader(formData.Encode()))
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.SetBasicAuth(ci.projectID, ci.authToken)

	resp, err := ci.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("API error (%d): %s", resp.StatusCode, string(body))
	}

	// Update local state
	return ci.UpdateCallState(ctx, callSID, StateCancelled, map[string]interface{}{
		"hung_up_by": "system",
	})
}

// GetCallStatus retrieves current call status from SignalWire
func (ci *CallInitiator) GetCallStatus(ctx context.Context, callSID string) (*SignalWireCallResponse, error) {
	reqURL := fmt.Sprintf("%s/Accounts/%s/Calls/%s.json", ci.baseURL, ci.projectID, callSID)

	req, err := http.NewRequestWithContext(ctx, "GET", reqURL, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	req.SetBasicAuth(ci.projectID, ci.authToken)

	resp, err := ci.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("API error (%d): %s", resp.StatusCode, string(body))
	}

	var swCall SignalWireCallResponse
	if err := json.NewDecoder(resp.Body).Decode(&swCall); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	return &swCall, nil
}

// ============================================
// DATABASE OPERATIONS
// ============================================

// insertCallSession inserts a new call session
func (ci *CallInitiator) insertCallSession(ctx context.Context, session *CallSession) error {
	query := `
		INSERT INTO call_sessions (
			id, campaign_id, target_id, agency_id,
			from_number, to_number, status, call_state,
			initiated_at, metadata, created_at, updated_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)
	`

	metadataJSON, _ := json.Marshal(session.Metadata)

	_, err := ci.db.Exec(ctx, query,
		session.ID, session.CampaignID, session.TargetID, session.AgencyID,
		session.FromNumber, session.ToNumber, session.Status, session.State,
		session.InitiatedAt, metadataJSON, session.CreatedAt, session.UpdatedAt,
	)

	return err
}

// updateCallSession updates an existing call session
func (ci *CallInitiator) updateCallSession(ctx context.Context, session *CallSession) error {
	query := `
		UPDATE call_sessions SET
			signalwire_call_sid = $1,
			status = $2,
			call_state = $3,
			ringing_at = $4,
			answered_at = $5,
			completed_at = $6,
			duration_seconds = $7,
			talk_time_seconds = $8,
			ring_time_seconds = $9,
			outcome = $10,
			outcome_reason = $11,
			recording_url = $12,
			recording_duration_seconds = $13,
			transcript_url = $14,
			transcript_text = $15,
			voicemail_detected = $16,
			voicemail_message_left = $17,
			audio_quality_score = $18,
			transcription_confidence = $19,
			cost_usd = $20,
			error_code = $21,
			error_message = $22,
			metadata = $23,
			updated_at = $24
		WHERE id = $25
	`

	metadataJSON, _ := json.Marshal(session.Metadata)

	_, err := ci.db.Exec(ctx, query,
		session.SignalWireCallSID,
		session.Status,
		session.State,
		session.RingingAt,
		session.AnsweredAt,
		session.CompletedAt,
		session.DurationSeconds,
		session.TalkTimeSeconds,
		session.RingTimeSeconds,
		session.Outcome,
		session.OutcomeReason,
		session.RecordingURL,
		session.RecordingDuration,
		session.TranscriptURL,
		session.TranscriptText,
		session.VoicemailDetected,
		session.VoicemailMessageLeft,
		session.AudioQuality,
		session.Confidence,
		session.CostUSD,
		session.ErrorCode,
		session.ErrorMessage,
		metadataJSON,
		session.UpdatedAt,
		session.ID,
	)

	return err
}

// getCallSessionBySID retrieves a call session by SignalWire SID
func (ci *CallInitiator) getCallSessionBySID(ctx context.Context, callSID string) (*CallSession, error) {
	query := `
		SELECT id, campaign_id, target_id, agency_id,
		       signalwire_call_sid, from_number, to_number,
		       status, call_state,
		       initiated_at, ringing_at, answered_at, completed_at,
		       duration_seconds, talk_time_seconds, ring_time_seconds,
		       outcome, outcome_reason,
		       recording_url, recording_duration_seconds,
		       transcript_url, transcript_text,
		       voicemail_detected, voicemail_message_left,
		       audio_quality_score, transcription_confidence,
		       cost_usd, error_code, error_message,
		       metadata, created_at, updated_at
		FROM call_sessions
		WHERE signalwire_call_sid = $1
	`

	var session CallSession
	var metadataJSON []byte

	err := ci.db.QueryRow(ctx, query, callSID).Scan(
		&session.ID, &session.CampaignID, &session.TargetID, &session.AgencyID,
		&session.SignalWireCallSID, &session.FromNumber, &session.ToNumber,
		&session.Status, &session.State,
		&session.InitiatedAt, &session.RingingAt, &session.AnsweredAt, &session.CompletedAt,
		&session.DurationSeconds, &session.TalkTimeSeconds, &session.RingTimeSeconds,
		&session.Outcome, &session.OutcomeReason,
		&session.RecordingURL, &session.RecordingDuration,
		&session.TranscriptURL, &session.TranscriptText,
		&session.VoicemailDetected, &session.VoicemailMessageLeft,
		&session.AudioQuality, &session.Confidence,
		&session.CostUSD, &session.ErrorCode, &session.ErrorMessage,
		&metadataJSON, &session.CreatedAt, &session.UpdatedAt,
	)

	if err != nil {
		return nil, err
	}

	json.Unmarshal(metadataJSON, &session.Metadata)

	return &session, nil
}

// ============================================
// VALIDATION & HELPERS
// ============================================

// validateConfig validates call configuration
func (ci *CallInitiator) validateConfig(config *CallConfig) error {
	if config.From == "" {
		return fmt.Errorf("from number is required")
	}
	if config.To == "" {
		return fmt.Errorf("to number is required")
	}
	if config.AgencyID == uuid.Nil {
		return fmt.Errorf("agency_id is required")
	}
	if config.AnswerURL == "" {
		return fmt.Errorf("answer_url is required")
	}

	// Validate E.164 format
	if !isValidE164(config.From) {
		return fmt.Errorf("from number must be in E.164 format (+1234567890)")
	}
	if !isValidE164(config.To) {
		return fmt.Errorf("to number must be in E.164 format (+1234567890)")
	}

	// Set defaults
	if config.RingTimeout == 0 {
		config.RingTimeout = 30
	}
	if config.MaxDuration == 0 {
		config.MaxDuration = 900 // 15 minutes
	}

	return nil
}

// isValidE164 checks if a phone number is in E.164 format
func isValidE164(phone string) bool {
	if len(phone) < 3 || len(phone) > 15 {
		return false
	}
	if phone[0] != '+' {
		return false
	}
	for _, c := range phone[1:] {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}

// nilUUIDToPtr converts uuid.Nil to nil pointer
func nilUUIDToPtr(id uuid.UUID) *uuid.UUID {
	if id == uuid.Nil {
		return nil
	}
	return &id
}

// GetActiveCallsCount returns the number of active calls
func (ci *CallInitiator) GetActiveCallsCount() int {
	count := 0
	ci.activeCalls.Range(func(key, value interface{}) bool {
		count++
		return true
	})
	return count
}

// CleanupCompletedCalls removes completed calls from active tracking
func (ci *CallInitiator) CleanupCompletedCalls() {
	ci.activeCalls.Range(func(key, value interface{}) bool {
		session := value.(*CallSession)
		if session.Status == StatusCompleted || session.Status == StatusFailed ||
			session.Status == StatusNoAnswer || session.Status == StatusBusy ||
			session.Status == StatusCancelled {
			ci.activeCalls.Delete(key)
		}
		return true
	})
}
