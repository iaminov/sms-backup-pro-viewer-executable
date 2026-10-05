package internal

import (
	"archive/zip"
	"bufio"
	"crypto/md5"
	"database/sql"
	"encoding/xml"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/labstack/echo/v4"
)

type MergeOptions struct {
	SourceFolder    string `json:"source_folder"`
	OutputFile      string `json:"output_file"`
	IncludeMedia    bool   `json:"include_media"`
	NormalizeSchema bool   `json:"normalize_schema"`
}

type MergeProgress struct {
	Status             string    `json:"status"` // "idle", "scanning", "merging", "writing", "completed", "error"
	TotalFiles         int       `json:"total_files"`
	ProcessedFiles     int       `json:"processed_files"`
	CurrentFile        string    `json:"current_file"`
	TotalFoundMessages int       `json:"total_found_messages"`
	UniqueMessages     int       `json:"unique_messages"`
	DuplicatesRemoved  int       `json:"duplicates_removed"`
	Percent            int       `json:"percent"`
	OutputFile         string    `json:"output_file"`
	OutputSize         int64     `json:"output_size"`
	Duration           string    `json:"duration,omitempty"`
	ErrorMessage       string    `json:"error_message,omitempty"`
	StartTime          time.Time `json:"start_time"`
}

var (
	mergeProgress     *MergeProgress
	mergeProgressLock sync.RWMutex
	dateInNameRe      = regexp.MustCompile(`(\d{4})[-_]?(\d{2})[-_]?(\d{2})[-_]?(\d{2})?[-_]?(\d{2})?[-_]?(\d{2})?`)
)

func GetMergeProgress() *MergeProgress {
	mergeProgressLock.RLock()
	defer mergeProgressLock.RUnlock()
	if mergeProgress == nil {
		return &MergeProgress{Status: "idle"}
	}
	cp := *mergeProgress
	return &cp
}

func updateMergeProgress(fn func(p *MergeProgress)) {
	mergeProgressLock.Lock()
	defer mergeProgressLock.Unlock()
	if mergeProgress == nil { mergeProgress = &MergeProgress{Status: "idle"} }; fn(mergeProgress)
}

// DiscoverBackupFiles recursively finds all .xml and .zip files in the directory
func DiscoverBackupFiles(root string) ([]string, error) {
	var files []string
	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		if info.IsDir() {
			return nil
		}
		lower := strings.ToLower(path)
		if strings.HasSuffix(lower, ".xml") || strings.HasSuffix(lower, ".zip") {
			files = append(files, path)
		}
		return nil
	})
	return files, err
}

func extractDateScoreFromFilename(path string) int64 {
	base := filepath.Base(path)
	match := dateInNameRe.FindStringSubmatch(base)
	if len(match) > 3 {
		year, _ := strconv.Atoi(match[1])
		month, _ := strconv.Atoi(match[2])
		day, _ := strconv.Atoi(match[3])
		hour, min, sec := 0, 0, 0
		if len(match) > 4 && match[4] != "" {
			hour, _ = strconv.Atoi(match[4])
		}
		if len(match) > 5 && match[5] != "" {
			min, _ = strconv.Atoi(match[5])
		}
		if len(match) > 6 && match[6] != "" {
			sec, _ = strconv.Atoi(match[6])
		}
		t := time.Date(year, time.Month(month), day, hour, min, sec, 0, time.UTC)
		return t.Unix()
	}
	info, err := os.Stat(path)
	if err == nil {
		return info.ModTime().Unix()
	}
	return 0
}

func sortFilesChronologically(files []string) {
	sort.Slice(files, func(i, j int) bool {
		scoreI := extractDateScoreFromFilename(files[i])
		scoreJ := extractDateScoreFromFilename(files[j])
		if scoreI != scoreJ {
			return scoreI < scoreJ
		}
		return files[i] < files[j]
	})
}

// TargetSchema holds attribute orders and defaults sampled from the newest backup file
type TargetSchema struct {
	SMSAttrs []string
	MMSAttrs []string
}

func getStandardSMSAttrs() []string {
	return []string{
		"protocol", "address", "date", "type", "subject", "body",
		"toa", "sc_toa", "service_center", "read", "status", "locked",
		"date_sent", "sub_id", "readable_date", "contact_name",
	}
}

