package internal

import (
	"archive/zip"
	"encoding/base64"
	"fmt"
	"html"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

var (
	gvDtRe          = regexp.MustCompile(`<abbr class="dt" title="([^"]+)">`)
	gvPublishedRe   = regexp.MustCompile(`<abbr class="published" title="([^"]+)">`)
	gvDurationRe    = regexp.MustCompile(`<abbr class="duration" title="([^"]*)">([^<]*)</abbr>`)
	gvTelHrefRe     = regexp.MustCompile(`href="tel:([^"]*)"`)
	gvFnRe          = regexp.MustCompile(`class="fn"[^>]*>([^<]*)`)
	gvQRe           = regexp.MustCompile(`(?s)<q>(.*?)</q>`)
	gvImgSrcRe      = regexp.MustCompile(`<img [^>]*src="([^"]+)"`)
	gvAudioSrcRe    = regexp.MustCompile(`<audio [^>]*src="([^"]+)"`)
	gvVideoSrcRe    = regexp.MustCompile(`<video [^>]*src="([^"]+)"`)
	gvAMediaRe      = regexp.MustCompile(`<a [^>]*class="(video|audio|vcard|image)"[^>]*href="([^"]+)"`)
	gvAEnclosureRe  = regexp.MustCompile(`<a [^>]*rel="enclosure"[^>]*href="([^"]+)"`)
	gvTranscriptRe  = regexp.MustCompile(`(?s)<span class="full-text">(.*?)</span>`)
	gvTagRe         = regexp.MustCompile(`href="[^"]*#([^"]+)"`)
	gvISODurationRe = regexp.MustCompile(`^PT(?:(\d+)H)?(?:(\d+)M)?(?:(\d+)S)?$`)
	gvTitleRe       = regexp.MustCompile(`(?s)<title>(.*?)</title>`)
	gvBrRe          = regexp.MustCompile(`(?i)<br\s*/?>`)
	gvTagsDivRe     = regexp.MustCompile(`(?s)<div class="tags">(.*?)</div>`)

	gvAccountCacheLock sync.RWMutex
	gvAccountCache     = make(map[string]string)
)

// IsGoogleVoiceHTMLFile checks if a file is an individual Google Voice HTML conversation/call file
func IsGoogleVoiceHTMLFile(filePath string) bool {
	lower := strings.ToLower(filePath)
	if !strings.HasSuffix(lower, ".html") || strings.HasSuffix(lower, "bills.html") {
		return false
	}
	base := filepath.Base(filePath)
	// Check common Google Voice Takeout filename naming patterns
	if strings.Contains(base, " - Text - ") ||
		strings.Contains(base, " - Missed - ") ||
		strings.Contains(base, " - Placed - ") ||
		strings.Contains(base, " - Received - ") ||
		strings.Contains(base, " - Voicemail - ") ||
		strings.HasPrefix(base, "Group Conversation") ||
		strings.HasPrefix(base, "- Missed -") ||
		strings.HasPrefix(base, "- Received -") {
		return true
	}

	// Sniff file header for Google Voice markers
	f, err := os.Open(filePath)
	if err != nil {
		return false
	}
	defer f.Close()

	buf := make([]byte, 1024)
	n, _ := f.Read(buf)
	content := string(buf[:n])
	return strings.Contains(content, "google.com/voice") ||
		strings.Contains(content, "hChatLog") ||
		strings.Contains(content, "haudio")
}

// IsGoogleVoiceZip checks if a zip archive contains Google Voice Takeout exports
func IsGoogleVoiceZip(zipPath string) bool {
	zr, err := zip.OpenReader(zipPath)
	if err != nil {
		return false
	}
	defer zr.Close()

	for _, f := range zr.File {
		name := strings.ToLower(f.Name)
		if strings.HasSuffix(name, "phones.vcf") ||
			strings.Contains(name, "voice/calls/") ||
			(strings.HasSuffix(name, ".html") && (strings.Contains(name, " - text - ") || strings.Contains(name, "group conversation"))) {
			return true
		}
	}
	return false
}

// ParseGoogleVoicePhonesVCF extracts Google Voice and linked cell phone numbers from a Phones.vcf file
func ParseGoogleVoicePhonesVCF(vcfContent string) (gvNumber string, cellNumbers []string) {
	lines := strings.Split(vcfContent, "\n")
	itemLabels := make(map[string]string)
	itemTels := make(map[string]string)
	var directTels []string

	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if idx := strings.Index(line, ":"); idx != -1 {
			key := strings.ToUpper(line[:idx])
			val := strings.TrimSpace(line[idx+1:])

			if strings.HasPrefix(key, "ITEM") {
				parts := strings.SplitN(key, ".", 2)
				if len(parts) == 2 {
					prefix := parts[0]
					prop := parts[1]
					if strings.HasPrefix(prop, "X-ABLABEL") {
						itemLabels[prefix] = val
					} else if strings.HasPrefix(prop, "TEL") {
						itemTels[prefix] = val
					}
				}
			} else if strings.HasPrefix(key, "TEL") {
				directTels = append(directTels, val)
			}
		}
	}

	// 1. Identify Google Voice number from items with Google Voice label
	for prefix, label := range itemLabels {
		if strings.Contains(strings.ToLower(label), "google voice") || strings.Contains(strings.ToLower(label), "voice") {
			if tel, ok := itemTels[prefix]; ok {
				gvNumber = normalizePhoneNumber(tel)
			}
		} else {
			if tel, ok := itemTels[prefix]; ok {
				norm := normalizePhoneNumber(tel)
				if norm != "" {
					cellNumbers = append(cellNumbers, norm)
				}
			}
		}
	}

	// 2. Collect any remaining itemTels
	for _, tel := range itemTels {
		norm := normalizePhoneNumber(tel)
		if norm != "" && norm != gvNumber {
			cellNumbers = append(cellNumbers, norm)
		}
	}

	// 3. Collect direct tels
	for _, tel := range directTels {
		norm := normalizePhoneNumber(tel)
		if norm != "" {
			if gvNumber == "" {
				gvNumber = norm
			} else if norm != gvNumber {
				cellNumbers = append(cellNumbers, norm)
			}
		}
	}

	return gvNumber, cellNumbers
}

