package internal

import (
	"encoding/base64"
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
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/labstack/echo/v4"
)

type MediaExtractProgress struct {
	TotalMMS        int       `json:"total_mms"`
	ProcessedMMS    int       `json:"processed_mms"`
	ImagesExtracted int       `json:"images_extracted"`
	VideosExtracted int       `json:"videos_extracted"`
	AudioExtracted  int       `json:"audio_extracted"`
	OtherExtracted  int       `json:"other_extracted"`
	TotalBytes      int64     `json:"total_bytes"`
	Status          string    `json:"status"` // "idle", "extracting", "completed", "error"
	ErrorMessage    string    `json:"error_message,omitempty"`
	OutputDir       string    `json:"output_dir"`
	StartTime       time.Time `json:"start_time"`
	Duration        string    `json:"duration,omitempty"`
}

type MediaExtractOptions struct {
	FilePath    string `json:"file_path"`
	OutputDir   string `json:"output_dir"`
	ConvertHeic bool   `json:"convert_heic"`
	ExtractImg  bool   `json:"extract_images"`
	ExtractVid  bool   `json:"extract_videos"`
	ExtractAud  bool   `json:"extract_audio"`
}

var (
	extractProgress     *MediaExtractProgress
	extractProgressLock sync.RWMutex
	invalidFilenameRe   = regexp.MustCompile(`[\\/:*?"<>|\r\n\t]+`)
)

func GetDefaultMediaDir() string {
	exePath, err := os.Executable()
	if err == nil {
		return filepath.Join(filepath.Dir(exePath), "media")
	}
	return "media"
}

func GetMediaExtractProgress() *MediaExtractProgress {
	extractProgressLock.RLock()
	defer extractProgressLock.RUnlock()
	if extractProgress == nil {
		return &MediaExtractProgress{
			Status:    "idle",
			OutputDir: GetDefaultMediaDir(),
		}
	}
	// Return copy
	copy := *extractProgress
	return &copy
}

func updateMediaProgress(fn func(p *MediaExtractProgress)) {
	extractProgressLock.Lock()
	defer extractProgressLock.Unlock()
	if extractProgress != nil {
		fn(extractProgress)
	}
}

func sanitizeFilename(name string) string {
	clean := invalidFilenameRe.ReplaceAllString(name, "_")
	clean = strings.TrimSpace(clean)
	if clean == "" || strings.EqualFold(clean, "null") {
		return ""
	}
	return clean
}

func getExtensionForContentType(ct string) string {
	ct = strings.ToLower(strings.TrimSpace(ct))
	if idx := strings.Index(ct, ";"); idx != -1 {
		ct = strings.TrimSpace(ct[:idx])
	}
	switch ct {
	case "image/jpeg", "image/jpg", "image/pjpeg":
		return ".jpg"
	case "image/png":
		return ".png"
	case "image/gif":
		return ".gif"
	case "image/heic", "image/heif":
		return ".heic"
	case "image/webp":
		return ".webp"
	case "image/bmp", "image/x-ms-bmp":
		return ".bmp"
	case "video/mp4":
		return ".mp4"
	case "video/3gpp", "video/3gp":
		return ".3gp"
	case "video/3gpp2", "video/3g2":
		return ".3g2"
	case "video/quicktime":
		return ".mov"
	case "video/webm":
		return ".webm"
	case "video/x-matroska":
		return ".mkv"
	case "audio/amr", "audio/amr-wb":
		return ".amr"
	case "audio/mp3", "audio/mpeg", "audio/mpg":
		return ".mp3"
	case "audio/aac", "audio/mp4", "audio/m4a":
		return ".m4a"
	case "audio/3gpp", "audio/3ga":
		return ".3ga"
	case "audio/wav", "audio/x-wav":
		return ".wav"
	case "audio/ogg", "audio/opus":
		return ".ogg"
	case "text/x-vcard", "text/vcard", "text/directory":
		return ".vcf"
	default:
		if strings.HasPrefix(ct, "image/") {
			return "." + strings.TrimPrefix(ct, "image/")
		}
		if strings.HasPrefix(ct, "video/") {
			return "." + strings.TrimPrefix(ct, "video/")
		}
		if strings.HasPrefix(ct, "audio/") {
			return "." + strings.TrimPrefix(ct, "audio/")
		}
		return ".bin"
	}
}

