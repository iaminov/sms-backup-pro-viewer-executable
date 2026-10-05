package internal

import (
	"database/sql"
	"os"
	"path/filepath"
	"testing"
)

func TestExtractGoogleVoiceNumber(t *testing.T) {
	sampleDir := filepath.Join("..", "google-voice-data")
	if _, err := os.Stat(sampleDir); os.IsNotExist(err) {
		t.Skip("google-voice-data folder not present, skipping test")
	}

	gvPhone, cellPhones, err := ExtractGoogleVoiceNumber(sampleDir)
	if err != nil {
		t.Fatalf("ExtractGoogleVoiceNumber failed: %v", err)
	}

	if gvPhone != "+16465043037" {
		t.Errorf("Expected GV phone +16465043037, got %s", gvPhone)
	}
	if len(cellPhones) == 0 || cellPhones[0] != "+16462447741" {
		t.Errorf("Expected linked cell +16462447741, got %v", cellPhones)
	}
}

func TestParseGoogleVoiceTextAndMMS(t *testing.T) {
	sampleDir := filepath.Join("..", "google-voice-data", "Voice", "Calls")
	if _, err := os.Stat(sampleDir); os.IsNotExist(err) {
		t.Skip("google-voice-data/Voice/Calls not present, skipping test")
	}

	// 1. Test 1-on-1 SMS text
	smsFile := filepath.Join(sampleDir, "+12014411841 - Text - 2025-10-13T17_12_47Z.html")
	smsCount := 0
	err := DecodeGoogleVoiceHTMLFile(
		smsFile,
		true,
		"+16465043037",
		func(sms *SMSEntry, dateMs int64) error {
			smsCount++
			if sms.Address != "+12014411841" {
				t.Errorf("Expected address +12014411841, got %s", sms.Address)
			}
			if sms.Type != "1" {
				t.Errorf("Expected type 1 (incoming), got %s", sms.Type)
			}
			if sms.Account != "+16465043037" {
				t.Errorf("Expected account +16465043037, got %s", sms.Account)
			}
			return nil
		},
		func(mms *MMSEntry, dateMs int64) error {
			t.Errorf("Unexpected MMS in text file")
			return nil
		},
		func(call *CallEntry, dateMs int64) error {
			t.Errorf("Unexpected Call in text file")
			return nil
		},
	)
	if err != nil {
		t.Fatalf("DecodeGoogleVoiceHTMLFile failed on SMS: %v", err)
	}
	if smsCount != 1 {
		t.Errorf("Expected 1 SMS, got %d", smsCount)
	}

	// 2. Test MMS with Image attachment
	mmsFile := filepath.Join(sampleDir, "+12018955491 - Text - 2022-10-08T11_00_20Z.html")
	foundMediaMMS := false
	err = DecodeGoogleVoiceHTMLFile(
		mmsFile,
		true,
		"+16465043037",
		func(sms *SMSEntry, dateMs int64) error {
			return nil
		},
		func(mms *MMSEntry, dateMs int64) error {
			for _, p := range mms.Parts {
				if p.ContentType == "image/jpeg" && p.Data != "null" && len(p.Data) > 100 {
					foundMediaMMS = true
				}
			}
			return nil
		},
		func(call *CallEntry, dateMs int64) error {
			return nil
		},
	)
	if err != nil {
		t.Fatalf("DecodeGoogleVoiceHTMLFile failed on MMS: %v", err)
	}
	if !foundMediaMMS {
		t.Errorf("Expected to find MMS with image/jpeg data in %s", mmsFile)
	}

	// 3. Test Group Conversation
	groupFile := filepath.Join(sampleDir, "Group Conversation - 2022-09-30T19_19_50Z.html")
	groupMsgCount := 0
	err = DecodeGoogleVoiceHTMLFile(
		groupFile,
		true,
		"+16465043037",
		func(sms *SMSEntry, dateMs int64) error {
			t.Errorf("Unexpected SMS in group file")
			return nil
		},
		func(mms *MMSEntry, dateMs int64) error {
			groupMsgCount++
			if len(mms.Addrs) < 2 {
				t.Errorf("Expected multiple addrs in group MMS, got %d", len(mms.Addrs))
			}
			return nil
		},
		func(call *CallEntry, dateMs int64) error {
			return nil
		},
	)
	if err != nil {
		t.Fatalf("DecodeGoogleVoiceHTMLFile failed on group: %v", err)
	}
	if groupMsgCount != 2 {
		t.Errorf("Expected 2 group MMS messages, got %d", groupMsgCount)
	}
}

