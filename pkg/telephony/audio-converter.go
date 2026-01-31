package telephony

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"math"
)

// Common audio format constants
var (
	AudioFormatMulaw = AudioFormat{SampleRate: 8000, Channels: 1, Encoding: "mulaw", BitDepth: 8}
	AudioFormatPCM   = AudioFormat{SampleRate: 16000, Channels: 1, Encoding: "pcm", BitDepth: 16}
	AudioFormatWAV   = AudioFormat{SampleRate: 16000, Channels: 1, Encoding: "wav", BitDepth: 16}
)

// ============================================
// AUDIO FORMAT CONVERSION
// ============================================
// Converts between different audio formats for telephony and AI services
//
// Supported conversions:
// - mulaw 8kHz → PCM 16kHz (for Deepgram)
// - PCM 16kHz → mulaw 8kHz (for telephony playback)
// - Sample rate conversion
// - Channel conversion (mono/stereo)
// ============================================

// AudioConverter handles audio format conversions
type AudioConverter struct {
	// Configuration
	inputSampleRate  int
	outputSampleRate int
	inputChannels    int
	outputChannels   int
}

// NewAudioConverter creates a new audio converter
func NewAudioConverter(inputSampleRate, outputSampleRate int, inputChannels, outputChannels int) *AudioConverter {
	return &AudioConverter{
		inputSampleRate:  inputSampleRate,
		outputSampleRate: outputSampleRate,
		inputChannels:    inputChannels,
		outputChannels:   outputChannels,
	}
}

// MulawToPCM16kHz converts mulaw 8kHz mono to PCM 16kHz mono
// This is the primary conversion needed for Deepgram streaming
func (c *AudioConverter) MulawToPCM16kHz(mulawData []byte) ([]byte, error) {
	// Step 1: Decode mulaw to 16-bit PCM (still at 8kHz)
	pcm8kHz, err := c.decodeMulaw(mulawData)
	if err != nil {
		return nil, fmt.Errorf("failed to decode mulaw: %w", err)
	}

	// Step 2: Resample from 8kHz to 16kHz
	pcm16kHz, err := c.resamplePCM16(pcm8kHz, 8000, 16000)
	if err != nil {
		return nil, fmt.Errorf("failed to resample: %w", err)
	}

	return pcm16kHz, nil
}

// PCM16kHzToMulaw converts PCM 16kHz mono to mulaw 8kHz mono
// Used for sending AI audio back to telephony system
func (c *AudioConverter) PCM16kHzToMulaw(pcmData []byte) ([]byte, error) {
	// Step 1: Resample from 16kHz to 8kHz
	pcm8kHz, err := c.resamplePCM16(pcmData, 16000, 8000)
	if err != nil {
		return nil, fmt.Errorf("failed to resample: %w", err)
	}

	// Step 2: Encode PCM to mulaw
	mulawData, err := c.encodeMulaw(pcm8kHz)
	if err != nil {
		return nil, fmt.Errorf("failed to encode mulaw: %w", err)
	}

	return mulawData, nil
}

// decodeMulaw decodes mulaw encoded audio to 16-bit PCM
func (c *AudioConverter) decodeMulaw(mulawData []byte) ([]byte, error) {
	// Mulaw decoding table (standard G.711)
	// Each mulaw byte (8-bit) maps to a 16-bit PCM sample
	pcmData := make([]byte, len(mulawData)*2)

	for i, mulawByte := range mulawData {
		// Complement the mulaw byte (flip all bits)
		mulawByte ^= 0xFF

		// Extract components
		sign := int16(1)
		if (mulawByte & 0x80) != 0 {
			sign = -1
		}

		exponent := (mulawByte >> 4) & 0x07
		mantissa := mulawByte & 0x0F

		// Convert to linear 16-bit PCM using G.711 formula
		sample := int16(sign * (((int16(mantissa) << 3) + 0x84) << exponent))

		// Store as little-endian 16-bit PCM
		binary.LittleEndian.PutUint16(pcmData[i*2:i*2+2], uint16(sample))
	}

	return pcmData, nil
}

