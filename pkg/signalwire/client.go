package signalwire

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Client is a SignalWire API client
type Client struct {
	projectID  string
	token      string
	space      string
	baseURL    string
	httpClient *http.Client
}

// Call represents a SignalWire call
type Call struct {
	SID          string    `json:"sid"`
	From         string    `json:"from"`
	To           string    `json:"to"`
	Status       string    `json:"status"`
	Direction    string    `json:"direction"`
	Duration     string    `json:"duration"`
	StartTime    time.Time `json:"start_time"`
	EndTime      time.Time `json:"end_time"`
	Price        string    `json:"price"`
	RecordingURL string    `json:"recording_url,omitempty"`
}

// Message represents an SMS message
type Message struct {
	SID       string    `json:"sid"`
	From      string    `json:"from"`
	To        string    `json:"to"`
	Body      string    `json:"body"`
	Status    string    `json:"status"`
	Direction string    `json:"direction"`
	DateSent  time.Time `json:"date_sent"`
	Price     string    `json:"price"`
}

// CallRequest options for making a call
type CallRequest struct {
	From             string `json:"From"`
	To               string `json:"To"`
	URL              string `json:"Url"`              // TwiML/LaML webhook URL
	Method           string `json:"Method"`           // POST or GET
	StatusCallback   string `json:"StatusCallback,omitempty"`
	Record           bool   `json:"Record,omitempty"`
	Timeout          int    `json:"Timeout,omitempty"`
	MachineDetection string `json:"MachineDetection,omitempty"` // Enable, DetectMessageEnd
}

// MessageRequest options for sending SMS
type MessageRequest struct {
	From string `json:"From"`
	To   string `json:"To"`
	Body string `json:"Body"`
}

// WebRTCToken for browser-based calls
type WebRTCToken struct {
	Token     string `json:"token"`
	ExpiresAt int64  `json:"expires_at"`
}

// NewClient creates a new SignalWire API client
func NewClient(projectID, token, space string) *Client {
	return &Client{
		projectID: projectID,
		token:     token,
		space:     space,
		baseURL:   fmt.Sprintf("https://%s/api/laml/2010-04-01", space),
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
	}
}

// MakeCall initiates an outbound call
func (c *Client) MakeCall(from, to, webhookURL string, record bool) (*Call, error) {
	if c.projectID == "" || c.token == "" {
		return nil, fmt.Errorf("SignalWire credentials not configured")
	}

	reqURL := fmt.Sprintf("%s/Accounts/%s/Calls.json", c.baseURL, c.projectID)

	formData := url.Values{}
	formData.Set("From", from)
	formData.Set("To", to)
	formData.Set("Url", webhookURL)
	formData.Set("Method", "POST")
	if record {
		formData.Set("Record", "true")
	}
	formData.Set("MachineDetection", "DetectMessageEnd")

	req, err := http.NewRequest("POST", reqURL, strings.NewReader(formData.Encode()))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.SetBasicAuth(c.projectID, c.token)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to make request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("SignalWire API error (%d): %s", resp.StatusCode, string(body))
	}

	var call Call
	if err := json.NewDecoder(resp.Body).Decode(&call); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	return &call, nil
}

// GetCall retrieves call details
func (c *Client) GetCall(callSID string) (*Call, error) {
	if c.projectID == "" || c.token == "" {
		return nil, fmt.Errorf("SignalWire credentials not configured")
	}

	reqURL := fmt.Sprintf("%s/Accounts/%s/Calls/%s.json", c.baseURL, c.projectID, callSID)

	req, err := http.NewRequest("GET", reqURL, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	req.SetBasicAuth(c.projectID, c.token)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to make request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("SignalWire API error (%d): %s", resp.StatusCode, string(body))
	}

	var call Call
	if err := json.NewDecoder(resp.Body).Decode(&call); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	return &call, nil
}

// HangupCall terminates an active call
func (c *Client) HangupCall(callSID string) error {
	if c.projectID == "" || c.token == "" {
		return fmt.Errorf("SignalWire credentials not configured")
	}

	reqURL := fmt.Sprintf("%s/Accounts/%s/Calls/%s.json", c.baseURL, c.projectID, callSID)

	formData := url.Values{}
	formData.Set("Status", "completed")

	req, err := http.NewRequest("POST", reqURL, strings.NewReader(formData.Encode()))
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.SetBasicAuth(c.projectID, c.token)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("failed to make request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("SignalWire API error (%d): %s", resp.StatusCode, string(body))
	}

	return nil
}

