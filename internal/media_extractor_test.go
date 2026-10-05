package internal

import (
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestExtractMediaFromXML(t *testing.T) {
	// Sample data
	fakeImageData := []byte("fake-jpeg-binary-image-data-here")
	fakeVideoData := []byte("fake-mp4-video-data-here")
	fakeAudioData := []byte("fake-amr-audio-data-here")

	b64Img := base64.StdEncoding.EncodeToString(fakeImageData)
	b64Vid := base64.StdEncoding.EncodeToString(fakeVideoData)
	b64Aud := base64.StdEncoding.EncodeToString(fakeAudioData)

	xmlContent := fmt.Sprintf(`<?xml version='1.0' encoding='UTF-8' standalone='yes' ?>
<smses count="2">
  <mms date="1672531199000" address="+15551234567" ct_t="application/vnd.wap.mms-message">
    <parts>
      <part ct="application/smil" text="&lt;smil&gt;&lt;/smil&gt;" />
      <part ct="image/jpeg" name="vacation.jpg" data="%s" />
      <part ct="video/mp4" name="clip.mp4" data="%s" />
    </parts>
  </mms>
  <mms date="1672531200000" address="+15559876543" ct_t="application/vnd.wap.mms-message">
    <parts>
      <part ct="audio/amr" name="voicenote.amr" data="%s" />
    </parts>
  </mms>
</smses>`, b64Img, b64Vid, b64Aud)

	tmpDir, err := os.MkdirTemp("", "test_media_extract_*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	opts := MediaExtractOptions{
		OutputDir:   tmpDir,
		ConvertHeic: false,
		ExtractImg:  true,
		ExtractVid:  true,
		ExtractAud:  true,
	}

	progress, err := ExtractMediaFromXML(strings.NewReader(xmlContent), opts)
	if err != nil {
		t.Fatalf("ExtractMediaFromXML failed: %v", err)
	}

	if progress.ImagesExtracted != 1 {
		t.Errorf("Expected 1 image extracted, got %d", progress.ImagesExtracted)
	}
	if progress.VideosExtracted != 1 {
		t.Errorf("Expected 1 video extracted, got %d", progress.VideosExtracted)
	}
	if progress.AudioExtracted != 1 {
		t.Errorf("Expected 1 audio extracted, got %d", progress.AudioExtracted)
	}

	// Verify image file exists
	imgFiles, err := os.ReadDir(filepath.Join(tmpDir, "image"))
	if err != nil || len(imgFiles) != 1 {
		t.Fatalf("Expected 1 file in image directory, got %d (err: %v)", len(imgFiles), err)
	}
	t.Logf("Extracted image: %s", imgFiles[0].Name())

	// Verify video file exists
	vidFiles, err := os.ReadDir(filepath.Join(tmpDir, "video"))
	if err != nil || len(vidFiles) != 1 {
		t.Fatalf("Expected 1 file in video directory, got %d (err: %v)", len(vidFiles), err)
	}
	t.Logf("Extracted video: %s", vidFiles[0].Name())

	// Verify audio file exists
	audFiles, err := os.ReadDir(filepath.Join(tmpDir, "audio"))
	if err != nil || len(audFiles) != 1 {
		t.Fatalf("Expected 1 file in audio directory, got %d (err: %v)", len(audFiles), err)
	}
	t.Logf("Extracted audio: %s", audFiles[0].Name())

	// Check content of extracted image
	extractedImgBytes, err := os.ReadFile(filepath.Join(tmpDir, "image", imgFiles[0].Name()))
	if err != nil {
		t.Fatalf("Failed to read extracted image: %v", err)
	}
	if string(extractedImgBytes) != string(fakeImageData) {
		t.Errorf("Extracted image content mismatch")
	}
}