// encodeMulaw encodes 16-bit PCM to mulaw
func (c *AudioConverter) encodeMulaw(pcmData []byte) ([]byte, error) {
	if len(pcmData)%2 != 0 {
		return nil, fmt.Errorf("PCM data length must be even (16-bit samples)")
	}

	// Number of 16-bit samples
	numSamples := len(pcmData) / 2
	mulawData := make([]byte, numSamples)

	for i := 0; i < numSamples; i++ {
		// Read 16-bit PCM sample (little-endian)
		sample := int16(binary.LittleEndian.Uint16(pcmData[i*2 : i*2+2]))

		// Encode to mulaw
		mulawData[i] = c.linearToMulaw(sample)
	}

	return mulawData, nil
}

// linearToMulaw converts a linear 16-bit PCM sample to mulaw
func (c *AudioConverter) linearToMulaw(sample int16) byte {
	// Get the absolute value and sign
	sign := int16(1)
	if sample < 0 {
		sign = -1
		sample = -sample
	}

	// Clamp to maximum mulaw value
	if sample > 32635 {
		sample = 32635
	}

	// Convert to mulaw using the standard G.711 algorithm
	exponent := int16(7)
	for exp := int16(0); exp < 7; exp++ {
		if sample <= (int16(1) << (exp + 5)) {
			exponent = exp
			break
		}
	}

	mantissa := sample >> (exponent + 1)

	// Compose the mulaw byte
	mulawByte := byte((exponent << 4) | mantissa)

	// Set sign bit
	if sign < 0 {
		mulawByte |= 0x80
	}

	// Invert for transmission (MSB is sign bit)
	return mulawByte ^ 0xFF
}

// resamplePCM16 resamples 16-bit PCM audio from one sample rate to another
// Uses simple linear interpolation (good enough for telephony)
func (c *AudioConverter) resamplePCM16(pcmData []byte, fromRate, toRate int) ([]byte, error) {
	if len(pcmData)%2 != 0 {
		return nil, fmt.Errorf("PCM data length must be even (16-bit samples)")
	}

	// Calculate number of samples
	numInputSamples := len(pcmData) / 2
	numOutputSamples := (numInputSamples * toRate) / fromRate

	// Create output buffer
	outputData := make([]byte, numOutputSamples*2)

	// Resample using linear interpolation
	ratio := float64(fromRate) / float64(toRate)

	for i := 0; i < numOutputSamples; i++ {
		// Calculate source position
		srcPos := float64(i) * ratio

		// Get integer sample index
		srcIndex := int(srcPos)

		// Bounds check
		if srcIndex >= numInputSamples-1 {
			srcIndex = numInputSamples - 2
		}

		// Get fractional part for interpolation
		fraction := srcPos - float64(srcIndex)

		// Read two consecutive samples
		sample1 := int16(binary.LittleEndian.Uint16(pcmData[srcIndex*2 : (srcIndex+1)*2]))
		sample2 := int16(binary.LittleEndian.Uint16(pcmData[(srcIndex+1)*2 : (srcIndex+2)*2]))

		// Linear interpolation
		interpolated := float64(sample1)*(1-fraction) + float64(sample2)*fraction

		// Clamp to 16-bit range
		if interpolated > math.MaxInt16 {
			interpolated = math.MaxInt16
		} else if interpolated < math.MinInt16 {
			interpolated = math.MinInt16
		}

		// Write output sample
		binary.LittleEndian.PutUint16(outputData[i*2:(i+1)*2], uint16(int16(interpolated)))
	}

	return outputData, nil
}

// ConvertAudio converts audio data based on input/output formats
func (c *AudioConverter) ConvertAudio(data []byte, inputFormat, outputFormat AudioFormat) ([]byte, error) {
	// If formats match, return as-is
	if inputFormat == outputFormat {
		return data, nil
	}

	// Handle specific conversions
	switch {
	case inputFormat == AudioFormatMulaw && outputFormat == AudioFormatPCM:
		return c.MulawToPCM16kHz(data)

	case inputFormat == AudioFormatPCM && outputFormat == AudioFormatMulaw:
		return c.PCM16kHzToMulaw(data)

	default:
		return nil, fmt.Errorf("unsupported conversion: %s -> %s", inputFormat, outputFormat)
	}
}