func TestParseGoogleVoiceCallsAndVoicemail(t *testing.T) {
	sampleDir := filepath.Join("..", "google-voice-data", "Voice", "Calls")
	if _, err := os.Stat(sampleDir); os.IsNotExist(err) {
		t.Skip("google-voice-data/Voice/Calls not present, skipping test")
	}

	// 1. Received Call
	callFile := filepath.Join(sampleDir, "+12012434317 - Received - 2025-02-18T18_16_21Z.html")
	callCount := 0
	err := DecodeGoogleVoiceHTMLFile(
		callFile,
		true,
		"+16465043037",
		func(sms *SMSEntry, dateMs int64) error { return nil },
		func(mms *MMSEntry, dateMs int64) error { return nil },
		func(call *CallEntry, dateMs int64) error {
			callCount++
			if call.Number != "+12012434317" {
				t.Errorf("Expected number +12012434317, got %s", call.Number)
			}
			if call.Type != "1" {
				t.Errorf("Expected type 1 (received), got %s", call.Type)
			}
			if call.Duration != "3" {
				t.Errorf("Expected duration 3, got %s", call.Duration)
			}
			return nil
		},
	)
	if err != nil {
		t.Fatalf("DecodeGoogleVoiceHTMLFile failed on Received call: %v", err)
	}
	if callCount != 1 {
		t.Errorf("Expected 1 call, got %d", callCount)
	}

	// 2. Missed Call
	missedFile := filepath.Join(sampleDir, "+12014411841 - Missed - 2025-10-13T17_12_01Z.html")
	callCount = 0
	err = DecodeGoogleVoiceHTMLFile(
		missedFile,
		true,
		"+16465043037",
		func(sms *SMSEntry, dateMs int64) error { return nil },
		func(mms *MMSEntry, dateMs int64) error { return nil },
		func(call *CallEntry, dateMs int64) error {
			callCount++
			if call.Type != "3" {
				t.Errorf("Expected type 3 (missed), got %s", call.Type)
			}
			return nil
		},
	)
	if err != nil {
		t.Fatalf("DecodeGoogleVoiceHTMLFile failed on Missed call: %v", err)
	}
	if callCount != 1 {
		t.Errorf("Expected 1 call, got %d", callCount)
	}

	// 3. Voicemail
	vmFile := filepath.Join(sampleDir, "+12014411841 - Voicemail - 2026-02-10T21_37_07Z.html")
	callCount = 0
	err = DecodeGoogleVoiceHTMLFile(
		vmFile,
		true,
		"+16465043037",
		func(sms *SMSEntry, dateMs int64) error { return nil },
		func(mms *MMSEntry, dateMs int64) error { return nil },
		func(call *CallEntry, dateMs int64) error {
			callCount++
			if call.Type != "4" {
				t.Errorf("Expected type 4 (voicemail), got %s", call.Type)
			}
			if call.Duration != "29" {
				t.Errorf("Expected duration 29, got %s", call.Duration)
			}
			return nil
		},
	)
	if err != nil {
		t.Fatalf("DecodeGoogleVoiceHTMLFile failed on Voicemail: %v", err)
	}
	if callCount != 1 {
		t.Errorf("Expected 1 call, got %d", callCount)
	}
}

