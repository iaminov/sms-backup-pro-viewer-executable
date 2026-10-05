package internal

import (
	"archive/zip"
	"bytes"
	"database/sql"
	"encoding/base64"
	"encoding/xml"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

type SMSBackup struct {
	XMLName  xml.Name    `xml:"smses"`
	Count    int         `xml:"count,attr"`
	Messages []SMSEntry  `xml:"sms"`
	MMS      []MMSEntry  `xml:"mms"`
	Calls    []CallEntry `xml:"call"`
}

type SMSEntry struct {
	Account       string `xml:"account,attr"`
	Address       string `xml:"address,attr"`
	Date          string `xml:"date,attr"`
	Type          string `xml:"type,attr"`
	Body          string `xml:"body,attr"`
	Read          string `xml:"read,attr"`
	ThreadID      string `xml:"thread_id,attr"`
	Subject       string `xml:"subject,attr"`
	Protocol      string `xml:"protocol,attr"`
	TOA           string `xml:"toa,attr"`
	SCTOA         string `xml:"sc_toa,attr"`
	ServiceCenter string `xml:"service_center,attr"`
	Status        string `xml:"status,attr"`
	SubID         string `xml:"sub_id,attr"`
	ReadableDate  string `xml:"readable_date,attr"`
	ContactName   string `xml:"contact_name,attr"`
}

type MMSEntry struct {
	Account      string    `xml:"account,attr"`
	Address      string    `xml:"address,attr"`
	Date         string    `xml:"date,attr"`
	Type         string    `xml:"msg_box,attr"`
	Read         string    `xml:"read,attr"`
	ThreadID     string    `xml:"thread_id,attr"`
	Subject      string    `xml:"sub,attr"`
	TrID         string    `xml:"tr_id,attr"`
	ContentType  string    `xml:"ct_t,attr"`
	ReadReport   string    `xml:"rr,attr"`
	ReadStatus   string    `xml:"read_status,attr"`
	MessageID    string    `xml:"m_id,attr"`
	MessageSize  string    `xml:"m_size,attr"`
	MessageType  string    `xml:"m_type,attr"`
	SimSlot      string    `xml:"sim_slot,attr"`
	ReadableDate string    `xml:"readable_date,attr"`
	ContactName  string    `xml:"contact_name,attr"`
	Parts        []MMSPart `xml:"parts>part"`
	Addrs        []MMSAddr `xml:"addrs>addr"`
	Body         string    `xml:"body,attr"`
}

type MMSPart struct {
	Seq         string `xml:"seq,attr"`
	ContentType string `xml:"ct,attr"`
	Name        string `xml:"name,attr"`
	Charset     string `xml:"chset,attr"`
	CL          string `xml:"cl,attr"`
	Text        string `xml:"text,attr"`
	Data        string `xml:"data,attr"`
}

type MMSAddr struct {
	Address string `xml:"address,attr"`
	Type    string `xml:"type,attr"`
	Charset string `xml:"charset,attr"`
}

type CallEntry struct {
	Account        string `xml:"account,attr"`
	Number         string `xml:"number,attr"`
	Duration       string `xml:"duration,attr"`
	Date           string `xml:"date,attr"`
	Type           string `xml:"type,attr"`
	Presentation   string `xml:"presentation,attr"`
	SubscriptionID string `xml:"subscription_id,attr"`
	ReadableDate   string `xml:"readable_date,attr"`
	ContactName    string `xml:"contact_name,attr"`
}

type ParseResult struct {
	Messages []Message
	Calls    []CallLog
}

func ParseSMSBackup(r io.Reader, loadMediaOpt ...bool) (ParseResult, error) {
	loadMedia := true
	if len(loadMediaOpt) > 0 {
		loadMedia = loadMediaOpt[0]
	}
	var backup SMSBackup
	decoder := xml.NewDecoder(r)
	err := decoder.Decode(&backup)
	if err != nil {
		return ParseResult{}, err
	}

	var result ParseResult

	// Parse SMS messages
	for _, sms := range backup.Messages {
		msg, err := convertSMSEntry(sms)
		if err != nil {
			slog.Error("Error parsing SMS", "error", err)
			continue
		}
		result.Messages = append(result.Messages, msg)
	}

	// Parse MMS messages
	for _, mms := range backup.MMS {
		msg, err := convertMMSEntry(mms, loadMedia)
		if err != nil {
			slog.Error("Error parsing MMS", "error", err)
			continue
		}
		result.Messages = append(result.Messages, msg)
	}

	// Parse call logs
	for _, call := range backup.Calls {
		callLog, err := convertCallEntry(call)
		if err != nil {
			slog.Error("Error parsing call log", "error", err)
			continue
		}
		result.Calls = append(result.Calls, callLog)
	}

	return result, nil
}

func convertSMSEntry(sms SMSEntry, defaultAccount ...string) (Message, error) {
	dateMs, err := strconv.ParseInt(sms.Date, 10, 64)
	if err != nil {
		return Message{}, err
	}

	msgType, _ := strconv.Atoi(sms.Type)
	read := sms.Read == "1"
	threadID, _ := strconv.Atoi(sms.ThreadID)
	protocol, _ := strconv.Atoi(sms.Protocol)
	status, _ := strconv.Atoi(sms.Status)
	subID, _ := strconv.Atoi(sms.SubID)

	account := sms.Account
	if account == "" && len(defaultAccount) > 0 {
		account = defaultAccount[0]
	}
	if account != "" {
		account = normalizePhoneNumber(account)
	}

	// Normalize the phone number to remove formatting differences
	normalizedAddress := normalizePhoneNumber(sms.Address)

	// For SMS, the address is the single phone number
	addresses := []string{}
	if normalizedAddress != "" {
		addresses = append(addresses, normalizedAddress)
	}

	// For received SMS messages, the sender is the address
	var sender string
	if msgType == 1 && normalizedAddress != "" {
		sender = normalizedAddress
	}

	return Message{
		Address:       normalizedAddress,
		Body:          sms.Body,
		Type:          msgType,
		Date:          time.Unix(dateMs/1000, 0),
		Read:          read,
		ThreadID:      threadID,
		Subject:       normalizeNullString(sms.Subject),
		Protocol:      protocol,
		Status:        status,
		ServiceCenter: sms.ServiceCenter,
		SubID:         subID,
		ContactName:   sms.ContactName,
		Sender:        sender,
		Addresses:     addresses,
		Account:       account,
	}, nil
}

func convertMMSEntry(mms MMSEntry, loadMedia bool, defaultAccount ...string) (Message, error) {
	account := mms.Account
	if account == "" && len(defaultAccount) > 0 {
		account = defaultAccount[0]
	}
	if account != "" {
		account = normalizePhoneNumber(account)
	}
	dateMs, err := strconv.ParseInt(mms.Date, 10, 64)
	if err != nil {
		return Message{}, err
	}

	msgType, _ := strconv.Atoi(mms.Type)
	read := mms.Read == "1"
	threadID, _ := strconv.Atoi(mms.ThreadID)
	readReport, _ := strconv.Atoi(mms.ReadReport)
	readStatus, _ := strconv.Atoi(mms.ReadStatus)
	messageSize, _ := strconv.Atoi(mms.MessageSize)
	messageType, _ := strconv.Atoi(mms.MessageType)
	simSlot, _ := strconv.Atoi(mms.SimSlot)

	// Normalize the phone number to remove formatting differences
	normalizedAddress := normalizePhoneNumber(mms.Address)

	// Extract all addresses from MMS and find the sender (type 137 = FROM)
	// Include ALL addresses to keep group conversations consistent
	addressMap := make(map[string]bool)
	var senderAddress string
	var firstAddress string

	for _, addr := range mms.Addrs {
		if addr.Address != "" {
			// Normalize each address to prevent duplicates due to formatting
			normalizedAddr := normalizePhoneNumber(addr.Address)
			if normalizedAddr != "" {
				addressMap[normalizedAddr] = true

				// Remember the first address we encounter
				if firstAddress == "" {
					firstAddress = normalizedAddr
				}

				// Type 137 (0x89) = FROM (sender in Android MMS)
				// For received messages, this tells us who sent it
				addrType, _ := strconv.Atoi(addr.Type)
				if addrType == 137 {
					senderAddress = normalizedAddr
				}
			}
		}
	}

	// If no type 137 sender was found for a received message, use the first address
	// or the single address for 1-on-1 conversations
	if msgType == 1 && senderAddress == "" {
		if len(addressMap) == 1 && firstAddress != "" {
			// 1-on-1 conversation: the single address is definitely the sender
			senderAddress = firstAddress
		} else if len(addressMap) > 1 && firstAddress != "" {
			// Group conversation without explicit sender: use first address as best guess
			senderAddress = firstAddress
		}
	}

	// Convert map to sorted, deduplicated slice
	addresses := make([]string, 0, len(addressMap))
	for addr := range addressMap {
		addresses = append(addresses, addr)
	}

	// Sort addresses for consistency
	sort.Strings(addresses)

	// Determine the primary address field for conversation grouping
	var primaryAddress string
	if len(addresses) >= 3 {
		// Group MMS (3+ participants) - join all normalized addresses to create a consistent group identifier
		primaryAddress = strings.Join(addresses, ",")
	} else if len(addresses) > 0 {
		// MMS with 1-2 addresses - use the normalized address
		primaryAddress = normalizedAddress
	} else {
		// Fallback to normalized mms.Address if no addresses found in mms.Addrs
		primaryAddress = normalizedAddress
	}

	// For received messages, store the sender in the Sender field
	// This allows us to display who sent each message in the UI
	var sender string
	if msgType == 1 && senderAddress != "" {
		// Received message - store the sender address
		sender = senderAddress
	}

	msg := Message{
		Address:     primaryAddress,
		Type:        msgType,
		Date:        time.Unix(dateMs/1000, 0),
		Read:        read,
		ThreadID:    threadID,
		Subject:     normalizeNullString(mms.Subject),
		ContentType: mms.ContentType,
		ReadReport:  readReport,
		ReadStatus:  readStatus,
		MessageID:   mms.MessageID,
		MessageSize: messageSize,
		MessageType: messageType,
		SimSlot:     simSlot,
		ContactName: mms.ContactName,
		Sender:      sender,
		Addresses:   addresses,
		Account:     account,
	}

	// Extract body text and media from parts
	var bodyText string
	for _, part := range mms.Parts {
		// Skip SMIL content - it's presentation metadata, not actual message content
		if isSMILContentType(part.ContentType) {
			continue
		}

		// Check for VCF (vCard) files - these are text/* but should be treated as media attachments
		if isVCardContentType(part.ContentType) && part.Data != "" {
			if loadMedia && msg.MediaType == "" { // Only store first media item
				data, err := base64.StdEncoding.DecodeString(part.Data)
				if err == nil {
					msg.MediaType = part.ContentType
					msg.MediaData = data
				}
			}
			continue
		}

		// Check for media - media parts often have text="null" which should be ignored
		if part.ContentType != "" && part.Data != "" && !isTextContentType(part.ContentType) {
			// This is media content (image, video, audio, etc.)
			if loadMedia && msg.MediaType == "" { // Only store first media item
				data, err := base64.StdEncoding.DecodeString(part.Data)
				if err == nil {
					// Store all media as-is (including HEIC images in original format)
					msg.MediaType = part.ContentType
					msg.MediaData = data
				}
			}
		} else if part.Text != "" && normalizeNullString(part.Text) != "" {
			// This is actual text content (not "null")
			bodyText += part.Text + " "
		}
	}

	if bodyText != "" {
		msg.Body = strings.TrimSpace(bodyText)
	}

	// Extract group name from RCS proto: tr_id if available
	// Use it as the subject if the current subject is empty or starts with "proto:"
	if mms.TrID != "" && strings.HasPrefix(mms.TrID, "proto:") {
		groupName := extractGroupNameFromTrID(mms.TrID)
		if groupName != "" {
			// Only use the extracted name if subject is empty or also starts with "proto:"
			if msg.Subject == "" || strings.HasPrefix(mms.Subject, "proto:") {
				msg.Subject = groupName
			}
		}
	}

	return msg, nil
}

// normalizeNullString converts the string "null" to an empty string
func normalizeNullString(s string) string {
	if strings.TrimSpace(strings.ToLower(s)) == "null" {
		return ""
	}
	return s
}

// isTextContentType checks if a content type is text-based
func isTextContentType(contentType string) bool {
	ct := strings.ToLower(strings.TrimSpace(contentType))
	return strings.HasPrefix(ct, "text/") ||
		ct == "application/xml" ||
		ct == "application/json"
}

// isSMILContentType checks if a content type is SMIL markup
func isSMILContentType(contentType string) bool {
	ct := strings.ToLower(strings.TrimSpace(contentType))
	return ct == "application/smil" ||
		strings.HasPrefix(ct, "application/smil+") ||
		strings.Contains(ct, "smil")
}

// isSMILMarkup checks if the body text is SMIL (Synchronized Multimedia Integration Language) markup
// which is MMS presentation metadata and should not be displayed to users
func isSMILMarkup(body string) bool {
	trimmed := strings.TrimSpace(body)
	return strings.HasPrefix(trimmed, "<smil") || strings.HasPrefix(trimmed, "<?xml")
}

// isVCardContentType checks if a content type is vCard format
func isVCardContentType(contentType string) bool {
	ct := strings.ToLower(strings.TrimSpace(contentType))
	return ct == "text/vcard" || ct == "text/x-vcard" || ct == "text/directory"
}

// extractGroupNameFromTrID extracts the group conversation name from RCS proto: tr_id field
func extractGroupNameFromTrID(trID string) string {
	return ""
	/*
		// Check if tr_id starts with "proto:"
		if !strings.HasPrefix(trID, "proto:") {
			return ""
		}

		// Remove the "proto:" prefix
		protoData := strings.TrimPrefix(trID, "proto:")

		// Base64 decode the remaining bytes
		decoded, err := base64.StdEncoding.DecodeString(protoData)
		if err != nil {
			slog.Error("Failed to base64 decode tr_id", "error", err)
			return ""
		}

		// Check if we have enough bytes (need at least 84 bytes: offset 83 + 1 for length)
		if len(decoded) < 84 {
			slog.Debug("Decoded tr_id too short", "bytes", len(decoded), "required", 84)
			return ""
		}

		// Read the length byte at offset 83
		nameLength := int(decoded[83])

		// Check if we have enough bytes for the name
		if len(decoded) < 84+nameLength {
			slog.Debug("Not enough bytes for group name", "have", len(decoded), "need", 84+nameLength)
			return ""
		}

		// Extract the group name string
		groupName := string(decoded[84 : 84+nameLength])

		slog.Debug("Extracted group name from tr_id", "group_name", groupName)
		return groupName
	*/
}

// isHEICContentType checks if a content type is HEIC/HEIF format
func isHEICContentType(contentType string) bool {
	ct := strings.ToLower(strings.TrimSpace(contentType))
	return strings.Contains(ct, "heic") || strings.Contains(ct, "heif")
}

// needsVideoConversion checks if a video format needs conversion for browser compatibility
func needsVideoConversion(contentType string) bool {
	ct := strings.ToLower(strings.TrimSpace(contentType))
	unsupportedFormats := []string{
		"3gpp", "3gp", "3g2", "3gpp2",
		"video/3gpp", "video/3gp", "video/3gpp2", "video/3g2",
		"video/x-matroska", // MKV container (may have various codecs)
	}

	for _, format := range unsupportedFormats {
		if strings.Contains(ct, format) {
			return true
		}
	}
	return false
}

// convertHEICtoJPEG is implemented in heic_enabled.go (with -tags heic) or heic_disabled.go (default)
// When HEIC support is enabled, it converts HEIC image data to JPEG format
// When HEIC support is disabled, it returns a placeholder image

// convertVideoToMP4 converts unsupported video formats (like 3GP) to MP4 using ffmpeg
// Returns the converted MP4 data or an error if conversion fails
func convertVideoToMP4(videoData []byte) ([]byte, error) {
	// Create temporary files for input and output
	tmpInputFile, err := os.CreateTemp("", "video-input-*.3gp")
	if err != nil {
		return nil, fmt.Errorf("failed to create temp input file: %w", err)
	}
	defer os.Remove(tmpInputFile.Name())
	defer tmpInputFile.Close()

	tmpOutputFile, err := os.CreateTemp("", "video-output-*.mp4")
	if err != nil {
		return nil, fmt.Errorf("failed to create temp output file: %w", err)
	}
	defer os.Remove(tmpOutputFile.Name())
	tmpOutputFile.Close()

	// Write input video data to temp file
	_, err = tmpInputFile.Write(videoData)
	if err != nil {
		return nil, fmt.Errorf("failed to write input video: %w", err)
	}
	tmpInputFile.Close()

	// Run ffmpeg to convert video to MP4 with H.264 codec
	// -i: input file
	// -c:v libx264: use H.264 video codec
	// -c:a aac: use AAC audio codec
	// -movflags +faststart: optimize for streaming
	// -preset fast: balance between speed and quality
	// -crf 23: constant rate factor (quality, lower is better, 23 is good default)
	cmd := exec.Command("ffmpeg",
		"-i", tmpInputFile.Name(),
		"-c:v", "libx264",
		"-c:a", "aac",
		"-movflags", "+faststart",
		"-preset", "fast",
		"-crf", "23",
		"-y", // overwrite output file
		tmpOutputFile.Name(),
	)

	// Capture stderr for error messages
	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	err = cmd.Run()
	if err != nil {
		return nil, fmt.Errorf("ffmpeg conversion failed: %w, stderr: %s", err, stderr.String())
	}

	// Read converted video data
	convertedData, err := os.ReadFile(tmpOutputFile.Name())
	if err != nil {
		return nil, fmt.Errorf("failed to read converted video: %w", err)
	}

	return convertedData, nil
}

// needsAudioConversion checks if an audio format needs conversion for browser compatibility
func needsAudioConversion(contentType string) bool {
	ct := strings.ToLower(strings.TrimSpace(contentType))
	unsupportedFormats := []string{
		"audio/amr", "audio/amr-wb",
		"audio/3gpp", "audio/3gpp2",
	}
	for _, format := range unsupportedFormats {
		if strings.Contains(ct, format) {
			return true
		}
	}
	return false
}

// convertAudioToMP3 converts unsupported audio formats (like AMR) to MP3 using ffmpeg
func convertAudioToMP3(audioData []byte) ([]byte, error) {
	tmpInputFile, err := os.CreateTemp("", "audio-input-*")
	if err != nil {
		return nil, fmt.Errorf("failed to create temp input file: %w", err)
	}
	defer os.Remove(tmpInputFile.Name())
	defer tmpInputFile.Close()

	tmpOutputFile, err := os.CreateTemp("", "audio-output-*.mp3")
	if err != nil {
		return nil, fmt.Errorf("failed to create temp output file: %w", err)
	}
	defer os.Remove(tmpOutputFile.Name())
	tmpOutputFile.Close()

	_, err = tmpInputFile.Write(audioData)
	if err != nil {
		return nil, fmt.Errorf("failed to write input audio: %w", err)
	}
	tmpInputFile.Close()

	cmd := exec.Command("ffmpeg",
		"-i", tmpInputFile.Name(),
		"-codec:a", "libmp3lame",
		"-q:a", "2",
		"-y",
		tmpOutputFile.Name(),
	)

	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	err = cmd.Run()
	if err != nil {
		return nil, fmt.Errorf("ffmpeg audio conversion failed: %w, stderr: %s", err, stderr.String())
	}

	convertedData, err := os.ReadFile(tmpOutputFile.Name())
	if err != nil {
		return nil, fmt.Errorf("failed to read converted audio: %w", err)
	}

	return convertedData, nil
}

func convertCallEntry(call CallEntry, defaultAccount ...string) (CallLog, error) {
	dateMs, err := strconv.ParseInt(call.Date, 10, 64)
	if err != nil {
		return CallLog{}, err
	}

	duration, _ := strconv.Atoi(call.Duration)
	callType, _ := strconv.Atoi(call.Type)
	presentation, _ := strconv.Atoi(call.Presentation)

	account := call.Account
	if account == "" && len(defaultAccount) > 0 {
		account = defaultAccount[0]
	}
	if account != "" {
		account = normalizePhoneNumber(account)
	}

	// Normalize the phone number to remove formatting differences
	normalizedNumber := normalizePhoneNumber(call.Number)

	return CallLog{
		Number:         normalizedNumber,
		Duration:       duration,
		Date:           time.Unix(dateMs/1000, 0),
		Type:           callType,
		Presentation:   presentation,
		SubscriptionID: call.SubscriptionID,
		ContactName:    call.ContactName,
		Account:        account,
	}, nil
}

// UploadProgress tracks the progress of an ongoing upload
type UploadProgress struct {
	TotalMessages     int       `json:"total_messages"`
	ProcessedMessages int       `json:"processed_messages"`
	TotalCalls        int       `json:"total_calls"`
	ProcessedCalls    int       `json:"processed_calls"`
	// BytesReceived/TotalBytes cover the "receiving" phase: the server is
	// still reading the request body and writing it to disk. The browser's
	// XHR upload.onprogress only reflects bytes handed to the OS socket
	// buffer, which can reach 100% well before the server has finished
	// reading and saving a large file -- these fields let the client keep
	// showing real progress across that gap instead of appearing to hang.
	BytesReceived int64     `json:"bytes_received,omitempty"`
	TotalBytes    int64     `json:"total_bytes,omitempty"`
	Status        string    `json:"status"` // "receiving", "parsing", "importing", "completed", "error"
	ErrorMessage  string    `json:"error_message,omitempty"`
	StartTime     time.Time `json:"start_time"`
	mu            sync.RWMutex
}

var (
	uploadProgress     *UploadProgress
	uploadProgressLock sync.RWMutex
)

// GetUploadProgress returns the current upload progress
func GetUploadProgress() *UploadProgress {
	uploadProgressLock.RLock()
	defer uploadProgressLock.RUnlock()

	if uploadProgress == nil {
		return nil
	}

	uploadProgress.mu.RLock()
	defer uploadProgress.mu.RUnlock()

	// Return a copy to avoid race conditions
	return &UploadProgress{
		TotalMessages:     uploadProgress.TotalMessages,
		ProcessedMessages: uploadProgress.ProcessedMessages,
		TotalCalls:        uploadProgress.TotalCalls,
		ProcessedCalls:    uploadProgress.ProcessedCalls,
		BytesReceived:     uploadProgress.BytesReceived,
		TotalBytes:        uploadProgress.TotalBytes,
		Status:            uploadProgress.Status,
		ErrorMessage:      uploadProgress.ErrorMessage,
		StartTime:         uploadProgress.StartTime,
	}
}

// StartReceivingUpload initializes progress tracking for the "receiving"
// phase (server reading/saving the request body), before parsing begins.
func StartReceivingUpload(totalBytes int64) {
	uploadProgressLock.Lock()
	defer uploadProgressLock.Unlock()

	uploadProgress = &UploadProgress{
		TotalBytes: totalBytes,
		Status:     "receiving",
		StartTime:  time.Now(),
	}
}

// UpdateReceivingProgress updates how many bytes of the upload the server
// has read and written to disk so far.
func UpdateReceivingProgress(bytesReceived int64) {
	uploadProgressLock.RLock()
	defer uploadProgressLock.RUnlock()

	if uploadProgress == nil {
		return
	}

	uploadProgress.mu.Lock()
	defer uploadProgress.mu.Unlock()

	uploadProgress.BytesReceived = bytesReceived
}

// SetUploadProgress initializes or updates the upload progress
func SetUploadProgress(total, processed int, status string) {
	uploadProgressLock.Lock()
	defer uploadProgressLock.Unlock()

	if uploadProgress == nil {
		uploadProgress = &UploadProgress{
			StartTime: time.Now(),
		}
	}

	uploadProgress.mu.Lock()
	defer uploadProgress.mu.Unlock()

	uploadProgress.TotalMessages = total
	uploadProgress.ProcessedMessages = processed
	uploadProgress.Status = status
}

// UpdateMessageProgress updates the progress for messages
func UpdateMessageProgress(processed int) {
	uploadProgressLock.RLock()
	defer uploadProgressLock.RUnlock()

	if uploadProgress == nil {
		return
	}

	uploadProgress.mu.Lock()
	defer uploadProgress.mu.Unlock()

	uploadProgress.ProcessedMessages = processed
}

// UpdateCallProgress updates the progress for calls
func UpdateCallProgress(processed int) {
	uploadProgressLock.RLock()
	defer uploadProgressLock.RUnlock()

	if uploadProgress == nil {
		return
	}

	uploadProgress.mu.Lock()
	defer uploadProgress.mu.Unlock()

	uploadProgress.ProcessedCalls = processed
}

// ClearUploadProgress clears the upload progress
func ClearUploadProgress() {
	uploadProgressLock.Lock()
	defer uploadProgressLock.Unlock()
	uploadProgress = nil
}

// SaveUploadedFile saves the uploaded file to a temporary location
func SaveUploadedFile(file io.Reader, filename string) (string, error) {
	// Stage the upload under DB_PATH_PREFIX (the same mounted data volume the
	// databases live on) rather than the OS temp directory. os.TempDir()
	// resolves to /tmp, which is the container's own (often small) root
	// filesystem -- large backups can exhaust it even when the data volume
	// has plenty of room, and it's unrelated storage from the user's
	// perspective. Using the same volume also means the temp file and the
	// destination database are on the same filesystem, avoiding a
	// cross-filesystem copy if this ever needs to be moved rather than
	// streamed.
	dbPathPrefix := os.Getenv("DB_PATH_PREFIX")
	if dbPathPrefix == "" {
		dbPathPrefix = "."
	}
	uploadDir := filepath.Join(dbPathPrefix, "sbv-uploads")
	err := os.MkdirAll(uploadDir, 0755)
	if err != nil {
		return "", fmt.Errorf("failed to create upload directory: %v", err)
	}

	// Create temporary file
	pattern := "backup-*.xml"
	if strings.HasSuffix(strings.ToLower(filename), ".zip") {
		pattern = "backup-*.zip"
	}
	tempFile, err := os.CreateTemp(uploadDir, pattern)
	if err != nil {
		return "", fmt.Errorf("failed to create temp file: %v", err)
	}
	defer tempFile.Close()

	// Copy uploaded file to temp file, reporting bytes received as we go so
	// clients can show real progress through the receive+save phase -- the
	// browser's own upload progress event only reflects bytes sent over the
	// wire, not bytes the server has actually read and written to disk.
	_, err = io.Copy(tempFile, &receiveProgressReader{reader: file})
	if err != nil {
		os.Remove(tempFile.Name())
		return "", fmt.Errorf("failed to save file: %v", err)
	}

	return tempFile.Name(), nil
}

// receiveProgressReader wraps an io.Reader and reports cumulative bytes read
// via UpdateReceivingProgress as the upload is streamed to disk. Updates are
// throttled to avoid taking the progress lock on every small read.
type receiveProgressReader struct {
	reader       io.Reader
	total        int64
	lastReported time.Time
}

func (r *receiveProgressReader) Read(p []byte) (int, error) {
	n, err := r.reader.Read(p)
	if n > 0 {
		r.total += int64(n)
		if time.Since(r.lastReported) >= 250*time.Millisecond {
			UpdateReceivingProgress(r.total)
			r.lastReported = time.Now()
		}
	}
	if err == io.EOF {
		// Always report the final count so the client sees the receive
		// phase reach 100% rather than stalling at the last throttled value.
		UpdateReceivingProgress(r.total)
	}
	return n, err
}

// isZipFile checks whether a file begins with the ZIP magic bytes
func isZipFile(path string) bool {
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()
	var magic [4]byte
	if _, err := io.ReadFull(f, magic[:]); err != nil {
		return false
	}
	return magic == [4]byte{'P', 'K', 0x03, 0x04}
}

// ProcessUploadedFile processes the uploaded file (.xml or .zip) in the background
func ProcessUploadedFile(userID string, username string, filePath string, loadMediaOpt ...bool) {
	loadMedia := true
	if len(loadMediaOpt) > 0 {
		loadMedia = loadMediaOpt[0]
	}

	defer func() {
		// Always clean up the temp file when done
		slog.Info("Removing temporary file", "path", filePath)
		if err := os.Remove(filePath); err != nil {
			slog.Warn("Failed to remove temp file", "path", filePath, "error", err)
		}
	}()

	slog.Info("Starting background processing", "path", filePath, "user", username, "loadMedia", loadMedia)

	// Get user database
	userDB, err := GetUserDB(userID, username)
	if err != nil {
		slog.Error("Error getting user database", "error", err)
		SetUploadProgress(0, 0, "error")
		uploadProgressLock.Lock()
		if uploadProgress != nil {
			uploadProgress.mu.Lock()
			uploadProgress.ErrorMessage = fmt.Sprintf("Failed to get user database: %v", err)
			uploadProgress.mu.Unlock()
		}
		uploadProgressLock.Unlock()
		return
	}

	detectedAccount := DetectAccountForFile(filePath)
	if detectedAccount != "" {
		slog.Info("Detected account for uploaded backup", "account", detectedAccount, "file", filePath)
	}

	// Check if file is a zip archive
	if isZipFile(filePath) || strings.HasSuffix(strings.ToLower(filePath), ".zip") {
		zReader, err := zip.OpenReader(filePath)
		if err != nil {
			slog.Error("Error opening zip archive", "error", err)
			SetUploadProgress(0, 0, "error")
			uploadProgressLock.Lock()
			if uploadProgress != nil {
				uploadProgress.mu.Lock()
				uploadProgress.ErrorMessage = fmt.Sprintf("Failed to open zip archive: %v", err)
				uploadProgress.mu.Unlock()
			}
			uploadProgressLock.Unlock()
			return
		}
		defer zReader.Close()

		foundXML := false
		var totalMessages, totalCalls int
		for _, zFile := range zReader.File {
			if strings.HasSuffix(strings.ToLower(zFile.Name), ".xml") {
				foundXML = true
				rc, err := zFile.Open()
				if err != nil {
					slog.Error("Error opening XML from zip", "file", zFile.Name, "error", err)
					continue
				}
				mCount, cCount, err := ParseSMSBackupStreaming(userDB, rc, defaultImportBatchSize, loadMedia, detectedAccount)
				rc.Close()
				if err != nil {
					slog.Error("Error processing file inside zip", "file", zFile.Name, "error", err)
					SetUploadProgress(0, 0, "error")
					uploadProgressLock.Lock()
					if uploadProgress != nil {
						uploadProgress.mu.Lock()
						uploadProgress.ErrorMessage = fmt.Sprintf("Failed to process %s in zip: %v", zFile.Name, err)
						uploadProgress.mu.Unlock()
					}
					uploadProgressLock.Unlock()
					return
				}
				totalMessages += mCount
				totalCalls += cCount
			}
		}

		if !foundXML {
			slog.Error("No XML file found inside ZIP archive", "path", filePath)
			SetUploadProgress(0, 0, "error")
			uploadProgressLock.Lock()
			if uploadProgress != nil {
				uploadProgress.mu.Lock()
				uploadProgress.ErrorMessage = "No .xml file found inside ZIP archive"
				uploadProgress.mu.Unlock()
			}
			uploadProgressLock.Unlock()
			return
		}

		slog.Info("Completed processing zip archive", "messages", totalMessages, "calls", totalCalls)
		return
	}

	// Open the file for reading
	file, err := os.Open(filePath)
	if err != nil {
		slog.Error("Error opening file", "error", err)
		SetUploadProgress(0, 0, "error")
		uploadProgressLock.Lock()
		if uploadProgress != nil {
			uploadProgress.mu.Lock()
			uploadProgress.ErrorMessage = fmt.Sprintf("Failed to open file: %v", err)
			uploadProgress.mu.Unlock()
		}
		uploadProgressLock.Unlock()
		return
	}
	defer file.Close()

	// Process with streaming parser. batchSize only controls how many rows
	// share one commit -- rows are still inserted and their data freed one at
	// a time as decoded, so this doesn't affect peak memory usage.
	messageCount, callCount, err := ParseSMSBackupStreaming(userDB, file, defaultImportBatchSize, loadMedia, detectedAccount)
	if err != nil {
		slog.Error("Error processing file", "error", err)
		SetUploadProgress(0, 0, "error")
		uploadProgressLock.Lock()
		if uploadProgress != nil {
			uploadProgress.mu.Lock()
			uploadProgress.ErrorMessage = fmt.Sprintf("Failed to process file: %v", err)
			uploadProgress.mu.Unlock()
		}
		uploadProgressLock.Unlock()
		return
	}

	slog.Info("Completed processing", "messages", messageCount, "calls", callCount)
}

// ParseSMSBackupStreaming parses SMS backup file with streaming to reduce memory usage
// Each message is inserted immediately and memory is freed aggressively
// defaultImportBatchSize is used when callers pass batchSize <= 0.
const defaultImportBatchSize = 200

// ParseSMSBackupStreaming parses an SMS Backup & Restore XML file and inserts
// rows as it goes, committing every batchSize rows instead of autocommitting
// each one individually. Each commit does a real fsync-equivalent flush of
// the journal/database file; on network-backed storage (NFS) that flush is a
// full round trip, so committing every single row makes network latency the
// dominant cost of import -- observed as low tens of messages/sec regardless
// of how fast decoding itself is. Batching cuts the number of commits by
// ~batchSize. Keep this modest rather than huge: a mid-import failure only
// loses the rows in the currently-open (uncommitted) batch -- they're safely
// re-importable on retry either way, since INSERT ... ON CONFLICT DO NOTHING
// makes re-running the same file idempotent, but a smaller batch bounds how
// much re-decoding work a failure near the end of a large import wastes.
func ParseSMSBackupStreaming(userDB *sql.DB, r io.Reader, batchSize int, opts ...interface{}) (int, int, error) {
	loadMedia := true
	defaultAccount := ""
	for _, opt := range opts {
		switch v := opt.(type) {
		case bool:
			loadMedia = v
		case string:
			defaultAccount = v
		}
	}
	if batchSize <= 0 {
		batchSize = defaultImportBatchSize
	}
	// Serialize writers against this user's database when not in WAL mode (no-op
	// in WAL mode). Held for the whole import so concurrent imports for the same
	// user queue up instead of racing SQLite's single-writer rollback journal.
	unlock := LockForWrite(userDB)
	defer unlock()

	// Initialize progress tracking
	uploadProgressLock.Lock()
	uploadProgress = &UploadProgress{
		Status:    "parsing",
		StartTime: time.Now(),
	}
	uploadProgressLock.Unlock()

	decoder := xml.NewDecoder(r)

	var messageCount, callCount int

	// Track total count from root element if available
	var totalCount int

	// Batches inserts into transactions of importBatchSize rows instead of
	// autocommitting each one individually. tx is nil when no transaction is
	// currently open (lazily started on the first row after each commit).
	var tx *sql.Tx
	var rowsInBatch int

	// currentExecer returns the target for the next insert: the open
	// transaction if one exists, opening a new one lazily on first use.
	currentExecer := func() (dbExecer, error) {
		if tx == nil {
			var err error
			tx, err = userDB.Begin()
			if err != nil {
				return nil, err
			}
			rowsInBatch = 0
		}
		return tx, nil
	}

	// commitIfBatchFull commits and clears the open transaction once
	// batchSize rows have been added to it.
	commitIfBatchFull := func() error {
		rowsInBatch++
		if rowsInBatch >= batchSize {
			err := tx.Commit()
			tx = nil
			rowsInBatch = 0
			return err
		}
		return nil
	}

	// commitPending flushes any partially-filled batch. Called on normal
	// completion and on error, so nothing successfully inserted is left
	// uncommitted.
	commitPending := func() error {
		if tx == nil {
			return nil
		}
		err := tx.Commit()
		tx = nil
		rowsInBatch = 0
		return err
	}
	// Roll back cleanly if we return early due to a decode error; a no-op
	// once commitPending has already committed and cleared tx.
	defer func() {
		if tx != nil {
			tx.Rollback()
		}
	}()

	for {
		token, err := decoder.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			SetUploadProgress(0, 0, "error")
			return messageCount, callCount, err
		}

		switch elem := token.(type) {
		case xml.StartElement:
			// Get total count from root element
			if elem.Name.Local == "smses" {
				for _, attr := range elem.Attr {
					if attr.Name.Local == "count" {
						totalCount, _ = strconv.Atoi(attr.Value)
						uploadProgressLock.Lock()
						uploadProgress.mu.Lock()
						uploadProgress.TotalMessages = totalCount
						uploadProgress.mu.Unlock()
						uploadProgressLock.Unlock()
					}
				}
			}

			// Process SMS messages
			if elem.Name.Local == "sms" {
				var sms SMSEntry
				err := decoder.DecodeElement(&sms, &elem)
				if err != nil {
					slog.Error("Error decoding SMS", "error", err)
					continue
				}

				msg, err := convertSMSEntry(sms, defaultAccount)
				if err != nil {
					slog.Error("Error converting SMS", "error", err)
					continue
				}

				execer, err := currentExecer()
				if err != nil {
					SetUploadProgress(0, 0, "error")
					return messageCount, callCount, fmt.Errorf("failed to begin batch: %w", err)
				}
				err = InsertMessage(execer, &msg)
				if err != nil {
					slog.Error("Error inserting message", "error", err)
				} else {
					messageCount++
					UpdateMessageProgress(messageCount)
				}
				if err := commitIfBatchFull(); err != nil {
					SetUploadProgress(0, 0, "error")
					return messageCount, callCount, fmt.Errorf("failed to commit batch: %w", err)
				}

				// Force garbage collection every 1000 messages to keep memory low
				if messageCount%1000 == 0 {
					runtime.GC()
				}
			}

			// Process MMS messages
			if elem.Name.Local == "mms" {
				var mms MMSEntry
				err := decoder.DecodeElement(&mms, &elem)
				if err != nil {
					slog.Error("Error decoding MMS", "error", err)
					continue
				}

				msg, err := convertMMSEntry(mms, loadMedia)

				// Clear the MMS struct immediately after conversion to free base64 strings
				mms.Parts = nil
				mms = MMSEntry{}

				if err != nil {
					slog.Error("Error converting MMS", "error", err)
					continue
				}

				execer, err := currentExecer()
				if err != nil {
					SetUploadProgress(0, 0, "error")
					return messageCount, callCount, fmt.Errorf("failed to begin batch: %w", err)
				}
				err = InsertMessage(execer, &msg)
				if err != nil {
					slog.Error("Error inserting message", "error", err)
				} else {
					messageCount++
					UpdateMessageProgress(messageCount)
				}
				if err := commitIfBatchFull(); err != nil {
					SetUploadProgress(0, 0, "error")
					return messageCount, callCount, fmt.Errorf("failed to commit batch: %w", err)
				}

				// Clear the message data immediately after insert
				msg.MediaData = nil
				msg = Message{}

				// Force garbage collection every 100 MMS messages (they're larger)
				if messageCount%100 == 0 {
					runtime.GC()
				}
			}

			// Process call logs
			if elem.Name.Local == "call" {
				var call CallEntry
				err := decoder.DecodeElement(&call, &elem)
				if err != nil {
					slog.Error("Error decoding call", "error", err)
					continue
				}

				callLog, err := convertCallEntry(call)
				if err != nil {
					slog.Error("Error converting call", "error", err)
					continue
				}

				execer, err := currentExecer()
				if err != nil {
					SetUploadProgress(0, 0, "error")
					return messageCount, callCount, fmt.Errorf("failed to begin batch: %w", err)
				}
				err = InsertCallLog(execer, &callLog)
				if err != nil {
					slog.Error("Error inserting call log", "error", err)
				} else {
					callCount++
					uploadProgressLock.Lock()
					uploadProgress.mu.Lock()
					uploadProgress.TotalCalls++
					uploadProgress.ProcessedCalls = callCount
					uploadProgress.mu.Unlock()
					uploadProgressLock.Unlock()
				}
				if err := commitIfBatchFull(); err != nil {
					SetUploadProgress(0, 0, "error")
					return messageCount, callCount, fmt.Errorf("failed to commit batch: %w", err)
				}
			}
		}
	}

	// Flush any partially-filled final batch.
	if err := commitPending(); err != nil {
		SetUploadProgress(0, 0, "error")
		return messageCount, callCount, fmt.Errorf("failed to commit final batch: %w", err)
	}

	// Final garbage collection
	runtime.GC()

	// Mark as completed
	SetUploadProgress(messageCount, messageCount, "completed")

	return messageCount, callCount, nil
}