func getStandardSMSDefault(attr string, dateMs int64) string {
	switch attr {
	case "protocol":
		return "0"
	case "type":
		return "1"
	case "read":
		return "1"
	case "status":
		return "-1"
	case "locked":
		return "0"
	case "sub_id":
		return "1"
	case "date_sent":
		if dateMs > 0 {
			return fmt.Sprintf("%d", dateMs)
		}
		return "0"
	case "readable_date":
		if dateMs > 0 {
			return time.Unix(dateMs/1000, 0).Format("Jan 02, 2006 3:04:05 PM")
		}
		return "null"
	case "contact_name":
		return "(Unknown)"
	case "toa", "sc_toa", "service_center", "subject":
		return "null"
	default:
		return "null"
	}
}

func getAttrValue(attrs []xml.Attr, name string) string {
	for _, a := range attrs {
		if a.Name.Local == name {
			return a.Value
		}
	}
	return ""
}

// computeSMSDedupKey produces a deterministic 16-byte MD5 hash for an SMS message
func computeSMSDedupKey(address, dateStr, msgType, body string) [16]byte {
	normAddr := normalizePhoneNumber(address)
	trimmedBody := strings.TrimSpace(body)
	h := md5.New()
	h.Write([]byte("sms:"))
	h.Write([]byte(normAddr))
	h.Write([]byte(":"))
	h.Write([]byte(dateStr))
	h.Write([]byte(":"))
	h.Write([]byte(msgType))
	h.Write([]byte(":"))
	h.Write([]byte(trimmedBody))
	var res [16]byte
	copy(res[:], h.Sum(nil))
	return res
}

// computeMMSDedupKey produces a deterministic 16-byte MD5 hash for an MMS message
func computeMMSDedupKey(mms *MMSEntry) [16]byte {
	mID := strings.TrimSpace(mms.MessageID)
	trID := strings.TrimSpace(mms.TrID)
	dateStr := strings.TrimSpace(mms.Date)

	h := md5.New()
	h.Write([]byte("mms:"))

	if mID != "" && !strings.EqualFold(mID, "null") {
		h.Write([]byte("mid:"))
		h.Write([]byte(mID))
		h.Write([]byte(":"))
		h.Write([]byte(dateStr))
	} else if trID != "" && !strings.EqualFold(trID, "null") {
		h.Write([]byte("trid:"))
		h.Write([]byte(trID))
		h.Write([]byte(":"))
		h.Write([]byte(dateStr))
	} else {
		h.Write([]byte("comp:"))
		h.Write([]byte(normalizePhoneNumber(mms.Address)))
		h.Write([]byte(":"))
		h.Write([]byte(dateStr))
		h.Write([]byte(":"))
		h.Write([]byte(mms.Type))
		for _, p := range mms.Parts {
			if p.Text != "" && !strings.EqualFold(p.Text, "null") {
				h.Write([]byte(p.Text))
			}
		}
	}
	var res [16]byte
	copy(res[:], h.Sum(nil))
	return res
}

func formatNormalizedSMS(attrs []xml.Attr, schema []string, dateMs int64) []byte {
	attrMap := make(map[string]string, len(attrs))
	for _, a := range attrs {
		attrMap[a.Name.Local] = a.Value
	}

	var sb strings.Builder
	sb.WriteString("<sms")

	seen := make(map[string]bool)
	// Write attributes in canonical target schema order
	for _, name := range schema {
		val, exists := attrMap[name]
		if !exists {
			val = getStandardSMSDefault(name, dateMs)
		}
		sb.WriteString(" ")
		sb.WriteString(name)
		sb.WriteString("=\"")
		xml.EscapeText(&sb, []byte(val))
		sb.WriteString("\"")
		seen[name] = true
	}

	// Append any extra legacy attributes that might be on the item but not in standard schema
	for _, a := range attrs {
		if !seen[a.Name.Local] {
			sb.WriteString(" ")
			sb.WriteString(a.Name.Local)
			sb.WriteString("=\"")
			xml.EscapeText(&sb, []byte(a.Value))
			sb.WriteString("\"")
		}
	}

	sb.WriteString(" />")
	return []byte(sb.String())
}