// FindPhonesVCFInDir looks for Phones.vcf in the directory or parent directories
func FindPhonesVCFInDir(startDir string) string {
	curr := startDir
	for i := 0; i < 4; i++ {
		candidate := filepath.Join(curr, "Phones.vcf")
		if _, err := os.Stat(candidate); err == nil {
			return candidate
		}
		candidateVoice := filepath.Join(curr, "Voice", "Phones.vcf")
		if _, err := os.Stat(candidateVoice); err == nil {
			return candidateVoice
		}
		parent := filepath.Dir(curr)
		if parent == curr || parent == "" {
			break
		}
		curr = parent
	}
	return ""
}

// ExtractGoogleVoiceNumber detects the account number associated with a Google Voice folder or zip
func ExtractGoogleVoiceNumber(dirOrZip string) (string, []string, error) {
	fi, err := os.Stat(dirOrZip)
	if err != nil {
		return "", nil, err
	}

	if fi.IsDir() {
		vcfPath := FindPhonesVCFInDir(dirOrZip)
		if vcfPath != "" {
			b, err := os.ReadFile(vcfPath)
			if err == nil {
				gv, cells := ParseGoogleVoicePhonesVCF(string(b))
				if gv != "" {
					return gv, cells, nil
				}
			}
		}
		// If Phones.vcf not found, scan first 10 HTML files for sender Me
		var candidateGV string
		_ = filepath.Walk(dirOrZip, func(path string, info os.FileInfo, err error) error {
			if err != nil || candidateGV != "" {
				return nil
			}
			if !info.IsDir() && strings.HasSuffix(strings.ToLower(path), ".html") && !strings.HasSuffix(strings.ToLower(path), "bills.html") {
				tel := sniffMeTelFromHTML(path)
				if tel != "" {
					candidateGV = tel
					return io.EOF
				}
			}
			return nil
		})
		if candidateGV != "" {
			return candidateGV, nil, nil
		}
		return "", nil, fmt.Errorf("no Google Voice phone number found in directory: %s", dirOrZip)
	}

	// Zip archive
	zr, err := zip.OpenReader(dirOrZip)
	if err != nil {
		return "", nil, err
	}
	defer zr.Close()

	for _, f := range zr.File {
		if strings.HasSuffix(strings.ToLower(f.Name), "phones.vcf") {
			rc, err := f.Open()
			if err == nil {
				b, _ := io.ReadAll(rc)
				rc.Close()
				gv, cells := ParseGoogleVoicePhonesVCF(string(b))
				if gv != "" {
					return gv, cells, nil
				}
			}
		}
	}

	// Fallback to sniffing HTML inside zip
	for _, f := range zr.File {
		if strings.HasSuffix(strings.ToLower(f.Name), ".html") && !strings.HasSuffix(strings.ToLower(f.Name), "bills.html") {
			rc, err := f.Open()
			if err == nil {
				buf := make([]byte, 8192)
				n, _ := rc.Read(buf)
				rc.Close()
				tel := sniffMeTelFromBytes(buf[:n])
				if tel != "" {
					return tel, nil, nil
				}
			}
		}
	}

	return "", nil, fmt.Errorf("no Google Voice phone number found in zip: %s", dirOrZip)
}