func getSubfolderForContentType(ct string) string {
	ct = strings.ToLower(strings.TrimSpace(ct))
	if strings.HasPrefix(ct, "image/") {
		return "image"
	}
	if strings.HasPrefix(ct, "video/") {
		return "video"
	}
	if strings.HasPrefix(ct, "audio/") {
		return "audio"
	}
	return "other"
}

func generateMediaFilename(part MMSPart, mmsDateMs int64, mmsIndex, partIndex int, ext string) string {
	name := sanitizeFilename(part.Name)
	if name == "" {
		name = sanitizeFilename(part.CL)
	}

	timePrefix := ""
	if mmsDateMs > 0 {
		t := time.Unix(mmsDateMs/1000, 0)
		timePrefix = t.Format("2006-01-02_150405")
	}

	if name != "" {
		origExt := filepath.Ext(name)
		base := strings.TrimSuffix(name, origExt)
		if timePrefix != "" {
			return fmt.Sprintf("%s_%s_%d-%d%s", timePrefix, base, mmsIndex, partIndex, ext)
		}
		return fmt.Sprintf("%s_%d-%d%s", base, mmsIndex, partIndex, ext)
	}

	if timePrefix != "" {
		return fmt.Sprintf("media_%s_%d-%d%s", timePrefix, mmsIndex, partIndex, ext)
	}
	return fmt.Sprintf("media_%d-%d%s", mmsIndex, partIndex, ext)
}

func getUniqueFilePath(dir, filename string) string {
	target := filepath.Join(dir, filename)
	if _, err := os.Stat(target); os.IsNotExist(err) {
		return target
	}

	ext := filepath.Ext(filename)
	base := strings.TrimSuffix(filename, ext)
	for i := 1; ; i++ {
		newName := fmt.Sprintf("%s_%d%s", base, i, ext)
		target = filepath.Join(dir, newName)
		if _, err := os.Stat(target); os.IsNotExist(err) {
			return target
		}
	}
}