func formatNormalizedMMS(elem *xml.StartElement, mms *MMSEntry, includeMedia bool) []byte {
	var sb strings.Builder
	sb.WriteString("<mms")

	// Write top-level MMS attributes
	for _, a := range elem.Attr {
		sb.WriteString(" ")
		sb.WriteString(a.Name.Local)
		sb.WriteString("=\"")
		xml.EscapeText(&sb, []byte(a.Value))
		sb.WriteString("\"")
	}
	sb.WriteString(">\n")

	// Parts
	if len(mms.Parts) > 0 {
		sb.WriteString("    <parts>\n")
		for _, p := range mms.Parts {
			dataVal := p.Data
			if !includeMedia && dataVal != "" && !isTextContentType(p.ContentType) {
				dataVal = "null"
			}

			sb.WriteString("      <part")
			if p.Seq != "" {
				sb.WriteString(fmt.Sprintf(" seq=\"%s\"", p.Seq))
			}
			if p.ContentType != "" {
				sb.WriteString(" ct=\"")
				xml.EscapeText(&sb, []byte(p.ContentType))
				sb.WriteString("\"")
			}
			if p.Name != "" {
				sb.WriteString(" name=\"")
				xml.EscapeText(&sb, []byte(p.Name))
				sb.WriteString("\"")
			}
			if p.Charset != "" {
				sb.WriteString(fmt.Sprintf(" chset=\"%s\"", p.Charset))
			}
			if p.CL != "" {
				sb.WriteString(" cl=\"")
				xml.EscapeText(&sb, []byte(p.CL))
				sb.WriteString("\"")
			}
			if p.Text != "" {
				sb.WriteString(" text=\"")
				xml.EscapeText(&sb, []byte(p.Text))
				sb.WriteString("\"")
			}
			if dataVal != "" {
				sb.WriteString(" data=\"")
				xml.EscapeText(&sb, []byte(dataVal))
				sb.WriteString("\"")
			}
			sb.WriteString(" />\n")
		}
		sb.WriteString("    </parts>\n")
	}

	// Addrs
	if len(mms.Addrs) > 0 {
		sb.WriteString("    <addrs>\n")
		for _, addr := range mms.Addrs {
			sb.WriteString("      <addr")
			if addr.Address != "" {
				sb.WriteString(" address=\"")
				xml.EscapeText(&sb, []byte(addr.Address))
				sb.WriteString("\"")
			}
			if addr.Type != "" {
				sb.WriteString(fmt.Sprintf(" type=\"%s\"", addr.Type))
			}
			if addr.Charset != "" {
				sb.WriteString(fmt.Sprintf(" charset=\"%s\"", addr.Charset))
			}
			sb.WriteString(" />\n")
		}
		sb.WriteString("    </addrs>\n")
	}

	sb.WriteString("  </mms>")
	return []byte(sb.String())
}