func sniffMeTelFromHTML(filePath string) string {
	f, err := os.Open(filePath)
	if err != nil {
		return ""
	}
	defer f.Close()
	buf := make([]byte, 8192)
	n, _ := f.Read(buf)
	return sniffMeTelFromBytes(buf[:n])
}

func sniffMeTelFromBytes(b []byte) string {
	content := string(b)
	// Look for Me sender link: <a class="tel" href="tel:+1..."><abbr class="fn" title="">Me</abbr>
	meIdx := strings.Index(content, ">Me<")
	if meIdx == -1 {
		meIdx = strings.Index(content, ">Me</abbr>")
	}
	if meIdx != -1 {
		start := meIdx - 150
		if start < 0 {
			start = 0
		}
		snippet := content[start : meIdx+30]
		if m := gvTelHrefRe.FindStringSubmatch(snippet); len(m) > 1 && m[1] != "" {
			return normalizePhoneNumber(m[1])
		}
	}
	return ""
}

// DetectGoogleVoiceAccountForPath retrieves or computes the Google Voice account phone number for an HTML file
func DetectGoogleVoiceAccountForPath(htmlPath string) string {
	dir := filepath.Dir(htmlPath)
	gvAccountCacheLock.RLock()
	if acc, ok := gvAccountCache[dir]; ok {
		gvAccountCacheLock.RUnlock()
		return acc
	}
	gvAccountCacheLock.RUnlock()

	acc, _, err := ExtractGoogleVoiceNumber(dir)
	if err != nil || acc == "" {
		// Try sniffing self from this specific file
		acc = sniffMeTelFromHTML(htmlPath)
	}

	gvAccountCacheLock.Lock()
	gvAccountCache[dir] = acc
	gvAccountCacheLock.Unlock()
	return acc
}

// parseISODuration converts ISO-8601 duration strings like PT29S or (00:00:29) into seconds
func parseISODuration(s, textFallback string) int {
	s = strings.TrimSpace(s)
	if s != "" {
		match := gvISODurationRe.FindStringSubmatch(s)
		if len(match) == 4 {
			hours, _ := strconv.Atoi(match[1])
			mins, _ := strconv.Atoi(match[2])
			secs, _ := strconv.Atoi(match[3])
			return hours*3600 + mins*60 + secs
		}
	}
	tf := strings.Trim(strings.TrimSpace(textFallback), "()")
	parts := strings.Split(tf, ":")
	if len(parts) == 3 {
		h, _ := strconv.Atoi(parts[0])
		m, _ := strconv.Atoi(parts[1])
		sec, _ := strconv.Atoi(parts[2])
		return h*3600 + m*60 + sec
	} else if len(parts) == 2 {
		m, _ := strconv.Atoi(parts[0])
		sec, _ := strconv.Atoi(parts[1])
		return m*60 + sec
	}
	n, _ := strconv.Atoi(tf)
	return n
}

// parseGoogleVoiceTimestamp parses Google Voice RFC-3339 timestamp with fallback
func parseGoogleVoiceTimestamp(raw string, fallbackFilename string) (time.Time, int64) {
	raw = strings.TrimSpace(raw)
	if raw != "" {
		if t, err := time.Parse(time.RFC3339Nano, raw); err == nil {
			return t, t.UnixMilli()
		}
		if t, err := time.Parse(time.RFC3339, raw); err == nil {
			return t, t.UnixMilli()
		}
	}
	// Fallback to filename timestamp
	ts := extractDateScoreFromFilename(fallbackFilename)
	if ts > 0 {
		t := time.Unix(ts, 0)
		return t, ts * 1000
	}
	now := time.Now()
	return now, now.UnixMilli()
}

