package internal

import (
	"crypto"
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	_ "crypto/sha256"
	_ "crypto/sha512"
	"database/sql"
	"encoding/base64"
	"encoding/binary"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "github.com/mattn/go-sqlite3"
	"golang.org/x/crypto/hkdf"
)

// SignalRecipientInfo holds mapped contact details for a Signal recipient
type SignalRecipientInfo struct {
	ID          int64
	Phone       string
	DisplayName string
	GroupID     string
	GroupTitle  string
	GroupPhones []string
}

// IsSignalBackup checks if the given file has a .backup extension or Signal header
func IsSignalBackup(filePath string) bool {
	if strings.HasSuffix(strings.ToLower(filePath), ".backup") {
		return true
	}
	return false
}

func signalBackupKey(password string, salt []byte) []byte {
	digest := crypto.SHA512.New()
	input := []byte(strings.ReplaceAll(strings.TrimSpace(password), " ", ""))
	hash := input

	if salt != nil {
		digest.Write(salt)
	}

	for i := 0; i < 250000; i++ {
		digest.Write(hash)
		digest.Write(input)
		hash = digest.Sum(nil)
		digest.Reset()
	}

	return hash[:32]
}

func signalDeriveSecrets(input, info []byte) []byte {
	sha := crypto.SHA256.New
	salt := make([]byte, sha().Size())
	okm := make([]byte, 64)

	hkdf := hkdf.New(sha, input, salt, info)
	_, _ = io.ReadFull(hkdf, okm)
	return okm
}

func signalBytesToUint32(b []byte) (val uint32) {
	val |= uint32(b[3])
	val |= uint32(b[2]) << 8
	val |= uint32(b[1]) << 16
	val |= uint32(b[0]) << 24
	return
}

func signalUint32ToBytes(b []byte, val uint32) {
	b[3] = byte(val)
	b[2] = byte(val >> 8)
	b[1] = byte(val >> 16)
	b[0] = byte(val >> 24)
}

func parseSignalRawBackupFrame(b []byte) (sqlStr string, params []interface{}, attRowId uint64, attLen uint32, isEnd bool) {
	pos := 0
	for pos < len(b) {
		tag, n := binary.Uvarint(b[pos:])
		if n <= 0 {
			break
		}
		pos += n
		fieldNum := tag >> 3
		wireType := tag & 0x07

		switch wireType {
		case 0:
			v, n := binary.Uvarint(b[pos:])
			pos += n
			if fieldNum == 6 && v != 0 {
				isEnd = true
			}
		case 1:
			pos += 8
		case 2:
			l, n := binary.Uvarint(b[pos:])
			pos += n
			if pos+int(l) > len(b) {
				return
			}
			val := b[pos : pos+int(l)]
			pos += int(l)

			if fieldNum == 2 {
				sqlStr, params = parseSignalSqlStatement(val)
			} else if fieldNum == 4 {
				attRowId, attLen = parseSignalAttachmentInfo(val)
			} else if fieldNum == 7 {
				attLen = parseSignalAvatarLen(val)
			}
		case 5:
			pos += 4
		default:
			return
		}
	}
	return
}

func parseSignalSqlStatement(b []byte) (statement string, params []interface{}) {
	pos := 0
	for pos < len(b) {
		tag, n := binary.Uvarint(b[pos:])
		if n <= 0 {
			break
		}
		pos += n
		fieldNum := tag >> 3
		wireType := tag & 0x07

		if wireType == 2 {
			l, n := binary.Uvarint(b[pos:])
			pos += n
			if pos+int(l) > len(b) {
				break
			}
			val := b[pos : pos+int(l)]
			pos += int(l)

			if fieldNum == 1 {
				statement = string(val)
			} else if fieldNum == 2 {
				param := parseSignalSqlParameter(val)
				params = append(params, param)
			}
		} else {
			break
		}
	}
	return
}