// SendSMS sends a text message
func (c *Client) SendSMS(from, to, message string) (*Message, error) {
	if c.projectID == "" || c.token == "" {
		return nil, fmt.Errorf("SignalWire credentials not configured")
	}

	reqURL := fmt.Sprintf("%s/Accounts/%s/Messages.json", c.baseURL, c.projectID)

	formData := url.Values{}
	formData.Set("From", from)
	formData.Set("To", to)
	formData.Set("Body", message)

	req, err := http.NewRequest("POST", reqURL, strings.NewReader(formData.Encode()))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.SetBasicAuth(c.projectID, c.token)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to make request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("SignalWire API error (%d): %s", resp.StatusCode, string(body))
	}

	var msg Message
	if err := json.NewDecoder(resp.Body).Decode(&msg); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	return &msg, nil
}

// GenerateTwiML creates a TwiML/LaML response for call webhooks
func (c *Client) GenerateTwiML(sayText string, gatherDigits bool) string {
	if gatherDigits {
		return fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<Response>
    <Gather numDigits="1" timeout="10" action="/api/webhooks/signalwire/gather">
        <Say voice="Polly.Joanna">%s</Say>
    </Gather>
    <Say voice="Polly.Joanna">We didn't receive any input. Goodbye!</Say>
</Response>`, sayText)
	}

	return fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<Response>
    <Say voice="Polly.Joanna">%s</Say>
</Response>`, sayText)
}

// GenerateStreamTwiML creates TwiML for AI-powered conversation streaming
func (c *Client) GenerateStreamTwiML(streamURL string) string {
	return fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<Response>
    <Connect>
        <Stream url="%s">
            <Parameter name="aCustomParameter" value="aCustomValue" />
        </Stream>
    </Connect>
</Response>`, streamURL)
}

// GetRecording retrieves a call recording
func (c *Client) GetRecording(recordingSID string) ([]byte, error) {
	if c.projectID == "" || c.token == "" {
		return nil, fmt.Errorf("SignalWire credentials not configured")
	}

	reqURL := fmt.Sprintf("%s/Accounts/%s/Recordings/%s.mp3", c.baseURL, c.projectID, recordingSID)

	req, err := http.NewRequest("GET", reqURL, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	req.SetBasicAuth(c.projectID, c.token)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to make request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("SignalWire API error (%d): %s", resp.StatusCode, string(body))
	}

	recording, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read recording data: %w", err)
	}

	return recording, nil
}

// ValidateConfiguration checks if SignalWire is properly configured
func (c *Client) ValidateConfiguration() error {
	if c.projectID == "" {
		return fmt.Errorf("SIGNALWIRE_PROJECT_ID not configured")
	}
	if c.token == "" {
		return fmt.Errorf("SIGNALWIRE_TOKEN not configured")
	}
	if c.space == "" {
		return fmt.Errorf("SIGNALWIRE_SPACE not configured")
	}
	return nil
}

// GetAccountInfo retrieves account information
func (c *Client) GetAccountInfo() (map[string]interface{}, error) {
	if c.projectID == "" || c.token == "" {
		return nil, fmt.Errorf("SignalWire credentials not configured")
	}

	reqURL := fmt.Sprintf("%s/Accounts/%s.json", c.baseURL, c.projectID)

	req, err := http.NewRequest("GET", reqURL, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	req.SetBasicAuth(c.projectID, c.token)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to make request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("SignalWire API error (%d): %s", resp.StatusCode, string(body))
	}

	var accountInfo map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&accountInfo); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	return accountInfo, nil
}

// CalculateCallCost estimates the cost of a call
func (c *Client) CalculateCallCost(durationSeconds int) float64 {
	minutes := float64(durationSeconds) / 60.0
	costPerMinute := 0.01 // SignalWire pricing: ~$0.01 per minute for US calls
	return minutes * costPerMinute
}
