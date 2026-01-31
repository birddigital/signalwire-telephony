package telephony

import (
	"context"
	"encoding/xml"
	"fmt"
	"log"
	"net/http"
	"path"

	"github.com/google/uuid"
)

// ============================================
// SIGNALWIRE CALL HANDLERS
// HTTP endpoints for call control and WebSocket streaming
// ============================================

// CallHandlers manages HTTP endpoints for SignalWire call control
type CallHandlers struct {
	callInitiator *CallInitiator
	audioBridge   *SignalWireAudioBridge
	streamBridge  *AudioStreamBridge
}

// NewCallHandlers creates a new call handlers instance
func NewCallHandlers(initiator *CallInitiator, audioBridge *SignalWireAudioBridge, streamBridge *AudioStreamBridge) *CallHandlers {
	return &CallHandlers{
		callInitiator: initiator,
		audioBridge:   audioBridge,
		streamBridge:  streamBridge,
	}
}

// ============================================
// TWIML GENERATION
// ============================================

// TwiMLResponse represents TwiML verb structure
type TwiMLResponse struct {
	XMLName xml.Name `xml:"Response"`
	*Start  `xml:",innerxml"`
}

// Start represents the <Start> verb for WebSocket streaming
type Start struct {
	XMLName  xml.Name `xml:"Start"`
	Streams []Stream `xml:"Stream"`
}

// Stream represents a <Stream> element
type Stream struct {
	XMLName    xml.Name `xml:"Stream"`
	URL        string   `xml:"url,attr"`
	Track      string   `xml:"track,attr"` // "inbound", "outbound", "both"
}

// ============================================
// HTTP HANDLERS
// ============================================

// HandleIncomingCall handles incoming call from SignalWire
// Returns TwiML with WebSocket streaming instructions
func (h *CallHandlers) HandleIncomingCall(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Extract call parameters
	callSID := r.FormValue("CallSid")
	from := r.FormValue("From")
	to := r.FormValue("To")

	if callSID == "" {
		log.Printf("[CallHandlers] Missing CallSid in request")
		http.Error(w, "Missing CallSid", http.StatusBadRequest)
		return
	}

	log.Printf("[CallHandlers] Incoming call: %s (from: %s, to: %s)", callSID, from, to)

	// Create bridge session
	sessionID := uuid.New().String()
	_, err := h.streamBridge.CreateSession(sessionID)
	if err != nil {
		log.Printf("[CallHandlers] Failed to create bridge session: %v", err)
		http.Error(w, "Failed to create session", http.StatusInternalServerError)
		return
	}

	log.Printf("[CallHandlers] Created bridge session: %s for call: %s", sessionID, callSID)

	// Construct WebSocket URL for SignalWire
	scheme := "https"
	if r.TLS != nil {
		scheme = "wss"
	} else {
		// Use wss for production
		scheme = "wss"
	}

	host := r.Host
	wsURL := fmt.Sprintf("%s://%s/api/telephony/calls/stream/%s",
		scheme, host, sessionID)

	// Add query parameters
	wsURL = fmt.Sprintf("%s?call_sid=%s", wsURL, callSID)

	log.Printf("[CallHandlers] WebSocket URL: %s", wsURL)

	// Generate TwiML with WebSocket streaming
	twiml := &TwiMLResponse{
		Start: &Start{
			Streams: []Stream{
				{
					URL:   wsURL,
					Track: "both", // Stream both inbound and outbound audio
				},
			},
		},
	}

	// Marshal to XML
	output, err := xml.Marshal(twiml)
	if err != nil {
		log.Printf("[CallHandlers] Failed to marshal TwiML: %v", err)
		http.Error(w, "Failed to generate TwiML", http.StatusInternalServerError)
		return
	}

	// Set content type and return
	w.Header().Set("Content-Type", "application/xml")
	w.WriteHeader(http.StatusOK)
	w.Write(output)

	log.Printf("[CallHandlers] Returned TwiML for call: %s", callSID)
}