// extractContactAndNumberFromFilename parses filenames like "+12014411841 - Text - 2025-10-13T17_12_47Z.html"
// or "InAmerica - Stella Wei - Text - 2025-10-18T15_02_07Z.html"
func extractContactAndNumberFromFilename(base string) (number string, contactName string, gvType string) {
	// Strip extension
	name := strings.TrimSuffix(base, filepath.Ext(base))

	// Look for - Type - separator
	typeDelims := []string{" - Text - ", " - Missed - ", " - Placed - ", " - Received - ", " - Voicemail - "}
	for _, delim := range typeDelims {
		if idx := strings.Index(name, delim); idx != -1 {
			prefix := strings.TrimSpace(name[:idx])
			gvType = strings.Trim(delim, " -")
			// Determine if prefix is phone number or contact name
			if strings.HasPrefix(prefix, "+") || (len(prefix) > 0 && prefix[0] >= '0' && prefix[0] <= '9') {
				number = normalizePhoneNumber(prefix)
			} else if prefix != "" && prefix != "-" {
				contactName = prefix
			}
			return
		}
	}

	if strings.HasPrefix(name, "Group Conversation") {
		gvType = "Group"
	}
	return
}

type GVMediaAttachment struct {
	Src         string
	ContentType string
	Data        string // base64 encoded data, or "null"
}

// MediaResolverFunc resolves a relative media filename into its data and detected MIME type
type MediaResolverFunc func(src string) ([]byte, string, error)

// MakeDirMediaResolver creates a media resolver for a local directory
func MakeDirMediaResolver(dir string) MediaResolverFunc {
	return func(src string) ([]byte, string, error) {
		src = strings.TrimSpace(src)
		if src == "" {
			return nil, "", fmt.Errorf("empty media source")
		}

		// 1. Direct path
		p := filepath.Join(dir, src)
		if fi, err := os.Stat(p); err == nil && !fi.IsDir() {
			data, err := os.ReadFile(p)
			return data, detectMimeType(p), err
		}

		// 2. Try common extensions
		commonExts := []string{".jpg", ".jpeg", ".png", ".gif", ".mp3", ".3gp", ".mp4", ".vcf", ".webp"}
		for _, ext := range commonExts {
			cand := p + ext
			if fi, err := os.Stat(cand); err == nil && !fi.IsDir() {
				data, err := os.ReadFile(cand)
				return data, detectMimeType(cand), err
			}
		}

		// 3. Fallback prefix match in directory
		baseSrc := filepath.Base(src)
		entries, err := os.ReadDir(dir)
		if err == nil {
			for _, e := range entries {
				if !e.IsDir() && strings.HasPrefix(e.Name(), baseSrc) {
					cand := filepath.Join(dir, e.Name())
					data, err := os.ReadFile(cand)
					return data, detectMimeType(cand), err
				}
			}
		}

		return nil, "", fmt.Errorf("media file not found: %s", src)
	}
}

func detectMimeType(filename string) string {
	ext := strings.ToLower(filepath.Ext(filename))
	switch ext {
	case ".jpg", ".jpeg":
		return "image/jpeg"
	case ".png":
		return "image/png"
	case ".gif":
		return "image/gif"
	case ".webp":
		return "image/webp"
	case ".mp3":
		return "audio/mp3"
	case ".wav":
		return "audio/wav"
	case ".ogg":
		return "audio/ogg"
	case ".3gp":
		return "video/3gpp"
	case ".mp4":
		return "video/mp4"
	case ".vcf":
		return "text/x-vcard"
	default:
		return "application/octet-stream"
	}
}

// splitHTMLMessageBlocks accurately extracts each <div class="message"> block even with nested tags
func splitHTMLMessageBlocks(content string) []string {
	var blocks []string
	const startTag = `<div class="message">`
	startIdx := 0

	for {
		idx := strings.Index(content[startIdx:], startTag)
		if idx == -1 {
			break
		}
		actualStart := startIdx + idx + len(startTag)

		// Find end of this message div tracking nesting depth
		depth := 1
		pos := actualStart
		for pos < len(content) && depth > 0 {
			nextOpen := strings.Index(content[pos:], "<div")
			nextClose := strings.Index(content[pos:], "</div>")

			if nextClose == -1 {
				break
			}

			if nextOpen != -1 && nextOpen < nextClose {
				depth++
				pos += nextOpen + 4
			} else {
				depth--
				if depth == 0 {
					blocks = append(blocks, content[actualStart:pos+nextClose])
					pos += nextClose + 6
					break
				}
				pos += nextClose + 6
			}
		}
		startIdx = pos
	}

	return blocks
}

