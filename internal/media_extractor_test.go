package internal

import (
	"archive/zip"
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestExtractMediaFromXML(t *testing.T) {
	fakeImageData := []byte("fake-jpeg-binary-image-data-here")
	fakeVideoData := []byte("fake-mp4-video-data-here")
	fakeAudioData := []byte("fake-amr-audio-data-here")

	b64Img := base64.StdEncoding.EncodeToString(fakeImageData)
	b64Vid := base64.StdEncoding.EncodeToString(fakeVideoData)
	b64Aud := base64.StdEncoding.EncodeToString(fakeAudioData)

	xmlContent := fmt.Sprintf(`<?xml version='1.0' encoding='UTF-8' standalone='yes' ?>
<smses count="2">
  <mms date="1672531199000" address="+15551234567" contact_name="Alice" ct_t="application/vnd.wap.mms-message">
    <parts>
      <part ct="application/smil" text="&lt;smil&gt;&lt;/smil&gt;" />
      <part ct="image/jpeg" name="vacation.jpg" data="%s" />
      <part ct="video/mp4" name="clip.mp4" data="%s" />
    </parts>
  </mms>
  <mms date="1672531200000" address="+15559876543" contact_name="Bob" ct_t="application/vnd.wap.mms-message">
    <parts>
      <part ct="audio/amr" name="voicenote.amr" data="%s" />
    </parts>
  </mms>
</smses>`, b64Img, b64Vid, b64Aud)

	// Test 1: Flat organization (GroupByConversation = false)
	{
		tmpDir, err := os.MkdirTemp("", "test_media_extract_flat_*")
		if err != nil {
			t.Fatalf("Failed to create temp dir: %v", err)
		}
		defer os.RemoveAll(tmpDir)

		opts := MediaExtractOptions{
			OutputDir:           tmpDir,
			ConvertHeic:         false,
			ExtractImg:          true,
			ExtractVid:          true,
			ExtractAud:          true,
			GroupByConversation: false,
		}

		progress, err := ExtractMediaFromXML(strings.NewReader(xmlContent), opts, int64(len(xmlContent)))
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
		if progress.Percent != 100 {
			t.Errorf("Expected 100 percent, got %d", progress.Percent)
		}

		// Verify files exist in flat category directories
		imgFiles, err := os.ReadDir(filepath.Join(tmpDir, "image"))
		if err != nil || len(imgFiles) != 1 {
			t.Fatalf("Expected 1 file in image directory, got %d (err: %v)", len(imgFiles), err)
		}
		vidFiles, err := os.ReadDir(filepath.Join(tmpDir, "video"))
		if err != nil || len(vidFiles) != 1 {
			t.Fatalf("Expected 1 file in video directory, got %d (err: %v)", len(vidFiles), err)
		}
		audFiles, err := os.ReadDir(filepath.Join(tmpDir, "audio"))
		if err != nil || len(audFiles) != 1 {
			t.Fatalf("Expected 1 file in audio directory, got %d (err: %v)", len(audFiles), err)
		}
	}

	// Test 2: Conversation grouping (GroupByConversation = true)
	{
		tmpDir, err := os.MkdirTemp("", "test_media_extract_conv_*")
		if err != nil {
			t.Fatalf("Failed to create temp dir: %v", err)
		}
		defer os.RemoveAll(tmpDir)

		opts := MediaExtractOptions{
			OutputDir:           tmpDir,
			ConvertHeic:         false,
			ExtractImg:          true,
			ExtractVid:          true,
			ExtractAud:          true,
			GroupByConversation: true,
		}

		progress, err := ExtractMediaFromXML(strings.NewReader(xmlContent), opts, int64(len(xmlContent)))
		if err != nil {
			t.Fatalf("ExtractMediaFromXML failed: %v", err)
		}

		if progress.ImagesExtracted != 1 || progress.VideosExtracted != 1 || progress.AudioExtracted != 1 {
			t.Errorf("Expected 1 img, 1 vid, 1 aud; got %d, %d, %d",
				progress.ImagesExtracted, progress.VideosExtracted, progress.AudioExtracted)
		}

		// Verify Alice's conversation folder has image and video subfolders
		aliceImg, err := os.ReadDir(filepath.Join(tmpDir, "Alice", "image"))
		if err != nil || len(aliceImg) != 1 {
			t.Fatalf("Expected 1 image in Alice/image, got %d (err: %v)", len(aliceImg), err)
		}
		aliceVid, err := os.ReadDir(filepath.Join(tmpDir, "Alice", "video"))
		if err != nil || len(aliceVid) != 1 {
			t.Fatalf("Expected 1 video in Alice/video, got %d (err: %v)", len(aliceVid), err)
		}

		// Verify Bob's conversation folder has audio subfolder
		bobAud, err := os.ReadDir(filepath.Join(tmpDir, "Bob", "audio"))
		if err != nil || len(bobAud) != 1 {
			t.Fatalf("Expected 1 audio in Bob/audio, got %d (err: %v)", len(bobAud), err)
		}
	}
}

func TestExtractMediaFromFile_XML_And_Zip(t *testing.T) {
	fakeImageData := []byte("image-binary-payload-data")
	b64Img := base64.StdEncoding.EncodeToString(fakeImageData)

	xmlContent := fmt.Sprintf(`<?xml version='1.0' encoding='UTF-8' standalone='yes' ?>
<smses count="1">
  <mms date="1672531199000" address="+15551234567" contact_name="Alice" ct_t="application/vnd.wap.mms-message">
    <parts>
      <part ct="image/jpeg" name="pic.jpg" data="%s" />
    </parts>
  </mms>
</smses>`, b64Img)

	tmpWorkingDir, err := os.MkdirTemp("", "test_extract_file_*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpWorkingDir)

	xmlPath := filepath.Join(tmpWorkingDir, "backup.xml")
	if err := os.WriteFile(xmlPath, []byte(xmlContent), 0644); err != nil {
		t.Fatalf("Failed to write xml file: %v", err)
	}

	zipPath := filepath.Join(tmpWorkingDir, "backup.zip")
	zipF, err := os.Create(zipPath)
	if err != nil {
		t.Fatalf("Failed to create zip file: %v", err)
	}
	zw := zip.NewWriter(zipF)
	w, err := zw.Create("backup.xml")
	if err != nil {
		t.Fatalf("Failed to create entry in zip: %v", err)
	}
	if _, err := w.Write([]byte(xmlContent)); err != nil {
		t.Fatalf("Failed to write entry in zip: %v", err)
	}
	zw.Close()
	zipF.Close()

	// 1. Test ExtractMediaFromFile on plain .xml
	{
		outDir := filepath.Join(tmpWorkingDir, "out_xml")
		opts := MediaExtractOptions{
			OutputDir:           outDir,
			ExtractImg:          true,
			GroupByConversation: false,
		}
		prog, err := ExtractMediaFromFile(xmlPath, opts)
		if err != nil {
			t.Fatalf("ExtractMediaFromFile(xml) failed: %v", err)
		}
		if prog.ImagesExtracted != 1 {
			t.Errorf("Expected 1 image extracted from xml, got %d", prog.ImagesExtracted)
		}
		files, _ := os.ReadDir(filepath.Join(outDir, "image"))
		if len(files) != 1 {
			t.Errorf("Expected 1 image file in %s/image, got %d", outDir, len(files))
		}
	}

	// 2. Test ExtractMediaFromFile on .zip
	{
		outDir := filepath.Join(tmpWorkingDir, "out_zip")
		opts := MediaExtractOptions{
			OutputDir:           outDir,
			ExtractImg:          true,
			GroupByConversation: true,
		}
		prog, err := ExtractMediaFromFile(zipPath, opts)
		if err != nil {
			t.Fatalf("ExtractMediaFromFile(zip) failed: %v", err)
		}
		if prog.ImagesExtracted != 1 {
			t.Errorf("Expected 1 image extracted from zip, got %d", prog.ImagesExtracted)
		}
		files, _ := os.ReadDir(filepath.Join(outDir, "Alice", "image"))
		if len(files) != 1 {
			t.Errorf("Expected 1 image file in %s/Alice/image, got %d", outDir, len(files))
		}
	}
}