// ExtractMediaFromXML streams an SMS/MMS XML file and extracts all media files into subfolders
func ExtractMediaFromXML(xmlReader io.Reader, opts MediaExtractOptions) (*MediaExtractProgress, error) {
	outDir := opts.OutputDir
	if strings.TrimSpace(outDir) == "" {
		outDir = GetDefaultMediaDir()
	}
	absOutDir, err := filepath.Abs(outDir)
	if err != nil {
		absOutDir = outDir
	}

	// Prepare subdirectories
	imgDir := filepath.Join(absOutDir, "image")
	vidDir := filepath.Join(absOutDir, "video")
	audDir := filepath.Join(absOutDir, "audio")
	othDir := filepath.Join(absOutDir, "other")

	for _, d := range []string{imgDir, vidDir, audDir, othDir} {
		if err := os.MkdirAll(d, 0755); err != nil {
			return nil, fmt.Errorf("failed to create directory %s: %w", d, err)
		}
	}

	extractProgressLock.Lock()
	extractProgress = &MediaExtractProgress{
		Status:    "extracting",
		OutputDir: absOutDir,
		StartTime: time.Now(),
	}
	extractProgressLock.Unlock()

	decoder := xml.NewDecoder(xmlReader)
	mmsCount := 0

	for {
		token, err := decoder.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			slog.Error("XML decoder error during media extraction", "error", err)
			break
		}

		elem, ok := token.(xml.StartElement)
		if !ok {
			continue
		}

		if elem.Name.Local == "mms" {
			mmsCount++
			var mms MMSEntry
			if err := decoder.DecodeElement(&mms, &elem); err != nil {
				slog.Error("Failed to decode MMS element", "error", err)
				continue
			}

			var dateMs int64
			if mms.Date != "" {
				dateMs, _ = strconv.ParseInt(mms.Date, 10, 64)
			}

			for partIdx, part := range mms.Parts {
				if part.Data == "" || isSMILContentType(part.ContentType) {
					continue
				}

				subfolder := getSubfolderForContentType(part.ContentType)
				if subfolder == "image" && !opts.ExtractImg {
					continue
				}
				if subfolder == "video" && !opts.ExtractVid {
					continue
				}
				if subfolder == "audio" && !opts.ExtractAud {
					continue
				}

				// Decode base64 data
				rawPayload := strings.TrimSpace(part.Data)
				data, err := base64.StdEncoding.DecodeString(rawPayload)
				if err != nil {
					data, err = base64.RawStdEncoding.DecodeString(rawPayload)
				}
				if err != nil || len(data) == 0 {
					continue
				}

				ext := getExtensionForContentType(part.ContentType)
				filename := generateMediaFilename(part, dateMs, mmsCount, partIdx+1, ext)

				var targetSubDir string
				switch subfolder {
				case "image":
					targetSubDir = imgDir
				case "video":
					targetSubDir = vidDir
				case "audio":
					targetSubDir = audDir
				default:
					targetSubDir = othDir
				}

				outPath := getUniqueFilePath(targetSubDir, filename)
				if err := os.WriteFile(outPath, data, 0644); err == nil {
					if dateMs > 0 {
						msgTime := time.Unix(dateMs/1000, 0)
						_ = os.Chtimes(outPath, msgTime, msgTime)
					}

					updateMediaProgress(func(p *MediaExtractProgress) {
						switch subfolder {
						case "image":
							p.ImagesExtracted++
						case "video":
							p.VideosExtracted++
						case "audio":
							p.AudioExtracted++
						default:
							p.OtherExtracted++
						}
						p.TotalBytes += int64(len(data))
					})
				}

				// HEIC JPEG conversion option
				if opts.ConvertHeic && isHEICContentType(part.ContentType) {
					jpgData, err := convertHEICtoJPEG(data)
					if err == nil && len(jpgData) > 0 {
						jpgName := strings.TrimSuffix(filepath.Base(outPath), filepath.Ext(outPath)) + ".jpg"
						jpgPath := getUniqueFilePath(imgDir, jpgName)
						if err := os.WriteFile(jpgPath, jpgData, 0644); err == nil {
							if dateMs > 0 {
								msgTime := time.Unix(dateMs/1000, 0)
								_ = os.Chtimes(jpgPath, msgTime, msgTime)
							}
						}
					}
				}

				// Free memory immediately
				data = nil
			}

			// Free MMS memory
			mms.Parts = nil
			mms = MMSEntry{}

			if mmsCount%50 == 0 {
				updateMediaProgress(func(p *MediaExtractProgress) {
					p.ProcessedMMS = mmsCount
				})
			}
		}
	}

	duration := time.Since(extractProgress.StartTime)
	updateMediaProgress(func(p *MediaExtractProgress) {
		p.ProcessedMMS = mmsCount
		p.Status = "completed"
		p.Duration = duration.Round(time.Millisecond).String()
	})

	slog.Info("Media extraction complete",
		"mmsCount", mmsCount,
		"images", extractProgress.ImagesExtracted,
		"videos", extractProgress.VideosExtracted,
		"audio", extractProgress.AudioExtracted,
		"duration", duration,
	)

	return GetMediaExtractProgress(), nil
}