// HandleCallStateChange handles call state events from SignalWire
func (h *CallHandlers) HandleCallStateChange(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Extract call parameters
	callSID := r.FormValue("CallSid")
	callStatus := r.FormValue("CallStatus")

	if callSID == "" {
		http.Error(w, "Missing CallSid", http.StatusBadRequest)
		return
	}

	log.Printf("[CallHandlers] Call state change: %s (status: %s)", callSID, callStatus)

	// Map SignalWire status to CallState
	var newState CallState
	switch callStatus {
	case "ringing":
		newState = StateRinging
	case "in-progress", "answered":
		newState = StateAnswered
	case "completed":
		newState = StateCompleted
	case "failed", "error":
		newState = StateFailed
	case "no-answer":
		newState = StateNoAnswer
	case "busy":
		newState = StateBusy
	case "canceled":
		newState = StateCancelled
	default:
		log.Printf("[CallHandlers] Unknown call status: %s", callStatus)
		newState = StateFailed
	}

	// Update call state in initiator
	ctx := context.Background()
	if err := h.callInitiator.UpdateCallState(ctx, callSID, newState, nil); err != nil {
		log.Printf("[CallHandlers] Failed to update call state: %v", err)
		// Don't return error - SignalWire doesn't care about our internal state
	}

	// Handle call completion
	if newState == StateCompleted || newState == StateFailed ||
	   newState == StateNoAnswer || newState == StateBusy ||
	   newState == StateCancelled {

		// Find and close associated bridge session
		if swSession := h.audioBridge.GetCallSessionBySignalWireSID(callSID); swSession != nil {
			log.Printf("[CallHandlers] Closing bridge session: %s for call: %s",
				swSession.SessionID, callSID)
			h.streamBridge.CloseSession(swSession.SessionID)
		}
	}

	w.WriteHeader(http.StatusOK)
}

// ============================================
// WEBSOCKET ENDPOINT
// ============================================

// HandleCallStream handles WebSocket connections from SignalWire
func (h *CallHandlers) HandleCallStream(w http.ResponseWriter, r *http.Request) {
	// Extract session ID from URL path
	sessionID := path.Base(r.URL.Path)

	log.Printf("[CallHandlers] WebSocket connection request for session: %s", sessionID)

	// Delegate to audio bridge
	h.audioBridge.HandleWebSocketConnection(w, r)
}

// ============================================
// HEALTH & STATUS ENDPOINTS
// ============================================

// HandleBridgeStatus returns the status of a bridge session
func (h *CallHandlers) HandleBridgeStatus(w http.ResponseWriter, r *http.Request) {
	sessionID := r.URL.Query().Get("session_id")
	if sessionID == "" {
		http.Error(w, "Missing session_id", http.StatusBadRequest)
		return
	}

	status, err := h.streamBridge.GetSessionStatus(sessionID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	// Write JSON response
	// TODO: Use json.Marshal
	fmt.Fprintf(w, "%+v", status)
}

// HandleBridgeMetrics returns metrics for a bridge session
func (h *CallHandlers) HandleBridgeMetrics(w http.ResponseWriter, r *http.Request) {
	sessionID := r.URL.Query().Get("session_id")
	if sessionID == "" {
		http.Error(w, "Missing session_id", http.StatusBadRequest)
		return
	}

	metrics, err := h.streamBridge.GetMetrics(sessionID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	// Write JSON response
	// TODO: Use json.Marshal
	fmt.Fprintf(w, "%+v", metrics)
}

// ============================================
// ROUTE REGISTRATION
// ============================================

// RegisterRoutes registers all call handler routes
func (h *CallHandlers) RegisterRoutes(mux *http.ServeMux) {
	// TwiML endpoints
	mux.HandleFunc("/api/telephony/calls/incoming", h.HandleIncomingCall)
	mux.HandleFunc("/api/telephony/calls/status", h.HandleCallStateChange)

	// WebSocket endpoint
	mux.HandleFunc("/api/telephony/calls/stream/", h.HandleCallStream)

	// Status endpoints
	mux.HandleFunc("/api/telephony/calls/bridge/status", h.HandleBridgeStatus)
	mux.HandleFunc("/api/telephony/calls/bridge/metrics", h.HandleBridgeMetrics)

	log.Printf("[CallHandlers] Registered call handler routes")
}