func parseSignalSqlParameter(b []byte) interface{} {
	pos := 0
	for pos < len(b) {
		tag, n := binary.Uvarint(b[pos:])
		if n <= 0 {
			break
		}
		pos += n
		fieldNum := tag >> 3
		wireType := tag & 0x07

		switch fieldNum {
		case 1:
			if wireType == 2 {
				l, n := binary.Uvarint(b[pos:])
				pos += n
				if pos+int(l) > len(b) {
					return ""
				}
				s := string(b[pos : pos+int(l)])
				pos += int(l)
				return s
			}
		case 2:
			if wireType == 0 {
				v, n := binary.Uvarint(b[pos:])
				pos += n
				return int64(v)
			}
		case 3:
			if wireType == 1 {
				if pos+8 > len(b) {
					return nil
				}
				bits := binary.LittleEndian.Uint64(b[pos : pos+8])
				pos += 8
				return bits
			}
		case 4:
			if wireType == 2 {
				l, n := binary.Uvarint(b[pos:])
				pos += n
				if pos+int(l) > len(b) {
					return nil
				}
				blob := make([]byte, l)
				copy(blob, b[pos:pos+int(l)])
				pos += int(l)
				return blob
			}
		case 5:
			return nil
		}
	}
	return nil
}

func parseSignalAttachmentInfo(b []byte) (rowId uint64, length uint32) {
	pos := 0
	for pos < len(b) {
		tag, n := binary.Uvarint(b[pos:])
		if n <= 0 {
			break
		}
		pos += n
		fieldNum := tag >> 3
		wireType := tag & 0x07
		if fieldNum == 1 && wireType == 0 {
			v, n := binary.Uvarint(b[pos:])
			rowId = v
			pos += n
		} else if fieldNum == 3 && wireType == 0 {
			v, n := binary.Uvarint(b[pos:])
			length = uint32(v)
			pos += n
		} else if wireType == 0 {
			_, n := binary.Uvarint(b[pos:])
			pos += n
		} else if wireType == 2 {
			l, n := binary.Uvarint(b[pos:])
			pos += n + int(l)
		} else {
			break
		}
	}
	return
}

func parseSignalAvatarLen(b []byte) uint32 {
	pos := 0
	for pos < len(b) {
		tag, n := binary.Uvarint(b[pos:])
		if n <= 0 {
			break
		}
		pos += n
		fieldNum := tag >> 3
		wireType := tag & 0x07
		if fieldNum == 2 && wireType == 0 {
			v, _ := binary.Uvarint(b[pos:])
			return uint32(v)
		}
		if wireType == 0 {
			_, n := binary.Uvarint(b[pos:])
			pos += n
		} else if wireType == 2 {
			l, n := binary.Uvarint(b[pos:])
			pos += n + int(l)
		} else {
			break
		}
	}
	return 0
}