func TestMergeGoogleVoiceBackups(t *testing.T) {
	sampleDir := filepath.Join("..", "google-voice-data")
	if _, err := os.Stat(sampleDir); os.IsNotExist(err) {
		t.Skip("google-voice-data folder not present, skipping test")
	}

	// Create a subset folder to test merge speed and end-to-end correctness
	tmpDir, err := os.MkdirTemp("", "sbv_test_gv_merge_*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	voiceDir := filepath.Join(tmpDir, "Voice")
	callsDir := filepath.Join(voiceDir, "Calls")
	_ = os.MkdirAll(callsDir, 0755)

	// Copy Phones.vcf
	phonesSrc := filepath.Join(sampleDir, "Voice", "Phones.vcf")
	if b, err := os.ReadFile(phonesSrc); err == nil {
		_ = os.WriteFile(filepath.Join(voiceDir, "Phones.vcf"), b, 0644)
	}

	// Copy a representative selection of HTML and media files
	filesToCopy := []string{
		"+12014411841 - Text - 2025-10-13T17_12_47Z.html",
		"+12018955491 - Text - 2022-10-08T11_00_20Z.html",
		"+12018955491 - Text - 2022-10-08T11_00_20Z-4-1.jpg",
		"Group Conversation - 2022-09-30T19_19_50Z.html",
		"+12012434317 - Received - 2025-02-18T18_16_21Z.html",
		"+12014411841 - Missed - 2025-10-13T17_12_01Z.html",
		"+12014411841 - Voicemail - 2026-02-10T21_37_07Z.html",
	}

	for _, fname := range filesToCopy {
		src := filepath.Join(sampleDir, "Voice", "Calls", fname)
		if b, err := os.ReadFile(src); err == nil {
			_ = os.WriteFile(filepath.Join(callsDir, fname), b, 0644)
		}
	}

	// 1. Test Number Detection
	detected, err := DetectMyNumbersFromBackups(tmpDir, "")
	if err != nil {
		t.Fatalf("DetectMyNumbersFromBackups failed: %v", err)
	}
	foundGV := false
	for _, d := range detected {
		if d.Phone == "+16465043037" {
			foundGV = true
		}
	}
	if !foundGV {
		t.Errorf("Expected to detect Google Voice account +16465043037, got %v", detected)
	}

	// 2. Test Merge without number normalization (segregated account tagging)
	outXML := filepath.Join(tmpDir, "merged_gv_segregated.xml")
	opts := MergeOptions{
		SourceFolder:      tmpDir,
		OutputFile:        outXML,
		IncludeMedia:      true,
		NormalizeSchema:   true,
		NormalizeMyNumber: false,
	}

	prog, err := MergeBackupsToSingleXML(opts)
	if err != nil {
		t.Fatalf("MergeBackupsToSingleXML failed: %v", err)
	}

	if prog.UniqueMessages < 10 {
		t.Errorf("Expected at least 10 unique items, got %d", prog.UniqueMessages)
	}

	// Verify the generated XML can be ingested into a user database
	userDBPath := filepath.Join(tmpDir, "test_user_gv.db")
	if err := InitUserDB("test_gv_user", userDBPath); err != nil {
		t.Fatalf("Failed to initialize user db: %v", err)
	}

	userDB, err := sql.Open("sqlite3", userDBPath)
	if err != nil {
		t.Fatalf("Failed to open user db: %v", err)
	}
	defer userDB.Close()

	xmlF, err := os.Open(outXML)
	if err != nil {
		t.Fatalf("Failed to open merged XML: %v", err)
	}
	defer xmlF.Close()

	msgCount, callCount, err := ParseSMSBackupStreaming(userDB, xmlF, 200, true)
	if err != nil {
		t.Fatalf("ParseSMSBackupStreaming failed on merged GV XML: %v", err)
	}

	if msgCount < 5 {
		t.Errorf("Expected at least 5 messages, got %d", msgCount)
	}
	if callCount != 3 { // Received, Missed, Voicemail
		t.Errorf("Expected 3 calls, got %d", callCount)
	}

	// Verify Account Segregation tags
	accounts, err := GetAccounts(userDB)
	if err != nil {
		t.Fatalf("GetAccounts failed: %v", err)
	}
	foundGVAccount := false
	for _, a := range accounts {
		if a.Account == "+16465043037" {
			foundGVAccount = true
			if a.CallCount != 3 {
				t.Errorf("Expected 3 calls for account +16465043037, got %d", a.CallCount)
			}
			if a.MessageCount < 5 {
				t.Errorf("Expected at least 5 messages for account +16465043037, got %d", a.MessageCount)
			}
		}
	}
	if !foundGVAccount {
		t.Errorf("Expected account +16465043037 in GetAccounts, got %v", accounts)
	}

	// 3. Test Merge WITH number normalization enabled (aliasing to a primary target number)
	outNormXML := filepath.Join(tmpDir, "merged_gv_normalized.xml")
	normOpts := MergeOptions{
		SourceFolder:       tmpDir,
		OutputFile:         outNormXML,
		IncludeMedia:       true,
		NormalizeSchema:    true,
		NormalizeMyNumber:  true,
		TargetMyNumber:     "+15559998888",
		AlternateMyNumbers: []string{"+16465043037", "+16462447741"},
	}

	normProg, err := MergeBackupsToSingleXML(normOpts)
	if err != nil {
		t.Fatalf("MergeBackupsToSingleXML with normalization failed: %v", err)
	}
	if normProg.UniqueMessages < 10 {
		t.Errorf("Expected at least 10 unique items, got %d", normProg.UniqueMessages)
	}

	normDBPath := filepath.Join(tmpDir, "test_user_norm.db")
	if err := InitUserDB("test_norm_user", normDBPath); err != nil {
		t.Fatalf("Failed to initialize user db: %v", err)
	}
	normDB, err := sql.Open("sqlite3", normDBPath)
	if err != nil {
		t.Fatalf("Failed to open user db: %v", err)
	}
	defer normDB.Close()

	normXMLF, err := os.Open(outNormXML)
	if err != nil {
		t.Fatalf("Failed to open normalized XML: %v", err)
	}
	defer normXMLF.Close()

	normMsgCount, normCallCount, err := ParseSMSBackupStreaming(normDB, normXMLF, 200, true)
	if err != nil {
		t.Fatalf("ParseSMSBackupStreaming failed on normalized XML: %v", err)
	}
	if normMsgCount < 5 || normCallCount != 3 {
		t.Errorf("Expected at least 5 messages and 3 calls, got %d msgs, %d calls", normMsgCount, normCallCount)
	}
}
