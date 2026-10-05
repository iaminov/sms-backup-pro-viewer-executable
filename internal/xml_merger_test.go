package internal

import (
	"archive/zip"
	"bytes"
	"encoding/base64"
	"encoding/xml"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func createTestZip(t *testing.T, zipPath string, xmlFilename string, xmlContent string) {
	f, err := os.Create(zipPath)
	if err != nil {
		t.Fatalf("Failed to create zip: %v", err)
	}
	defer f.Close()

	zw := zip.NewWriter(f)
	w, err := zw.Create(xmlFilename)
	if err != nil {
		t.Fatalf("Failed to create zip entry: %v", err)
	}
	if _, err := io.WriteString(w, xmlContent); err != nil {
		t.Fatalf("Failed to write to zip entry: %v", err)
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("Failed to close zip writer: %v", err)
	}
}

func TestMergeBackups(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "sbv_test_merge_*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	subDir1 := filepath.Join(tempDir, "folder1")
	subDir2 := filepath.Join(tempDir, "folder2", "nested")
	os.MkdirAll(subDir1, 0755)
	os.MkdirAll(subDir2, 0755)

	fakeImg := base64.StdEncoding.EncodeToString([]byte("fake-jpeg-data"))

	// File 1 (XML in subfolder 1):
	// Contains:
	// - SMS 1 (date: 1000): "Hello from 2020"
	// - SMS 2 (date: 3000): "Duplicate SMS"
	// - MMS 1 (date: 2000): "Picture msg" with image
	xml1 := fmt.Sprintf(`<?xml version='1.0' encoding='UTF-8' standalone='yes' ?>
<smses count="3">
  <sms protocol="0" address="+15551234567" date="1000" type="1" subject="null" body="Hello from 2020" toa="null" sc_toa="null" service_center="null" read="1" status="-1" locked="0" />
  <sms protocol="0" address="+15551234567" date="3000" type="1" subject="null" body="Duplicate SMS" toa="null" sc_toa="null" service_center="null" read="1" status="-1" locked="0" />
  <mms date="2000" m_id="mms-123" address="+15559876543" contact_name="Bob" ct_t="application/vnd.wap.mms-message">
    <parts>
      <part ct="image/jpeg" name="pic.jpg" data="%s" />
      <part ct="text/plain" text="Picture msg" />
    </parts>
  </mms>
</smses>`, fakeImg)

	xmlPath1 := filepath.Join(subDir1, "sms-20200101000000.xml")
	if err := os.WriteFile(xmlPath1, []byte(xml1), 0644); err != nil {
		t.Fatalf("Failed to write xml1: %v", err)
	}

	// File 2 (ZIP containing XML in subfolder 2):
	// Contains:
	// - SMS 2 (date: 3000): "Duplicate SMS" (Exact duplicate of SMS 2 in File 1)
	// - SMS 3 (date: 500): Earlier message! (Should be sorted first)
	// - MMS 1 (date: 2000): Duplicate of MMS 1
	// - MMS 2 (date: 4000): Later MMS
	xml2 := fmt.Sprintf(`<?xml version='1.0' encoding='UTF-8' standalone='yes' ?>
<smses count="4">
  <sms protocol="0" address="+15551234567" date="500" type="1" subject="null" body="Earliest SMS" toa="null" sc_toa="null" service_center="null" read="1" status="-1" locked="0" />
  <sms protocol="0" address="+15551234567" date="3000" type="1" subject="null" body="Duplicate SMS" toa="null" sc_toa="null" service_center="null" read="1" status="-1" locked="0" />
  <mms date="2000" m_id="mms-123" address="+15559876543" contact_name="(Unknown)" ct_t="application/vnd.wap.mms-message">
    <parts>
      <part ct="image/jpeg" name="pic.jpg" data="%s" />
      <part ct="text/plain" text="Picture msg" />
    </parts>
  </mms>
  <mms date="4000" m_id="mms-456" address="+15559876543" contact_name="Bob" ct_t="application/vnd.wap.mms-message">
    <parts>
      <part ct="text/plain" text="Latest MMS" />
    </parts>
  </mms>
</smses>`, fakeImg)

	zipPath2 := filepath.Join(subDir2, "sms-20210101000000.zip")
	createTestZip(t, zipPath2, "sms-20210101000000.xml", xml2)

	// Also add an ignored file to test recursion filtering
	os.WriteFile(filepath.Join(tempDir, "notes.txt"), []byte("not an xml or zip"), 0644)

	// Discover files test
	files, err := DiscoverBackupFiles(tempDir)
	if err != nil {
		t.Fatalf("DiscoverBackupFiles failed: %v", err)
	}
	if len(files) != 2 {
		t.Fatalf("Expected 2 backup files, got %d: %v", len(files), files)
	}

	// Output path
	outputPath := filepath.Join(tempDir, "merged_output.xml")

	// Run Merge with IncludeMedia: false
	opts := MergeOptions{
		SourceFolder:    tempDir,
		OutputFile:      outputPath,
		IncludeMedia:    false,
		NormalizeSchema: true,
	}

	progress, err := MergeBackupsToSingleXML(opts)
	if err != nil {
		t.Fatalf("MergeBackupsToSingleXML failed: %v", err)
	}

	// Total messages in files: 3 + 4 = 7
	// Unique expected:
	// 1) SMS date=500 ("Earliest SMS")
	// 2) SMS date=1000 ("Hello from 2020")
	// 3) MMS date=2000 ("Picture msg") -> Should keep File 1's version because contact_name="Bob" is richer than "(Unknown)"
	// 4) SMS date=3000 ("Duplicate SMS")
	// 5) MMS date=4000 ("Latest MMS")
	// Unique count = 5. Duplicates removed = 2.
	if progress.TotalFoundMessages != 7 {
		t.Errorf("Expected 7 total found messages, got %d", progress.TotalFoundMessages)
	}
	if progress.UniqueMessages != 5 {
		t.Errorf("Expected 5 unique messages, got %d", progress.UniqueMessages)
	}
	if progress.DuplicatesRemoved != 2 {
		t.Errorf("Expected 2 duplicates removed, got %d", progress.DuplicatesRemoved)
	}

	// Verify merged XML content
	mergedBytes, err := os.ReadFile(outputPath)
	if err != nil {
		t.Fatalf("Failed to read merged file: %v", err)
	}

	mergedContent := string(mergedBytes)

	// Verify root tag
	if !strings.Contains(mergedContent, `<smses count="5"`) {
		t.Errorf("Expected <smses count=\"5\" in output XML, got:\n%s", mergedContent)
	}

	// Verify media was stripped
	if strings.Contains(mergedContent, fakeImg) {
		t.Errorf("Expected base64 image data to be stripped because IncludeMedia=false, but found it!")
	}
	if !strings.Contains(mergedContent, `data="null"`) {
		t.Errorf("Expected data=\"null\" on stripped part, but got:\n%s", mergedContent)
	}

	// Parse elements sequentially to verify strict chronological ordering
	decoder := xml.NewDecoder(bytes.NewReader(mergedBytes))
	var chronologicalDates []int64

	for {
		tok, err := decoder.Token()
		if err != nil {
			break
		}
		if se, ok := tok.(xml.StartElement); ok {
			switch se.Name.Local {
			case "sms":
				var s SMSEntry
				if err := decoder.DecodeElement(&s, &se); err == nil {
					d, _ := strconv.ParseInt(s.Date, 10, 64)
					chronologicalDates = append(chronologicalDates, d)
				}
			case "mms":
				var m MMSEntry
				if err := decoder.DecodeElement(&m, &se); err == nil {
					d, _ := strconv.ParseInt(m.Date, 10, 64)
					chronologicalDates = append(chronologicalDates, d)
					// Check that Bob was preserved over (Unknown)
					if m.Date == "2000" && m.ContactName != "Bob" {
						t.Errorf("Expected contact name 'Bob' to be preserved, got '%s'", m.ContactName)
					}
				}
			}
		}
	}

	expectedDates := []int64{500, 1000, 2000, 3000, 4000}
	if len(chronologicalDates) != len(expectedDates) {
		t.Fatalf("Expected %d chronological items, got %d (%v)", len(expectedDates), len(chronologicalDates), chronologicalDates)
	}
	for i, exp := range expectedDates {
		if chronologicalDates[i] != exp {
			t.Errorf("Item %d: expected date %d, got %d", i, exp, chronologicalDates[i])
		}
	}
}

func TestMergeWithSignalBackup(t *testing.T) {
	sigPath := "../signal-2022-11-10-02-00-00.backup"
	if _, err := os.Stat(sigPath); os.IsNotExist(err) {
		t.Skip("Signal backup file not found, skipping test")
	}

	tempDir, err := os.MkdirTemp("", "sbv_test_merge_sig_*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	// Create an XML backup in subfolder
	xmlContent := `<?xml version='1.0' encoding='UTF-8' standalone='yes' ?>
<smses count="2">
  <sms protocol="0" address="+15551234567" date="1000000000" type="1" subject="null" body="Ancient SMS" toa="null" sc_toa="null" service_center="null" read="1" status="-1" locked="0" />
  <sms protocol="0" address="+19175151180" date="1647708969291" type="2" subject="null" body="yo what's good" toa="null" sc_toa="null" service_center="null" read="1" status="-1" locked="0" />
</smses>`
	xmlPath := filepath.Join(tempDir, "sms-backup.xml")
	if err := os.WriteFile(xmlPath, []byte(xmlContent), 0644); err != nil {
		t.Fatalf("Failed to write test xml: %v", err)
	}

	// Copy Signal backup into tempDir
	destSig := filepath.Join(tempDir, "signal.backup")
	inF, err := os.Open(sigPath)
	if err != nil {
		t.Fatalf("Failed to open source signal backup: %v", err)
	}
	defer inF.Close()
	outF, err := os.Create(destSig)
	if err != nil {
		t.Fatalf("Failed to create dest signal backup: %v", err)
	}
	io.Copy(outF, inF)
	outF.Close()

	outputPath := filepath.Join(tempDir, "merged_with_signal.xml")
	opts := MergeOptions{
		SourceFolder:     tempDir,
		OutputFile:       outputPath,
		IncludeMedia:     false,
		NormalizeSchema:  true,
		SignalPassphrase: "31889 30544 62782 17192 51469 48815",
	}

	prog, err := MergeBackupsToSingleXML(opts)
	if err != nil {
		t.Fatalf("MergeBackupsToSingleXML with Signal failed: %v", err)
	}

	t.Logf("Merge with Signal result: TotalFound=%d, Unique=%d, DuplicatesRemoved=%d",
		prog.TotalFoundMessages, prog.UniqueMessages, prog.DuplicatesRemoved)

	// In Signal backup: 2866 SMS + 192 MMS = 3058 messages
	// In XML: 2 messages (one is duplicate "yo what's good" with date 1647708969291)
	// TotalFound should be 3058 + 2 = 3060
	// DuplicatesRemoved should be at least 1
	if prog.TotalFoundMessages < 3000 {
		t.Errorf("Expected >3000 total found messages, got %d", prog.TotalFoundMessages)
	}
	if prog.DuplicatesRemoved < 1 {
		t.Errorf("Expected duplicate 'yo what's good' to be removed, but DuplicatesRemoved=%d", prog.DuplicatesRemoved)
	}

	// Check output file exists and is valid XML
	outInfo, err := os.Stat(outputPath)
	if err != nil || outInfo.Size() == 0 {
		t.Fatalf("Merged output file is empty or missing: %v", err)
	}
}