// DecodeSignalBackup decrypts an encrypted Signal Android backup file and converts
// its messages into standard SMSEntry and MMSEntry structures.
func DecodeSignalBackup(
	backupPath string,
	passphrase string,
	includeMedia bool,
	emitSMS func(sms *SMSEntry, dateMs int64) error,
	emitMMS func(mms *MMSEntry, dateMs int64) error,
) (int, int, error) {
	cleanPass := strings.ReplaceAll(strings.TrimSpace(passphrase), " ", "")
	if cleanPass == "" {
		return 0, 0, fmt.Errorf("passphrase is required to decrypt Signal backup %s", filepath.Base(backupPath))
	}

	f, err := os.Open(backupPath)
	if err != nil {
		return 0, 0, fmt.Errorf("failed to open Signal backup: %w", err)
	}
	defer f.Close()

	var headerLen uint32
	if err := binary.Read(f, binary.BigEndian, &headerLen); err != nil {
		return 0, 0, fmt.Errorf("failed to read Signal backup header length: %w", err)
	}

	headerData := make([]byte, headerLen)
	if _, err := io.ReadFull(f, headerData); err != nil {
		return 0, 0, fmt.Errorf("failed to read Signal backup header frame: %w", err)
	}

	var iv, salt []byte
	pos := 0
	if pos < len(headerData) && headerData[pos] == 0x0a {
		pos++
		_, n := binary.Uvarint(headerData[pos:])
		pos += n
	}

	for pos < len(headerData) {
		tag := headerData[pos]
		pos++
		fieldNum := tag >> 3
		wireType := tag & 0x07

		if wireType == 2 {
			l, n := binary.Uvarint(headerData[pos:])
			pos += n
			if pos+int(l) > len(headerData) {
				break
			}
			val := headerData[pos : pos+int(l)]
			pos += int(l)

			if fieldNum == 1 {
				iv = val
			} else if fieldNum == 2 {
				salt = val
			}
		} else {
			break
		}
	}

	if len(iv) != 16 || len(salt) != 32 {
		return 0, 0, fmt.Errorf("invalid Signal backup file format (header missing IV/Salt)")
	}

	key := signalBackupKey(cleanPass, salt)
	derived := signalDeriveSecrets(key, []byte("Backup Export"))
	cipherKey := derived[:32]
	macKey := derived[32:]

	aesBlock, err := aes.NewCipher(cipherKey)
	if err != nil {
		return 0, 0, fmt.Errorf("failed to initialize AES cipher: %w", err)
	}

	currentIV := make([]byte, 16)
	copy(currentIV, iv)
	counter := signalBytesToUint32(iv)

	// Temporary directory for staged media attachments and SQLite DB
	tempDir, err := os.MkdirTemp("", "sbv_signal_*")
	if err != nil {
		return 0, 0, fmt.Errorf("failed to create temp dir: %w", err)
	}
	defer os.RemoveAll(tempDir)

	dbPath := filepath.Join(tempDir, "signal_temp.db")
	db, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		return 0, 0, fmt.Errorf("failed to open temporary SQLite database: %w", err)
	}
	defer db.Close()

	_, _ = db.Exec("PRAGMA synchronous = OFF; PRAGMA journal_mode = MEMORY; PRAGMA temp_store = MEMORY;")

	macHasher := hmac.New(crypto.SHA256.New, macKey)
	tx, err := db.Begin()
	if err != nil {
		return 0, 0, fmt.Errorf("failed to begin transaction: %w", err)
	}

	sqlCount := 0
	totalFrames := 0

	for {
		var frameLen uint32
		err := binary.Read(f, binary.BigEndian, &frameLen)
		if err == io.EOF {
			break
		}
		if err != nil {
			break
		}

		frame := make([]byte, frameLen)
		if _, err := io.ReadFull(f, frame); err != nil {
			break
		}

		if len(frame) < 10 {
			break
		}

		theirMac := frame[len(frame)-10:]
		ciphertext := frame[:len(frame)-10]

		macHasher.Reset()
		macHasher.Write(ciphertext)
		ourMac := macHasher.Sum(nil)[:10]

		if !hmac.Equal(theirMac, ourMac) {
			_ = tx.Rollback()
			if totalFrames == 0 {
				return 0, 0, fmt.Errorf("incorrect Signal backup passphrase")
			}
			return 0, 0, fmt.Errorf("corrupted Signal backup frame at frame %d", totalFrames)
		}

		signalUint32ToBytes(currentIV, counter)
		counter++
		stream := cipher.NewCTR(aesBlock, currentIV)
		plaintext := make([]byte, len(ciphertext))
		stream.XORKeyStream(plaintext, ciphertext)

		totalFrames++

		stmtSQL, stmtParams, attRowId, attLen, isEnd := parseSignalRawBackupFrame(plaintext)

		if stmtSQL != "" {
			sqlCount++
			_, _ = tx.Exec(stmtSQL, stmtParams...)
			if sqlCount%1000 == 0 {
				_ = tx.Commit()
				tx, _ = db.Begin()
			}
		}

		if attLen > 0 {
			signalUint32ToBytes(currentIV, counter)
			counter++

			attBuf := make([]byte, attLen)
			_, _ = io.ReadFull(f, attBuf)
			macBuf := make([]byte, 10)
			_, _ = io.ReadFull(f, macBuf)

			if includeMedia && attRowId > 0 {
				streamAtt := cipher.NewCTR(aesBlock, currentIV)
				attPlain := make([]byte, attLen)
				streamAtt.XORKeyStream(attPlain, attBuf)

				partFile := filepath.Join(tempDir, fmt.Sprintf("part_%d.bin", attRowId))
				_ = os.WriteFile(partFile, attPlain, 0600)
			}
		}

		if isEnd {
			break
		}
	}

	_ = tx.Commit()

	// Load recipients
	recipients := make(map[int64]SignalRecipientInfo)
	rRows, err := db.Query("SELECT _id, phone, system_display_name, signal_profile_name, group_id FROM recipient")
	if err == nil {
		for rRows.Next() {
			var id int64
			var phone, sysName, sigName, gid sql.NullString
			rRows.Scan(&id, &phone, &sysName, &sigName, &gid)

			disp := sysName.String
			if disp == "" {
				disp = sigName.String
			}
			if disp == "" {
				disp = phone.String
			}

			recipients[id] = SignalRecipientInfo{
				ID:          id,
				Phone:       phone.String,
				DisplayName: disp,
				GroupID:     gid.String,
			}
		}
		rRows.Close()
	}

	// Load groups
	gRows, err := db.Query("SELECT group_id, recipient_id, title, members FROM groups")
	if err == nil {
		for gRows.Next() {
			var gid sql.NullString
			var recipId sql.NullInt64
			var title, members sql.NullString
			gRows.Scan(&gid, &recipId, &title, &members)

			var memberPhones []string
			if members.Valid && members.String != "" {
				mIDs := strings.Split(members.String, ",")
				for _, midStr := range mIDs {
					var midVal int64
					fmt.Sscanf(strings.TrimSpace(midStr), "%d", &midVal)
					if midVal > 0 {
						if r, ok := recipients[midVal]; ok && r.Phone != "" {
							memberPhones = append(memberPhones, r.Phone)
						}
					}
				}
			}

			if recipId.Valid && recipId.Int64 > 0 {
				r := recipients[recipId.Int64]
				r.GroupTitle = title.String
				r.GroupPhones = memberPhones
				recipients[recipId.Int64] = r
			}
		}
		gRows.Close()
	}

	smsCount := 0
	mmsCount := 0

	// 1. Process SMS table
	sRows, err := db.Query("SELECT _id, thread_id, address, date, date_sent, read, type, body FROM sms ORDER BY date ASC")
	if err == nil {
		for sRows.Next() {
			var id, threadId, addrId, dateVal, dateSentVal, readVal, typeVal int64
			var body sql.NullString
			if err := sRows.Scan(&id, &threadId, &addrId, &dateVal, &dateSentVal, &readVal, &typeVal, &body); err != nil {
				continue
			}

			rInfo := recipients[addrId]
			addr := rInfo.Phone
			if addr == "" {
				if rInfo.DisplayName != "" {
					addr = rInfo.DisplayName
				} else {
					addr = fmt.Sprintf("signal_%d", addrId)
				}
			}

			contactName := rInfo.DisplayName
			if contactName == "" {
				contactName = "(Unknown)"
			}

			// Map Signal type to SMS Backup & Restore type
			baseType := typeVal & 0x1F
			smsType := "1" // Default incoming
			if baseType == 23 || baseType == 21 || baseType == 22 || baseType == 24 {
				smsType = "2" // Outgoing
			}

			readStr := "1"
			if readVal == 0 {
				readStr = "0"
			}

			if dateSentVal <= 0 {
				dateSentVal = dateVal
			}

			entry := SMSEntry{
				Address:       addr,
				Date:          fmt.Sprintf("%d", dateVal),
				Type:          smsType,
				Body:          body.String,
				Read:          readStr,
				ThreadID:      fmt.Sprintf("%d", threadId),
				Subject:       "null",
				Protocol:      "0",
				TOA:           "null",
				SCTOA:         "null",
				ServiceCenter: "null",
				Status:        "-1",
				SubID:         "1",
				ReadableDate:  time.Unix(dateVal/1000, 0).Format("Jan 02, 2006 3:04:05 PM"),
				ContactName:   contactName,
			}

			if err := emitSMS(&entry, dateVal); err == nil {
				smsCount++
			}
		}
		sRows.Close()
	}

	// 2. Process MMS table
	mRows, err := db.Query("SELECT _id, thread_id, address, date, date_received, msg_box, read, body FROM mms ORDER BY date ASC")
	if err == nil {
		for mRows.Next() {
			var id, threadId, addrId, dateVal, dateRecvVal, msgBoxVal, readVal int64
			var body sql.NullString
			if err := mRows.Scan(&id, &threadId, &addrId, &dateVal, &dateRecvVal, &msgBoxVal, &readVal, &body); err != nil {
				continue
			}

			rInfo := recipients[addrId]

			baseBox := msgBoxVal & 0x1F
			mmsType := "1"
			if baseBox == 23 || baseBox == 21 || baseBox == 22 || baseBox == 24 {
				mmsType = "2"
			}

			readStr := "1"
			if readVal == 0 {
				readStr = "0"
			}

			addr := rInfo.Phone
			if addr == "" && len(rInfo.GroupPhones) > 0 {
				addr = "~" + strings.Join(rInfo.GroupPhones, "~")
			} else if addr == "" {
				if rInfo.GroupTitle != "" {
					addr = rInfo.GroupTitle
				} else if rInfo.DisplayName != "" {
					addr = rInfo.DisplayName
				} else {
					addr = fmt.Sprintf("signal_%d", addrId)
				}
			}

			contactName := rInfo.DisplayName
			if contactName == "" && rInfo.GroupTitle != "" {
				contactName = rInfo.GroupTitle
			}
			if contactName == "" {
				contactName = "(Unknown)"
			}

			// Clean body if it contains raw protobuf or base64 control data
			bodyText := body.String
			if strings.HasPrefix(bodyText, "CiQK") {
				bodyText = "" // Skip internal group control protos
			}

			entry := MMSEntry{
				Address:      addr,
				Date:         fmt.Sprintf("%d", dateVal),
				Type:         mmsType,
				Read:         readStr,
				ThreadID:     fmt.Sprintf("%d", threadId),
				Subject:      "null",
				TrID:         fmt.Sprintf("signal_mms_%d", id),
				ContentType:  "application/vnd.wap.multipart.related",
				ReadReport:   "null",
				ReadStatus:   "null",
				MessageID:    fmt.Sprintf("signal_%d", id),
				MessageSize:  "null",
				MessageType:  "132",
				SimSlot:      "0",
				ReadableDate: time.Unix(dateVal/1000, 0).Format("Jan 02, 2006 3:04:05 PM"),
				ContactName:  contactName,
				Body:         bodyText,
			}

			// Add MMS parts
			pRows, err := db.Query("SELECT _id, seq, ct, file_name, data_size FROM part WHERE mid = ? ORDER BY seq ASC", id)
			seqIdx := 0
			if err == nil {
				for pRows.Next() {
					var pid, pseq, psize int64
					var pct, pfn sql.NullString
					pRows.Scan(&pid, &pseq, &pct, &pfn, &psize)

					dataVal := "null"
					if includeMedia && psize > 0 {
						partFile := filepath.Join(tempDir, fmt.Sprintf("part_%d.bin", pid))
						if rawBytes, err := os.ReadFile(partFile); err == nil && len(rawBytes) > 0 {
							dataVal = base64.StdEncoding.EncodeToString(rawBytes)
						}
					}

					ctVal := pct.String
					if ctVal == "" {
						ctVal = "application/octet-stream"
					}
					fnVal := pfn.String
					if fnVal == "" {
						fnVal = fmt.Sprintf("attachment_%d", pid)
					}

					entry.Parts = append(entry.Parts, MMSPart{
						Seq:         fmt.Sprintf("%d", seqIdx),
						ContentType: ctVal,
						Name:        fnVal,
						Charset:     "null",
						CL:          fnVal,
						Text:        "null",
						Data:        dataVal,
					})
					seqIdx++
				}
				pRows.Close()
			}

			// If there is message text, add as a text/plain part
			if bodyText != "" {
				entry.Parts = append([]MMSPart{{
					Seq:         fmt.Sprintf("%d", seqIdx),
					ContentType: "text/plain",
					Name:        "text.txt",
					Charset:     "106",
					CL:          "text.txt",
					Text:        bodyText,
					Data:        "null",
				}}, entry.Parts...)
			}

			// Add addresses
			if len(rInfo.GroupPhones) > 0 {
				for _, gPhone := range rInfo.GroupPhones {
					entry.Addrs = append(entry.Addrs, MMSAddr{
						Address: gPhone,
						Type:    "130",
						Charset: "106",
					})
				}
				if mmsType == "2" {
					entry.Addrs = append(entry.Addrs, MMSAddr{
						Address: "insert-address-token",
						Type:    "137",
						Charset: "106",
					})
				}
			} else {
				if mmsType == "2" {
					entry.Addrs = append(entry.Addrs, MMSAddr{
						Address: addr,
						Type:    "151",
						Charset: "106",
					})
					entry.Addrs = append(entry.Addrs, MMSAddr{
						Address: "insert-address-token",
						Type:    "137",
						Charset: "106",
					})
				} else {
					entry.Addrs = append(entry.Addrs, MMSAddr{
						Address: addr,
						Type:    "137",
						Charset: "106",
					})
				}
			}

			if err := emitMMS(&entry, dateVal); err == nil {
				mmsCount++
			}
		}
		mRows.Close()
	}

	return smsCount, mmsCount, nil
}
