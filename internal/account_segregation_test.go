package internal

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/labstack/echo/v4"
)

func TestAccountSegregation(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "sbv_test_acc_*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	dbPath := filepath.Join(tmpDir, "test_account.db")
	_ = InitDB(dbPath)

	userID := "test_acc_user_1"
	userDBPath := filepath.Join(tmpDir, "sbv_"+userID+".db")
	if err := InitUserDB(userID, userDBPath); err != nil {
		t.Fatalf("Failed to init user DB: %v", err)
	}
	userDB, err := GetUserDB(userID, "testuser")
	if err != nil {
		t.Fatalf("Failed to get user DB: %v", err)
	}

	acc1 := "+16462447741"
	acc2 := "+19175551234"

	// 1. Insert messages for Account 1
	msg1 := &Message{
		Address: "+15551112222",
		Body:    "Hello from Account 1",
		Type:    1,
		Date:    time.Now().Add(-2 * time.Hour),
		Account: acc1,
	}
	if err := InsertMessage(userDB, msg1); err != nil {
		t.Fatalf("Failed to insert msg1: %v", err)
	}

	call1 := &CallLog{
		Number:   "+15551112222",
		Type:     1,
		Duration: 60,
		Date:     time.Now().Add(-1 * time.Hour),
		Account:  acc1,
	}
	if err := InsertCallLog(userDB, call1); err != nil {
		t.Fatalf("Failed to insert call1: %v", err)
	}

	// 2. Insert messages for Account 2
	msg2 := &Message{
		Address: "+15553334444",
		Body:    "Hello from Account 2",
		Type:    2,
		Date:    time.Now().Add(-30 * time.Minute),
		Account: acc2,
	}
	if err := InsertMessage(userDB, msg2); err != nil {
		t.Fatalf("Failed to insert msg2: %v", err)
	}

	msg3 := &Message{
		Address: "+15553334444",
		Body:    "Another from Account 2",
		Type:    1,
		Date:    time.Now().Add(-15 * time.Minute),
		Account: acc2,
	}
	if err := InsertMessage(userDB, msg3); err != nil {
		t.Fatalf("Failed to insert msg3: %v", err)
	}

	// 3. Test GetAccounts
	accounts, err := GetAccounts(userDB)
	if err != nil {
		t.Fatalf("GetAccounts failed: %v", err)
	}
	if len(accounts) != 2 {
		t.Fatalf("Expected 2 accounts, got %d", len(accounts))
	}

	accountMap := make(map[string]AccountInfo)
	for _, a := range accounts {
		accountMap[a.Account] = a
	}

	info1, ok1 := accountMap[acc1]
	if !ok1 {
		t.Fatalf("Account 1 not found in GetAccounts")
	}
	if info1.MessageCount != 1 || info1.CallCount != 1 {
		t.Errorf("Account 1 counts mismatch: got msgs=%d, calls=%d; expected 1 msg, 1 call", info1.MessageCount, info1.CallCount)
	}

	info2, ok2 := accountMap[acc2]
	if !ok2 {
		t.Fatalf("Account 2 not found in GetAccounts")
	}
	if info2.MessageCount != 2 || info2.CallCount != 0 {
		t.Errorf("Account 2 counts mismatch: got msgs=%d, calls=%d; expected 2 msgs, 0 calls", info2.MessageCount, info2.CallCount)
	}

	// 4. Test GetConversations with and without account filter
	allConvs, err := GetConversations(userDB, nil, nil)
	if err != nil {
		t.Fatalf("GetConversations (all) failed: %v", err)
	}
	if len(allConvs) != 2 {
		t.Errorf("Expected 2 conversations in total, got %d", len(allConvs))
	}

	acc1Convs, err := GetConversations(userDB, nil, nil, acc1)
	if err != nil {
		t.Fatalf("GetConversations (acc1) failed: %v", err)
	}
	if len(acc1Convs) != 1 || acc1Convs[0].Address != "+15551112222" {
		t.Errorf("Expected 1 conversation for acc1 with +15551112222, got %v", acc1Convs)
	}

	acc2Convs, err := GetConversations(userDB, nil, nil, acc2)
	if err != nil {
		t.Fatalf("GetConversations (acc2) failed: %v", err)
	}
	if len(acc2Convs) != 1 || acc2Convs[0].Address != "+15553334444" {
		t.Errorf("Expected 1 conversation for acc2 with +15553334444, got %v", acc2Convs)
	}

	// 5. Test GetAnalytics segregated by account
	allAnalytics, err := GetAnalytics(userDB, nil, nil, 10, 0)
	if err != nil {
		t.Fatalf("GetAnalytics (all) failed: %v", err)
	}
	if allAnalytics.TotalMessages != 4 {
		t.Errorf("Expected 4 total records in all analytics, got %d", allAnalytics.TotalMessages)
	}

	acc1Analytics, err := GetAnalytics(userDB, nil, nil, 10, 0, acc1)
	if err != nil {
		t.Fatalf("GetAnalytics (acc1) failed: %v", err)
	}
	if acc1Analytics.TotalMessages != 2 || acc1Analytics.TotalCalls != 1 {
		t.Errorf("Expected 2 items (1 msg, 1 call) in acc1 analytics, got %d msgs, %d calls", acc1Analytics.TotalMessages, acc1Analytics.TotalCalls)
	}

	acc2Analytics, err := GetAnalytics(userDB, nil, nil, 10, 0, acc2)
	if err != nil {
		t.Fatalf("GetAnalytics (acc2) failed: %v", err)
	}
	if acc2Analytics.TotalMessages != 2 || acc2Analytics.TotalCalls != 0 {
		t.Errorf("Expected 2 msgs in acc2 analytics, got %d msgs, %d calls", acc2Analytics.TotalMessages, acc2Analytics.TotalCalls)
	}

	// 6. Test HandleGetAccounts HTTP endpoint
	e := echo.New()
	req := httptest.NewRequest(http.MethodGet, "/api/accounts", nil)
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)
	c.Set("user_id", userID)
	c.Set("username", "testuser")

	if err := HandleGetAccounts(c); err != nil {
		t.Fatalf("HandleGetAccounts failed: %v", err)
	}
	if rec.Code != http.StatusOK {
		t.Fatalf("Expected 200 OK, got %d: %s", rec.Code, rec.Body.String())
	}

	var jsonAccounts []AccountInfo
	if err := json.Unmarshal(rec.Body.Bytes(), &jsonAccounts); err != nil {
		t.Fatalf("Failed to parse JSON accounts: %v", err)
	}
	if len(jsonAccounts) != 2 {
		t.Fatalf("Expected 2 accounts in JSON response, got %d", len(jsonAccounts))
	}
}