// ParseGoogleVoiceHTML streams decoded SMS, MMS, and Call items from a single Google Voice HTML file
func ParseGoogleVoiceHTML(
	r io.Reader,
	filename string,
	includeMedia bool,
	gvAccount string,
	resolver MediaResolverFunc,
	onSMS func(sms *SMSEntry, dateMs int64) error,
	onMMS func(mms *MMSEntry, dateMs int64) error,
	onCall func(call *CallEntry, dateMs int64) error,
) error {
	b, err := io.ReadAll(r)
	if err != nil {
		return err
	}
	content := string(b)
	baseName := filepath.Base(filename)

	if strings.EqualFold(baseName, "bills.html") {
		return nil
	}

	fnNumber, fnContact, fnType := extractContactAndNumberFromFilename(baseName)

	// Check if this file is a Call or Voicemail
	isCall := strings.Contains(content, `class="haudio"`) ||
		strings.Contains(baseName, " - Received - ") ||
		strings.Contains(baseName, " - Placed - ") ||
		strings.Contains(baseName, " - Missed - ") ||
		strings.Contains(baseName, " - Voicemail - ") ||
		strings.HasPrefix(baseName, "- Missed -") ||
		strings.HasPrefix(baseName, "- Received -")

	if isCall {
		return parseGoogleVoiceCall(content, baseName, fnNumber, fnContact, fnType, gvAccount, onCall)
	}

	// Otherwise, it is a Text message or Group Conversation
	return parseGoogleVoiceConversation(content, baseName, fnNumber, fnContact, fnType, gvAccount, includeMedia, resolver, onSMS, onMMS)
}

func parseGoogleVoiceCall(
	content, baseName, fnNumber, fnContact, fnType, gvAccount string,
	onCall func(call *CallEntry, dateMs int64) error,
) error {
	// 1. Timestamp
	rawDate := ""
	if m := gvPublishedRe.FindStringSubmatch(content); len(m) > 1 {
		rawDate = m[1]
	}
	t, dateMs := parseGoogleVoiceTimestamp(rawDate, baseName)
	dateStr := strconv.FormatInt(dateMs, 10)
	readableDate := t.Format("Jan 02, 2006 3:04:05 PM")

	// 2. Call Type: 1 = Incoming/Received, 2 = Outgoing/Placed, 3 = Missed, 4 = Voicemail
	callType := "1"
	tagContent := ""
	if m := gvTagsDivRe.FindStringSubmatch(content); len(m) > 1 {
		tagContent = strings.ToLower(m[1])
	}
	titleContent := ""
	if m := gvTitleRe.FindStringSubmatch(content); len(m) > 1 {
		titleContent = strings.ToLower(m[1])
	}

	if strings.Contains(tagContent, "#missed") || strings.Contains(titleContent, "missed") || strings.EqualFold(fnType, "missed") {
		callType = "3"
	} else if strings.Contains(tagContent, "#voicemail") || strings.Contains(titleContent, "voicemail") || strings.EqualFold(fnType, "voicemail") {
		callType = "4"
	} else if strings.Contains(tagContent, "#placed") || strings.Contains(titleContent, "placed") || strings.EqualFold(fnType, "placed") {
		callType = "2"
	} else {
		callType = "1" // Received
	}

	// 3. Duration
	durationStr := "0"
	if m := gvDurationRe.FindStringSubmatch(content); len(m) > 2 {
		durSec := parseISODuration(m[1], m[2])
		durationStr = strconv.Itoa(durSec)
	}

	// 4. Phone Number & Contact Name
	number := fnNumber
	contact := fnContact

	// Look inside contributor vcard
	if m := gvTelHrefRe.FindStringSubmatch(content); len(m) > 1 && m[1] != "" {
		parsedNum := normalizePhoneNumber(m[1])
		if parsedNum != "" {
			number = parsedNum
		}
	}
	if m := gvFnRe.FindStringSubmatch(content); len(m) > 1 {
		name := strings.TrimSpace(m[1])
		if name != "" && !strings.EqualFold(name, "me") {
			contact = name
		}
	}

	if number == "" {
		if contact != "" {
			number = contact
		} else {
			number = "Unknown"
		}
	}
	if contact == "" {
		contact = "(Unknown)"
	}

	call := CallEntry{
		Account:        gvAccount,
		Number:         number,
		Duration:       durationStr,
		Date:           dateStr,
		Type:           callType,
		Presentation:   "1",
		SubscriptionID: "-1",
		ReadableDate:   readableDate,
		ContactName:    contact,
	}

	return onCall(&call, dateMs)
}