// MergeBackupsToSingleXML scans all XML/ZIP files, streams & deduplicates via temporary SQLite staging,
// and streams out a single, perfectly sorted, unified XML backup file.
func MergeBackupsToSingleXML(opts MergeOptions) (*MergeProgress, error) {
	startTime := time.Now()

	updateMergeProgress(func(p *MergeProgress) {
		p.Status = "scanning"
		p.StartTime = startTime
		p.TotalFiles = 0
		p.ProcessedFiles = 0
		p.TotalFoundMessages = 0
		p.UniqueMessages = 0
		p.DuplicatesRemoved = 0
		p.Percent = 0
		p.ErrorMessage = ""
	})

	files, err := DiscoverBackupFiles(opts.SourceFolder)
	if err != nil {
		return nil, fmt.Errorf("failed to scan directory: %w", err)
	}
	if len(files) == 0 {
		return nil, fmt.Errorf("no .xml or .zip backup files found in: %s", opts.SourceFolder)
	}

	sortFilesChronologically(files)

	updateMergeProgress(func(p *MergeProgress) {
		p.TotalFiles = len(files)
		p.Status = "merging"
	})

	// Create temporary SQLite database for zero-RAM streaming deduplication
	tmpDBPath := filepath.Join(os.TempDir(), fmt.Sprintf("sbv_merge_%d.db", time.Now().UnixNano()))
	defer os.Remove(tmpDBPath)

	db, err := sql.Open("sqlite3", tmpDBPath)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize staging database: %w", err)
	}
	defer db.Close()

	// High performance tuning for temporary staging database
	_, _ = db.Exec("PRAGMA synchronous = OFF;")
	_, _ = db.Exec("PRAGMA journal_mode = MEMORY;")
	_, _ = db.Exec("PRAGMA temp_store = MEMORY;")
	_, _ = db.Exec("PRAGMA cache_size = -64000;") // 64MB cache

	createTableSQL := `
	CREATE TABLE IF NOT EXISTS staging_records (
		dedup_key BLOB PRIMARY KEY,
		item_date INTEGER NOT NULL,
		richness INTEGER NOT NULL,
		xml_data BLOB NOT NULL
	);
	CREATE INDEX IF NOT EXISTS idx_staging_date ON staging_records(item_date ASC);
	`
	if _, err := db.Exec(createTableSQL); err != nil {
		return nil, fmt.Errorf("failed to create staging table: %w", err)
	}

	stmt, err := db.Prepare(`
		INSERT INTO staging_records (dedup_key, item_date, richness, xml_data)
		VALUES (?, ?, ?, ?)
		ON CONFLICT(dedup_key) DO UPDATE SET
			xml_data = CASE WHEN excluded.richness > staging_records.richness THEN excluded.xml_data ELSE staging_records.xml_data END,
			richness = MAX(staging_records.richness, excluded.richness);
	`)
	if err != nil {
		return nil, fmt.Errorf("failed to prepare staging insert: %w", err)
	}
	defer stmt.Close()

	smsSchema := getStandardSMSAttrs()

	var totalFound int
	var tx *sql.Tx
	txCount := 0

	tx, err = db.Begin()
	if err != nil {
		return nil, err
	}

	for fileIdx, filePath := range files {
		fileName := filepath.Base(filePath)
		updateMergeProgress(func(p *MergeProgress) {
			p.ProcessedFiles = fileIdx + 1
			p.CurrentFile = fileName
			if p.TotalFiles > 0 {
				// Files scan is 0% to 80% of total progress
				p.Percent = int((float64(fileIdx+1) / float64(p.TotalFiles)) * 80)
			}
		})

		processXMLStream := func(reader io.Reader) error {
			decoder := xml.NewDecoder(reader)
			for {
				token, err := decoder.Token()
				if err == io.EOF {
					break
				}
				if err != nil {
					break
				}

				startElem, ok := token.(xml.StartElement)
				if !ok {
					continue
				}

				if startElem.Name.Local == "sms" {
					totalFound++
					dateStr := getAttrValue(startElem.Attr, "date")
					dateMs, _ := strconv.ParseInt(dateStr, 10, 64)
					addr := getAttrValue(startElem.Attr, "address")
					msgType := getAttrValue(startElem.Attr, "type")
					body := getAttrValue(startElem.Attr, "body")
					contact := getAttrValue(startElem.Attr, "contact_name")

					key := computeSMSDedupKey(addr, dateStr, msgType, body)
					richness := 1
					if contact != "" && !strings.EqualFold(contact, "(unknown)") && !strings.EqualFold(contact, "null") {
						richness = 2
					}

					xmlBytes := formatNormalizedSMS(startElem.Attr, smsSchema, dateMs)
					_, _ = tx.Stmt(stmt).Exec(key[:], dateMs, richness, xmlBytes)
					txCount++

					if txCount >= 2000 {
						_ = tx.Commit()
						tx, _ = db.Begin()
						txCount = 0
					}
				} else if startElem.Name.Local == "mms" {
					totalFound++
					var mms MMSEntry
					if err := decoder.DecodeElement(&mms, &startElem); err == nil {
						dateMs, _ := strconv.ParseInt(mms.Date, 10, 64)
						key := computeMMSDedupKey(&mms)

						richness := 1
						hasMedia := false
						for _, p := range mms.Parts {
							if p.Data != "" && !isTextContentType(p.ContentType) && !strings.EqualFold(p.Data, "null") {
								hasMedia = true
								break
							}
						}
						if hasMedia {
							richness = 10
						}
						if mms.ContactName != "" && !strings.EqualFold(mms.ContactName, "(unknown)") && !strings.EqualFold(mms.ContactName, "null") {
							richness += 2
						}

						xmlBytes := formatNormalizedMMS(&startElem, &mms, opts.IncludeMedia)
						_, _ = tx.Stmt(stmt).Exec(key[:], dateMs, richness, xmlBytes)
						txCount++

						if txCount >= 2000 {
							_ = tx.Commit()
							tx, _ = db.Begin()
							txCount = 0
						}
					}
				}
			}
			return nil
		}

		if strings.HasSuffix(strings.ToLower(filePath), ".zip") {
			zReader, err := zip.OpenReader(filePath)
			if err == nil {
				for _, zFile := range zReader.File {
					if strings.HasSuffix(strings.ToLower(zFile.Name), ".xml") {
						rc, err := zFile.Open()
						if err == nil {
							_ = processXMLStream(rc)
							rc.Close()
						}
					}
				}
				zReader.Close()
			}
		} else {
			f, err := os.Open(filePath)
			if err == nil {
				_ = processXMLStream(f)
				f.Close()
			}
		}

		updateMergeProgress(func(p *MergeProgress) {
			p.TotalFoundMessages = totalFound
		})
	}

	if tx != nil {
		_ = tx.Commit()
	}

	// Count unique messages
	var uniqueCount int
	_ = db.QueryRow("SELECT COUNT(*) FROM staging_records").Scan(&uniqueCount)
	duplicatesRemoved := totalFound - uniqueCount
	if duplicatesRemoved < 0 {
		duplicatesRemoved = 0
	}

	updateMergeProgress(func(p *MergeProgress) {
		p.Status = "writing"
		p.UniqueMessages = uniqueCount
		p.DuplicatesRemoved = duplicatesRemoved
		p.Percent = 85
	})

	// Prepare output file
	outputFile := opts.OutputFile
	if strings.TrimSpace(outputFile) == "" {
		outputFile = filepath.Join(opts.SourceFolder, "merged_sms_backup.xml")
	}
	absOut, err := filepath.Abs(outputFile)
	if err == nil {
		outputFile = absOut
	}
	if err := os.MkdirAll(filepath.Dir(outputFile), 0755); err != nil {
		return nil, fmt.Errorf("failed to create destination directory: %w", err)
	}

	outF, err := os.Create(outputFile)
	if err != nil {
		return nil, fmt.Errorf("failed to create output XML file: %w", err)
	}
	defer outF.Close()

	w := bufio.NewWriterSize(outF, 1024*1024) // 1MB buffer

	w.WriteString("<?xml version='1.0' encoding='UTF-8' standalone='yes' ?>\n")
	w.WriteString(fmt.Sprintf("<!--File Created By SMS Backup & Restore Merger on %s-->\n", time.Now().Format("02/01/2006 15:04:05")))
	w.WriteString("<?xml-stylesheet type=\"text/xsl\" href=\"sms.xsl\"?>\n")
	w.WriteString(fmt.Sprintf("<smses count=\"%d\" backup_set=\"%s\" backup_date=\"%d\">\n", uniqueCount, uuid.New().String(), time.Now().UnixMilli()))

	// Query sorted chronologically (earliest to latest)
	rows, err := db.Query("SELECT xml_data FROM staging_records ORDER BY item_date ASC")
	if err != nil {
		return nil, fmt.Errorf("failed to query sorted records: %w", err)
	}
	defer rows.Close()

	writtenCount := 0
	for rows.Next() {
		var xmlBytes []byte
		if err := rows.Scan(&xmlBytes); err == nil {
			w.WriteString("  ")
			w.Write(xmlBytes)
			w.WriteString("\n")
			writtenCount++

			if writtenCount%5000 == 0 && uniqueCount > 0 {
				pct := 85 + int((float64(writtenCount)/float64(uniqueCount))*15)
				if pct > 99 {
					pct = 99
				}
				updateMergeProgress(func(p *MergeProgress) {
					p.Percent = pct
				})
			}
		}
	}

	w.WriteString("</smses>\n")
	w.Flush()

	fi, _ := outF.Stat()
	outSize := int64(0)
	if fi != nil {
		outSize = fi.Size()
	}

	duration := time.Since(startTime)
	updateMergeProgress(func(p *MergeProgress) {
		p.Status = "completed"
		p.Percent = 100
		p.OutputFile = outputFile
		p.OutputSize = outSize
		p.Duration = duration.Round(time.Millisecond).String()
	})

	slog.Info("Merge completed successfully",
		"totalFiles", len(files),
		"foundMessages", totalFound,
		"uniqueMessages", uniqueCount,
		"duplicatesRemoved", duplicatesRemoved,
		"outputFile", outputFile,
		"outputSize", outSize,
		"duration", duration,
	)

	return GetMergeProgress(), nil
}