// DetectAccountForFile inspects a backup file (XML, ZIP, or Signal) and returns the detected primary phone account
func DetectAccountForFile(filePath string, signalPassphrase ...string) string {
	lower := strings.ToLower(filePath)
	if IsSignalBackup(filePath) {
		pp := ""
		if len(signalPassphrase) > 0 {
			pp = signalPassphrase[0]
		}
		if pp != "" {
			selfPhone, _, err := ExtractSignalSelfPhone(filePath, pp)
			if err == nil && selfPhone != "" {
				return normalizePhoneNumber(selfPhone)
			}
		}
		return ""
	}

	counts := make(map[string]int)
	sources := make(map[string]string)
	if strings.HasSuffix(lower, ".zip") || isZipFile(filePath) {
		zr, err := zip.OpenReader(filePath)
		if err == nil {
			defer zr.Close()
			for _, zf := range zr.File {
				if strings.HasSuffix(strings.ToLower(zf.Name), ".xml") {
					rc, err := zf.Open()
					if err == nil {
						scanBackupStreamForUserNumbers(rc, counts, sources)
						rc.Close()
						break
					}
				}
			}
		}
	} else {
		f, err := os.Open(filePath)
		if err == nil {
			defer f.Close()
			scanBackupStreamForUserNumbers(f, counts, sources)
		}
	}

	bestPhone := ""
	bestCount := 0
	for phone, count := range counts {
		if count > bestCount {
			bestCount = count
			bestPhone = phone
		}
	}
	return bestPhone
}