// HandleExtractMedia starts the media extraction process
func HandleExtractMedia(c echo.Context) error {
	var opts MediaExtractOptions
	opts.ExtractImg = true
	opts.ExtractVid = true
	opts.ExtractAud = true
	opts.ConvertHeic = true

	// Check if request is multipart form (file upload) or JSON (local path)
	contentType := c.Request().Header.Get("Content-Type")

	if strings.Contains(contentType, "multipart/form-data") {
		file, header, err := c.Request().FormFile("file")
		if err != nil {
			return c.JSON(http.StatusBadRequest, map[string]interface{}{
				"success": false,
				"error":   "Failed to read uploaded XML file: " + err.Error(),
			})
		}
		defer file.Close()

		if out := c.FormValue("output_dir"); out != "" {
			opts.OutputDir = out
		}
		if conv := c.FormValue("convert_heic"); conv != "" {
			opts.ConvertHeic = conv == "true" || conv == "1"
		}
		if img := c.FormValue("extract_images"); img != "" {
			opts.ExtractImg = img == "true" || img == "1"
		}
		if vid := c.FormValue("extract_videos"); vid != "" {
			opts.ExtractVid = vid == "true" || vid == "1"
		}
		if aud := c.FormValue("extract_audio"); aud != "" {
			opts.ExtractAud = aud == "true" || aud == "1"
		}

		// Save uploaded file to temp file so extraction can run in background
		tempFile, err := os.CreateTemp("", "extract-xml-*.xml")
		if err != nil {
			return c.JSON(http.StatusInternalServerError, map[string]interface{}{
				"success": false,
				"error":   "Failed to create temp file: " + err.Error(),
			})
		}
		defer tempFile.Close()

		_, err = io.Copy(tempFile, file)
		if err != nil {
			_ = os.Remove(tempFile.Name())
			return c.JSON(http.StatusInternalServerError, map[string]interface{}{
				"success": false,
				"error":   "Failed to save uploaded XML: " + err.Error(),
			})
		}

		tempPath := tempFile.Name()
		go func() {
			defer os.Remove(tempPath)
			f, err := os.Open(tempPath)
			if err != nil {
				updateMediaProgress(func(p *MediaExtractProgress) {
					p.Status = "error"
					p.ErrorMessage = err.Error()
				})
				return
			}
			defer f.Close()
			_, _ = ExtractMediaFromXML(f, opts)
		}()

		return c.JSON(http.StatusOK, map[string]interface{}{
			"success":  true,
			"filename": header.Filename,
			"message":  "Media extraction started",
		})
	}

	// JSON request with file path on disk
	if err := c.Bind(&opts); err != nil {
		return c.JSON(http.StatusBadRequest, map[string]interface{}{
			"success": false,
			"error":   "Invalid request format: " + err.Error(),
		})
	}

	if opts.FilePath == "" {
		return c.JSON(http.StatusBadRequest, map[string]interface{}{
			"success": false,
			"error":   "file_path is required",
		})
	}

	// Verify file exists
	if _, err := os.Stat(opts.FilePath); os.IsNotExist(err) {
		return c.JSON(http.StatusBadRequest, map[string]interface{}{
			"success": false,
			"error":   "Specified XML file does not exist on disk: " + opts.FilePath,
		})
	}

	filePath := opts.FilePath
	go func() {
		f, err := os.Open(filePath)
		if err != nil {
			updateMediaProgress(func(p *MediaExtractProgress) {
				p.Status = "error"
				p.ErrorMessage = err.Error()
			})
			return
		}
		defer f.Close()
		_, _ = ExtractMediaFromXML(f, opts)
	}()

	return c.JSON(http.StatusOK, map[string]interface{}{
		"success":   true,
		"file_path": filePath,
		"message":   "Media extraction started",
	})
}

// HandleExtractMediaProgress returns current media extraction progress
func HandleExtractMediaProgress(c echo.Context) error {
	progress := GetMediaExtractProgress()
	return c.JSON(http.StatusOK, progress)
}

// HandleOpenMediaFolder opens the media directory in Windows Explorer / OS file manager
func HandleOpenMediaFolder(c echo.Context) error {
	var req struct {
		Path string `json:"path"`
	}
	_ = c.Bind(&req)
	targetDir := req.Path
	if targetDir == "" {
		targetDir = GetDefaultMediaDir()
	}

	absDir, err := filepath.Abs(targetDir)
	if err == nil {
		targetDir = absDir
	}
	_ = os.MkdirAll(targetDir, 0755)

	var cmd *exec.Cmd
	if runtime.GOOS == "windows" {
		cmd = exec.Command("explorer.exe", targetDir)
	} else if runtime.GOOS == "darwin" {
		cmd = exec.Command("open", targetDir)
	} else {
		cmd = exec.Command("xdg-open", targetDir)
	}

	if err := cmd.Start(); err != nil {
		slog.Error("Failed to open media directory", "dir", targetDir, "error", err)
		return c.JSON(http.StatusInternalServerError, map[string]interface{}{
			"success": false,
			"error":   "Failed to open folder: " + err.Error(),
		})
	}

	return c.JSON(http.StatusOK, map[string]interface{}{
		"success": true,
		"path":    targetDir,
	})
}