// HTTP Handlers for Merger Feature

func HandleStartMergeBackups(c echo.Context) error {
	var opts MergeOptions
	opts.IncludeMedia = true
	opts.NormalizeSchema = true

	if err := c.Bind(&opts); err != nil {
		return c.JSON(http.StatusBadRequest, map[string]interface{}{
			"success": false,
			"error":   "Invalid request format: " + err.Error(),
		})
	}

	if strings.TrimSpace(opts.SourceFolder) == "" {
		return c.JSON(http.StatusBadRequest, map[string]interface{}{
			"success": false,
			"error":   "source_folder is required",
		})
	}

	if _, err := os.Stat(opts.SourceFolder); os.IsNotExist(err) {
		return c.JSON(http.StatusBadRequest, map[string]interface{}{
			"success": false,
			"error":   "Source folder does not exist: " + opts.SourceFolder,
		})
	}

	go func() {
		_, err := MergeBackupsToSingleXML(opts)
		if err != nil {
			updateMergeProgress(func(p *MergeProgress) {
				p.Status = "error"
				p.ErrorMessage = err.Error()
			})
		}
	}()

	return c.JSON(http.StatusOK, map[string]interface{}{
		"success": true,
		"message": "Merge started in background",
	})
}

func HandleGetMergeProgress(c echo.Context) error {
	p := GetMergeProgress()
	return c.JSON(http.StatusOK, p)
}