func parseGoogleVoiceConversation(
	content, baseName, fnNumber, fnContact, fnType, gvAccount string,
	includeMedia bool,
	resolver MediaResolverFunc,
	onSMS func(sms *SMSEntry, dateMs int64) error,
	onMMS func(mms *MMSEntry, dateMs int64) error,
) error {
	// 1. Group conversation participant extraction
	isGroup := strings.HasPrefix(baseName, "Group Conversation") || strings.Contains(content, `class="participants"`)
	var groupParticipants []struct {
		Phone string
		Name  string
	}

	if isGroup {
		partIdx := strings.Index(content, `<div class="participants">`)
		if partIdx != -1 {
			endIdx := strings.Index(content[partIdx:], "</div>")
			if endIdx != -1 {
				partBlock := content[partIdx : partIdx+endIdx]
				tels := gvTelHrefRe.FindAllStringSubmatch(partBlock, -1)
				names := gvFnRe.FindAllStringSubmatch(partBlock, -1)
				for i, t := range tels {
					phone := normalizePhoneNumber(t[1])
					name := ""
					if i < len(names) {
						name = strings.TrimSpace(names[i][1])
					}
					if phone != "" {
						groupParticipants = append(groupParticipants, struct {
							Phone string
							Name  string
						}{Phone: phone, Name: name})
					}
				}
			}
		}
	}

	// 2. Extract conversation partner for 1-on-1 chats
	partnerNumber := fnNumber
	partnerName := fnContact
	if partnerNumber == "" && partnerName == "" {
		// Look at title: e.g. <title>Me to InAmerica - Stella Wei</title>
		if m := gvTitleRe.FindStringSubmatch(content); len(m) > 1 {
			tStr := strings.TrimSpace(m[1])
			tStr = strings.TrimPrefix(tStr, "Me to")
			tStr = strings.TrimSpace(tStr)
			if tStr != "" {
				partnerName = tStr
			}
		}
	}

	// 3. Process each message block
	msgBlocks := splitHTMLMessageBlocks(content)
	if len(msgBlocks) == 0 {
		return nil
	}

	for _, block := range msgBlocks {
		// Timestamp
		rawDate := ""
		if m := gvDtRe.FindStringSubmatch(block); len(m) > 1 {
			rawDate = m[1]
		}
		t, dateMs := parseGoogleVoiceTimestamp(rawDate, baseName)
		dateStr := strconv.FormatInt(dateMs, 10)
		readableDate := t.Format("Jan 02, 2006 3:04:05 PM")

		// Sender detection
		senderBlock := ""
		if idx := strings.Index(block, `class="sender vcard"`); idx != -1 {
			endIdx := strings.Index(block[idx:], "</cite>")
			if endIdx != -1 {
				senderBlock = block[idx : idx+endIdx]
			}
		}

		isMe := strings.Contains(senderBlock, ">Me<") ||
			strings.Contains(senderBlock, ">Me</abbr>") ||
			strings.Contains(senderBlock, ">Me</span>") ||
			strings.Contains(block, "<cite class=\"sender vcard\">Me</cite>")

		senderTel := ""
		if m := gvTelHrefRe.FindStringSubmatch(senderBlock); len(m) > 1 && m[1] != "" {
			senderTel = normalizePhoneNumber(m[1])
		}
		senderName := ""
		if m := gvFnRe.FindStringSubmatch(senderBlock); len(m) > 1 {
			senderName = strings.TrimSpace(m[1])
		}

		// Message body text
		body := ""
		if m := gvQRe.FindStringSubmatch(block); len(m) > 1 {
			rawText := m[1]
			// Replace <br> with newline
			rawText = gvBrRe.ReplaceAllString(rawText, "\n")
			body = html.UnescapeString(strings.TrimSpace(rawText))
		}

		// Attachments
		var mediaList []GVMediaAttachment
		findAndAddMedia := func(src, defaultMime string) {
			src = strings.TrimSpace(src)
			if src == "" {
				return
			}
			mime := defaultMime
			dataVal := "null"
			if resolver != nil {
				fileBytes, detectedMime, err := resolver(src)
				if err == nil {
					if detectedMime != "" {
						mime = detectedMime
					}
					if includeMedia {
						dataVal = base64.StdEncoding.EncodeToString(fileBytes)
					}
				}
			}
			mediaList = append(mediaList, GVMediaAttachment{
				Src:         src,
				ContentType: mime,
				Data:        dataVal,
			})
		}

		for _, m := range gvImgSrcRe.FindAllStringSubmatch(block, -1) {
			findAndAddMedia(m[1], "image/jpeg")
		}
		for _, m := range gvAudioSrcRe.FindAllStringSubmatch(block, -1) {
			findAndAddMedia(m[1], "audio/mp3")
		}
		for _, m := range gvVideoSrcRe.FindAllStringSubmatch(block, -1) {
			findAndAddMedia(m[1], "video/3gpp")
		}
		for _, m := range gvAMediaRe.FindAllStringSubmatch(block, -1) {
			kind := m[1]
			src := m[2]
			switch kind {
			case "video":
				findAndAddMedia(src, "video/3gpp")
			case "audio":
				findAndAddMedia(src, "audio/mp3")
			case "vcard":
				findAndAddMedia(src, "text/x-vcard")
			case "image":
				findAndAddMedia(src, "image/jpeg")
			}
		}

		// Update partner info from incoming message if 1-on-1 and not known
		if !isGroup && !isMe {
			if senderTel != "" && partnerNumber == "" {
				partnerNumber = senderTel
			}
			if senderName != "" && partnerName == "" {
				partnerName = senderName
			}
		}

		msgBox := "1" // incoming
		if isMe {
			msgBox = "2" // outgoing
		}

		// Clean body text if it's the standard Google Voice MMS placeholder
		isMMSPlaceholder := strings.EqualFold(body, "MMS Received") || strings.EqualFold(body, "MMS Sent")
		if isMMSPlaceholder && len(mediaList) > 0 {
			body = ""
		}

		// Decision: SMS vs MMS
		if !isGroup && len(mediaList) == 0 {
			// Plain 1-on-1 SMS
			addr := partnerNumber
			if addr == "" {
				if partnerName != "" {
					addr = partnerName
				} else {
					addr = "Unknown"
				}
			}
			cName := partnerName
			if cName == "" {
				cName = "(Unknown)"
			}

			sms := SMSEntry{
				Account:       gvAccount,
				Address:       addr,
				Date:          dateStr,
				Type:          msgBox,
				Body:          body,
				Read:          "1",
				Status:        "-1",
				Protocol:      "0",
				ReadableDate:  readableDate,
				ContactName:   cName,
				ServiceCenter: "null",
				Subject:       "null",
				TOA:           "null",
				SCTOA:         "null",
				SubID:         "1",
			}
			if err := onSMS(&sms, dateMs); err != nil {
				return err
			}
		} else {
			// MMS (Group or Media attachment)
			mms := MMSEntry{
				Account:      gvAccount,
				Date:         dateStr,
				Type:         msgBox,
				Read:         "1",
				Subject:      "null",
				Body:         body,
				ReadableDate: readableDate,
				ContentType:  "application/vnd.wap.mms-message",
			}

			// Parts
			partSeq := 0
			if body != "" {
				mms.Parts = append(mms.Parts, MMSPart{
					Seq:         strconv.Itoa(partSeq),
					ContentType: "text/plain",
					Text:        body,
					CL:          fmt.Sprintf("text_%d.txt", partSeq),
				})
				partSeq++
			}
			for _, med := range mediaList {
				mms.Parts = append(mms.Parts, MMSPart{
					Seq:         strconv.Itoa(partSeq),
					ContentType: med.ContentType,
					Name:        filepath.Base(med.Src),
					CL:          filepath.Base(med.Src),
					Data:        med.Data,
				})
				partSeq++
			}

			// Addrs & Address
			if isGroup {
				var addrList []string
				for _, p := range groupParticipants {
					if p.Phone != "" {
						addrList = append(addrList, p.Phone)
					}
				}
				sort.Strings(addrList)
				mms.Address = strings.Join(addrList, "~")
				if isMe {
					myPhone := gvAccount
					if senderTel != "" {
						myPhone = senderTel
					}
					mms.Addrs = append(mms.Addrs, MMSAddr{Address: myPhone, Type: "137", Charset: "106"})
					for _, p := range groupParticipants {
						mms.Addrs = append(mms.Addrs, MMSAddr{Address: p.Phone, Type: "151", Charset: "106"})
					}
				} else {
					fromPhone := senderTel
					if fromPhone == "" && len(groupParticipants) > 0 {
						fromPhone = groupParticipants[0].Phone
					}
					mms.Addrs = append(mms.Addrs, MMSAddr{Address: fromPhone, Type: "137", Charset: "106"})
					for _, p := range groupParticipants {
						if p.Phone != fromPhone {
							mms.Addrs = append(mms.Addrs, MMSAddr{Address: p.Phone, Type: "151", Charset: "106"})
						}
					}
					if gvAccount != "" {
						mms.Addrs = append(mms.Addrs, MMSAddr{Address: gvAccount, Type: "151", Charset: "106"})
					}
				}
			} else {
				// 1-on-1 MMS
				addr := partnerNumber
				if addr == "" {
					addr = partnerName
				}
				mms.Address = addr
				mms.ContactName = partnerName
				myPhone := gvAccount
				if isMe {
					if senderTel != "" {
						myPhone = senderTel
					}
					mms.Addrs = append(mms.Addrs, MMSAddr{Address: myPhone, Type: "137", Charset: "106"})
					mms.Addrs = append(mms.Addrs, MMSAddr{Address: addr, Type: "151", Charset: "106"})
				} else {
					mms.Addrs = append(mms.Addrs, MMSAddr{Address: addr, Type: "137", Charset: "106"})
					mms.Addrs = append(mms.Addrs, MMSAddr{Address: myPhone, Type: "151", Charset: "106"})
				}
			}

			if err := onMMS(&mms, dateMs); err != nil {
				return err
			}
		}
	}

	return nil
}