// DetectAudioFormat attempts to detect the audio format from raw data
// This is a heuristic and may not be 100% accurate
func DetectAudioFormat(data []byte) (AudioFormat, error) {
	if len(data) < 2 {
		return AudioFormat{}, fmt.Errorf("data too short to detect format")
	}

	// Check for WAV header
	if len(data) >= 12 && string(data[0:4]) == "RIFF" && string(data[8:12]) == "WAVE" {
		return AudioFormatWAV, nil
	}

	// For raw audio, we can't reliably detect between mulaw and PCM
	// without additional context. Return unknown format.
	return AudioFormat{}, fmt.Errorf("unable to detect audio format (no header found)")
}

// ApplyGain applies a gain factor to PCM audio data
// Useful for normalizing audio levels
func ApplyGain(pcmData []byte, gain float64) ([]byte, error) {
	if len(pcmData)%2 != 0 {
		return nil, fmt.Errorf("PCM data length must be even (16-bit samples)")
	}

	result := make([]byte, len(pcmData))
	numSamples := len(pcmData) / 2

	for i := 0; i < numSamples; i++ {
		// Read sample
		sample := int16(binary.LittleEndian.Uint16(pcmData[i*2 : (i+1)*2]))

		// Apply gain
		amplified := float64(sample) * gain

		// Clamp to 16-bit range
		if amplified > math.MaxInt16 {
			amplified = math.MaxInt16
		} else if amplified < math.MinInt16 {
			amplified = math.MinInt16
		}

		// Write result
		binary.LittleEndian.PutUint16(result[i*2:(i+1)*2], uint16(int16(amplified)))
	}

	return result, nil
}

// MixAudio mixes multiple PCM audio streams together
// All inputs must have the same length, sample rate, and format
func MixAudio(streams ...[]byte) ([]byte, error) {
	if len(streams) == 0 {
		return nil, fmt.Errorf("no audio streams provided")
	}

	// Check all streams have same length
	length := len(streams[0])
	for _, stream := range streams {
		if len(stream) != length {
			return nil, fmt.Errorf("all audio streams must have the same length")
		}
		if length%2 != 0 {
			return nil, fmt.Errorf("PCM data length must be even (16-bit samples)")
		}
	}

	// Mix streams
	result := make([]byte, length)
	numSamples := length / 2

	for i := 0; i < numSamples; i++ {
		sum := int32(0)

		// Sum samples from all streams
		for _, stream := range streams {
			sample := int16(binary.LittleEndian.Uint16(stream[i*2 : (i+1)*2]))
			sum += int32(sample)
		}

		// Average the samples
		average := sum / int32(len(streams))

		// Clamp to 16-bit range
		if average > math.MaxInt16 {
			average = math.MaxInt16
		} else if average < math.MinInt16 {
			average = math.MinInt16
		}

		// Write result
		binary.LittleEndian.PutUint16(result[i*2:(i+1)*2], uint16(int16(average)))
	}

	return result, nil
}

// SplitAudioBuffer splits a large audio buffer into smaller chunks
// Useful for streaming audio in fixed-size chunks
func SplitAudioBuffer(data []byte, chunkSize int) [][]byte {
	if chunkSize <= 0 {
		chunkSize = 320 // Default: 20ms at 8kHz for 16-bit samples
	}

	var chunks [][]byte
	for i := 0; i < len(data); i += chunkSize {
		end := i + chunkSize
		if end > len(data) {
			end = len(data)
		}
		chunks = append(chunks, data[i:end])
	}

	return chunks
}

// ConcatAudioBuffers concatenates multiple audio buffers
func ConcatAudioBuffers(buffers [][]byte) []byte {
	var buffer bytes.Buffer
	for _, buf := range buffers {
		buffer.Write(buf)
	}
	return buffer.Bytes()
}
