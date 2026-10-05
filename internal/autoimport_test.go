package internal

import (
	"archive/zip"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestAutoImport_ZipAndXML(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "test_autoimport_*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	testUID := fmt.Sprintf("ai_user_%d", time.Now().UnixNano())
	userDBPath := filepath.Join(tmpDir, fmt.Sprintf("sbv_%s.db", testUID))
	if err := InitUserDB(testUID, userDBPath); err != nil {
		t.Fatalf("Failed to init user db: %v", err)
	}
	userDB, err := GetUserDB(testUID, "aiuser")
	if err != nil {
		t.Fatalf("Failed to get user db: %v", err)
	}
	defer func() {
		userDB.Close()
		userDBsMutex.Lock()
		delete(userDBs, testUID)
		userDBsMutex.Unlock()
	}()

	sampleXML := `<?xml version='1.0' encoding='UTF-8' standalone='yes' ?>
<smses count="2">
  <sms protocol="0" address="+15551113333" date="1672531000000" type="1" body="AutoImport SMS" read="1" status="-1" />
  <sms protocol="0" address="+15551113333" date="1672532000000" type="2" body="AutoImport Response" read="1" status="-1" />
</smses>`

	// 1. Test parseXMLBackup
	xmlPath := filepath.Join(tmpDir, "sample.xml")
	if err := os.WriteFile(xmlPath, []byte(sampleXML), 0644); err != nil {
		t.Fatalf("Failed to write sample.xml: %v", err)
	}

	logger := &importLogger{
		file:     os.Stdout,
		userID:   testUID,
		filename: "sample.xml",
	}

	svc := NewAutoImportService(tmpDir)
	if err := svc.parseXMLBackup(userDB, xmlPath, logger); err != nil {
		t.Fatalf("parseXMLBackup failed: %v", err)
	}

	var count int
	_ = userDB.QueryRow("SELECT COUNT(*) FROM messages").Scan(&count)
	if count != 2 {
		t.Errorf("Expected 2 messages from XML auto-import, got %d", count)
	}

	// 2. Test parseZipBackup
	// Clear messages first
	_, _ = userDB.Exec("DELETE FROM messages")

	zipPath := filepath.Join(tmpDir, "sample.zip")
	zf, err := os.Create(zipPath)
	if err != nil {
		t.Fatalf("Failed to create zip: %v", err)
	}
	zw := zip.NewWriter(zf)
	w, err := zw.Create("contained_backup.xml")
	if err != nil {
		t.Fatalf("Failed to create entry in zip: %v", err)
	}
	_, _ = w.Write([]byte(sampleXML))
	zw.Close()
	zf.Close()

	if err := svc.parseZipBackup(userDB, zipPath, logger); err != nil {
		t.Fatalf("parseZipBackup failed: %v", err)
	}

	_ = userDB.QueryRow("SELECT COUNT(*) FROM messages").Scan(&count)
	if count != 2 {
		t.Errorf("Expected 2 messages from ZIP auto-import, got %d", count)
	}
}
