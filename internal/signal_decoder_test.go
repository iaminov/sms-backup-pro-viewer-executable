package internal

import (
	"os"
	"testing"
)

func TestDecodeSignalBackup(t *testing.T) {
	backupPath := "../signal-2022-11-10-02-00-00.backup"
	if _, err := os.Stat(backupPath); os.IsNotExist(err) {
		t.Skip("Signal backup file not found, skipping test")
	}

	passphrase := "31889 30544 62782 17192 51469 48815"
	smsCount := 0
	mmsCount := 0

	sCount, mCount, err := DecodeSignalBackup(backupPath, passphrase, false, func(sms *SMSEntry, dateMs int64) error {
		smsCount++
		if sms.Address == "" {
			t.Errorf("SMS has empty address: %+v", sms)
		}
		if sms.Date == "" {
			t.Errorf("SMS has empty date: %+v", sms)
		}
		return nil
	}, func(mms *MMSEntry, dateMs int64) error {
		mmsCount++
		if mms.Date == "" {
			t.Errorf("MMS has empty date: %+v", mms)
		}
		return nil
	})

	if err != nil {
		t.Fatalf("DecodeSignalBackup failed: %v", err)
	}

	t.Logf("Successfully decoded Signal backup: %d SMS, %d MMS", sCount, mCount)

	if sCount == 0 {
		t.Errorf("Expected at least one SMS, got 0")
	}
	if mCount == 0 {
		t.Errorf("Expected at least one MMS, got 0")
	}
}

func TestDecodeSignalBackupWithMedia(t *testing.T) {
	backupPath := "../signal-2022-11-10-02-00-00.backup"
	if _, err := os.Stat(backupPath); os.IsNotExist(err) {
		t.Skip("Signal backup file not found, skipping test")
	}

	passphrase := "31889 30544 62782 17192 51469 48815"
	mediaPartsFound := 0

	_, _, err := DecodeSignalBackup(backupPath, passphrase, true, func(sms *SMSEntry, dateMs int64) error {
		return nil
	}, func(mms *MMSEntry, dateMs int64) error {
		for _, p := range mms.Parts {
			if p.Data != "" && p.Data != "null" {
				mediaPartsFound++
			}
		}
		return nil
	})

	if err != nil {
		t.Fatalf("DecodeSignalBackup with media failed: %v", err)
	}

	t.Logf("Successfully extracted %d media parts from Signal backup", mediaPartsFound)
	if mediaPartsFound == 0 {
		t.Errorf("Expected at least one media part with data, got 0")
	}
}