func HandleBrowseMergeFolder(c echo.Context) error {
	if runtime.GOOS != "windows" {
		return c.JSON(http.StatusOK, map[string]interface{}{"path": ""})
	}

	script := `
Add-Type -AssemblyName System.Windows.Forms
$dialog = New-Object System.Windows.Forms.FolderBrowserDialog
$dialog.Description = "Select Folder Containing XML and ZIP Backups to Merge"
$dialog.ShowNewFolderButton = $false
if ($dialog.ShowDialog() -eq [System.Windows.Forms.DialogResult]::OK) {
    [Console]::OutputEncoding = [System.Text.Encoding]::UTF8
    Write-Output $dialog.SelectedPath
}
`
	cmd := exec.Command("powershell", "-NoProfile", "-Sta", "-Command", script)
	out, err := cmd.Output()
	if err != nil {
		return c.JSON(http.StatusOK, map[string]interface{}{"path": "", "error": err.Error()})
	}

	path := strings.TrimSpace(string(out))
	return c.JSON(http.StatusOK, map[string]interface{}{"path": path})
}

func HandleBrowseMergeSaveFile(c echo.Context) error {
	if runtime.GOOS != "windows" {
		return c.JSON(http.StatusOK, map[string]interface{}{"path": ""})
	}

	script := `
Add-Type -AssemblyName System.Windows.Forms
$dialog = New-Object System.Windows.Forms.SaveFileDialog
$dialog.Filter = "SMS Backup XML (*.xml)|*.xml|All Files (*.*)|*.*"
$dialog.Title = "Select Destination for Merged XML Backup"
$dialog.FileName = "merged-sms-backup.xml"
if ($dialog.ShowDialog() -eq [System.Windows.Forms.DialogResult]::OK) {
    [Console]::OutputEncoding = [System.Text.Encoding]::UTF8
    Write-Output $dialog.FileName
}
`
	cmd := exec.Command("powershell", "-NoProfile", "-Sta", "-Command", script)
	out, err := cmd.Output()
	if err != nil {
		return c.JSON(http.StatusOK, map[string]interface{}{"path": "", "error": err.Error()})
	}

	path := strings.TrimSpace(string(out))
	return c.JSON(http.StatusOK, map[string]interface{}{"path": path})
}

func HandleOpenMergedFileFolder(c echo.Context) error {
	var req struct {
		Path string `json:"path"`
	}
	_ = c.Bind(&req)
	target := req.Path
	if target == "" {
		target = GetMergeProgress().OutputFile
	}
	if target == "" {
		return c.JSON(http.StatusBadRequest, map[string]interface{}{"error": "No output file path specified"})
	}

	dir := filepath.Dir(target)
	var cmd *exec.Cmd
	if runtime.GOOS == "windows" {
		cmd = exec.Command("explorer.exe", fmt.Sprintf("/select,%s", target))
	} else if runtime.GOOS == "darwin" {
		cmd = exec.Command("open", "-R", target)
	} else {
		cmd = exec.Command("xdg-open", dir)
	}

	_ = cmd.Start()
	return c.JSON(http.StatusOK, map[string]interface{}{"success": true, "path": target})
}