// DecodeGoogleVoiceZip parses an entire Google Voice Takeout zip archive, streaming SMS, MMS, and Call entries
func DecodeGoogleVoiceZip(
	zipPath string,
	includeMedia bool,
	onSMS func(sms *SMSEntry, dateMs int64) error,
	onMMS func(mms *MMSEntry, dateMs int64) error,
	onCall func(call *CallEntry, dateMs int64) error,
) error {
	zr, err := zip.OpenReader(zipPath)
	if err != nil {
		return err
	}
	defer zr.Close()

	// 1. Detect Google Voice Account
	gvAccount, _, _ := ExtractGoogleVoiceNumber(zipPath)

	// 2. Build index of files in zip for fast media resolution
	zipFileMap := make(map[string]*zip.File)
	var htmlFiles []*zip.File

	for _, f := range zr.File {
		name := f.Name
		zipFileMap[name] = f
		zipFileMap[filepath.Base(name)] = f
		lower := strings.ToLower(name)
		if strings.HasSuffix(lower, ".html") && !strings.HasSuffix(lower, "bills.html") {
			htmlFiles = append(htmlFiles, f)
		}
	}

	resolver := func(src string) ([]byte, string, error) {
		src = strings.TrimSpace(src)
		base := filepath.Base(src)
		zf, ok := zipFileMap[base]
		if !ok {
			// Try extensions
			commonExts := []string{".jpg", ".jpeg", ".png", ".gif", ".mp3", ".3gp", ".mp4", ".vcf"}
			for _, ext := range commonExts {
				if cand, found := zipFileMap[base+ext]; found {
					zf = cand
					ok = true
					break
				}
			}
		}
		if !ok {
			return nil, "", fmt.Errorf("media not found in zip: %s", src)
		}
		rc, err := zf.Open()
		if err != nil {
			return nil, "", err
		}
		defer rc.Close()
		data, err := io.ReadAll(rc)
		return data, detectMimeType(zf.Name), err
	}

	// 3. Process HTML files
	for _, zf := range htmlFiles {
		rc, err := zf.Open()
		if err != nil {
			continue
		}
		_ = ParseGoogleVoiceHTML(rc, zf.Name, includeMedia, gvAccount, resolver, onSMS, onMMS, onCall)
		rc.Close()
	}

	return nil
}

// DecodeGoogleVoiceHTMLFile decodes a single Google Voice HTML file from disk
func DecodeGoogleVoiceHTMLFile(
	filePath string,
	includeMedia bool,
	gvAccount string,
	onSMS func(sms *SMSEntry, dateMs int64) error,
	onMMS func(mms *MMSEntry, dateMs int64) error,
	onCall func(call *CallEntry, dateMs int64) error,
) error {
	f, err := os.Open(filePath)
	if err != nil {
		return err
	}
	defer f.Close()

	dir := filepath.Dir(filePath)
	if gvAccount == "" {
		gvAccount = DetectGoogleVoiceAccountForPath(filePath)
	}

	resolver := MakeDirMediaResolver(dir)
	return ParseGoogleVoiceHTML(f, filePath, includeMedia, gvAccount, resolver, onSMS, onMMS, onCall)
}
