# AGENTS.md — SMS Backup Viewer & Multi-Format Merger

This file serves as the definitive operational manual, architecture guide, and rulebook for AI agents working in this repository.

---

## 🚨 RULE ZERO: Mandatory Maintenance of AGENTS.md

> [!IMPORTANT]
> **All AI agents working on this project must keep this `agents.md` file up to date.**
> Whenever you:
> - Add, remove, or modify a feature or architectural pipeline,
> - Discover a critical pitfall, environment requirement, or toolchain constraint,
> - Change build procedures, packaging scripts, or runtime arguments,
> - Add new file formats, schemas, or external integrations,
>
> **You MUST update `agents.md` in the same turn or session** so subsequent agents and human collaborators inherit accurate context.

---

## 1. Project Purpose & High-Level Architecture

**SMS Backup Viewer (SBV) Portable for Windows** is an offline, standalone Windows application that serves three core purposes:
1. **Interactive Conversation Viewer**: A fast, local web interface for browsing, searching (SQLite FTS5), and analyzing SMS/MMS messages and call logs.
2. **Media Extraction Engine**: Decodes and organizes embedded base64 multimedia from MMS messages directly onto the local filesystem into categorized directories (`image/`, `video/`, `audio/`, `other/`).
3. **Multi-Format Backup Merger**: Recursively scans directories for fragmented or overlapping backups across multiple platforms—Android SMS Backup & Restore (`.xml`, `.zip`), encrypted Signal Android (`.backup`), and Google Voice Takeout (HTML folders & `.zip`)—and merges them into a single, clean, deduplicated, chronologically sorted XML backup.

### Core Technology Stack
- **Backend**: Go (1.25+) with the Echo v4 web framework.
- **Embedded Database**: SQLite3 with native FTS5 full-text search indexing (`mattn/go-sqlite3`).
- **Codec Support**: Native `libheif` CGO bindings (`lowcarbdev/libheif-go`) with MSYS2 UCRT64 codec DLLs for Apple HEIC/HEIF photo decoding.
- **Frontend**: React 19, Vite, Bootstrap 5 / React Bootstrap, Recharts, Lucide icons.
- **Desktop Runtime**: Passwordless local mode bound strictly to `127.0.0.1:8085` via `Start SBV.cmd`.

---

## 2. Repository Layout & Module Directory

```
sms-backup-pro-viewer-executable/
├── main.go                               # Application entrypoint & CLI flags
├── internal/                             # Core Go backend packages
│   ├── account_segregation_test.go      # Tests for multi-account UI segregation
│   ├── auth.go / auth_handlers.go       # Authentication & passwordless desktop mode
│   ├── autoimport.go                    # Auto-import background watcher
│   ├── database.go                      # SQLite connection, schema, FTS5 queries
│   ├── dialogs.go                       # Modern Windows IFileOpenDialog folder picker
│   ├── google_voice_decoder.go          # Google Voice Takeout parser & normalizer
│   ├── google_voice_test.go             # Unit & integration tests for Google Voice
│   ├── handlers.go                      # REST API endpoints (conversations, messages, calls)
│   ├── heic_enabled.go / disabled.go    # libheif build tag switches
│   ├── media_extractor.go               # MMS base64 media extraction pipeline
│   ├── models.go                        # Data schemas (SMS, MMS, Call, Summary)
│   ├── parser.go                        # Streaming XML backup parser
│   ├── signal_decoder.go                # Encrypted Signal Android backup engine
│   ├── signal_decoder_test.go           # Signal decryption & parsing tests
│   ├── utils.go                         # Phone number normalization helpers
│   ├── xml_merger.go                    # Multi-format backup discovery & SQLite merger
│   └── xml_merger_test.go               # Multi-format deduplication & merge tests
├── frontend/                             # React 19 Single Page Application
│   ├── src/
│   │   ├── App.jsx                      # Main routing, Account Selector & view switching
│   │   ├── components/
│   │   │   ├── ConversationList.jsx     # Contact list with account badges
│   │   │   ├── MessageThread.jsx        # Chronological message bubble view
│   │   │   ├── Calls.jsx                # Call logs with duration & type stats
│   │   │   ├── Summary.jsx              # Analytics & message statistics per account
│   │   │   ├── Search.jsx               # FTS5 full-text message search
│   │   │   ├── MediaExtractorModal.jsx  # Native disk media extraction UI
│   │   │   └── MergeBackupsModal.jsx    # Multi-format backup merger & aliasing UI
│   ├── dist/                            # Production precompiled web assets (gitignored)
│   └── package.json                     # Frontend dependencies & scripts
├── Build-SBV-Windows.ps1                 # Unified master PowerShell build & packaging script
├── SBV-Windows-Builder/
│   ├── SBV-Windows-Portable/            # Standalone distribution folder
│   │   ├── sbv.exe                      # Compiled Windows binary (FTS5 + HEIC)
│   │   ├── Start SBV.cmd                # One-click portable launcher
│   │   ├── frontend/dist/               # Embedded React production bundle
│   │   ├── *.dll                        # Bundled UCRT64 runtime & codec DLLs
│   │   └── README-WINDOWS.txt           # End-user portable release instructions
│   └── SBV-Windows-Portable.zip         # Packaged release zip archive
├── README.md                             # Primary project documentation
└── AGENTS.md                             # This agent guide
```

---

## 3. Toolchain, Build & Compilation Rules

### Windows Build Environment
The project relies on native Windows CGO compilers and MSYS2 UCRT64 libraries:
- **Go Path**: `C:\Program Files\Go\bin\go.exe`
- **MSYS2 Toolchain**: `C:\msys64\ucrt64\bin` (GCC, `pkg-config`, DLLs)
- **Node.js / npm**: Node 22+

### ⚠️ Mandatory Go Build Tags
Running `go build` or `go test` without tags will **fail**:
- Plain `go test` fails with `no such module: fts5` or CGO stub errors.
- Always use:
  ```powershell
  $env:CGO_ENABLED = "1"
  $env:PATH = "C:\msys64\ucrt64\bin;" + $env:PATH
  $env:PKG_CONFIG_PATH = "C:\msys64\ucrt64\lib\pkgconfig;C:\msys64\ucrt64\share\pkgconfig"

  # Testing:
  go test -tags "fts5 heic" ./internal/...

  # Compiling:
  go build -trimpath -tags "fts5 heic" -ldflags "-s -w" -o sbv.exe .
  ```

### Frontend Build
- Run from `frontend/`:
  ```powershell
  cd frontend
  npm run build
  cd ..
  ```
- Assets are output to `frontend/dist/`.

### Updating the Portable Release
Whenever modifying the Go backend or React frontend:
1. Recompile `sbv.exe` and `frontend/dist`.
2. Copy `sbv.exe` into `SBV-Windows-Builder/SBV-Windows-Portable/sbv.exe`.
3. Copy `frontend/dist` into `SBV-Windows-Builder/SBV-Windows-Portable/frontend/dist`.
4. Repackage the portable zip archive using `tar.exe` (excluding any `.db*`, `.log*`, or `.tmp` files):
   ```powershell
   $OutputDir = "SBV-Windows-Builder\SBV-Windows-Portable"
   $zip = "SBV-Windows-Builder\SBV-Windows-Portable.zip"
   if (Test-Path $zip) { Remove-Item $zip -Force }
   tar.exe --exclude="*.db*" --exclude="*.log*" --exclude="*.tmp" -a -cf $zip -C $OutputDir *
   ```

---

## 4. Architectural Rules & Best Practices

### A. Windows Folder Browser Dialogs (`IFileOpenDialog`)
- **NEVER** use the legacy WinForms `FolderBrowserDialog` (the antiquated tree-view shown in older Windows versions).
- **ALWAYS** use `ShowModernFolderPicker(title, initialDir)` from [`internal/dialogs.go`](file:///C:/Users/iamin/PycharmProjects/sms-backup-pro-viewer-executable/internal/dialogs.go).
- This utilizes the native Windows Shell `IFileOpenDialog` with `FOS_PICKFOLDERS | FOS_FORCEFILESYSTEM`, rendering the modern Windows Explorer "Save As" / "Open File" dialog with breadcrumbs, Quick Access, network drives, and a "Select Folder" button.

### B. Multi-Format Merger & Low-RAM Pipeline
- **Memory Constraint**: Backups can exceed 25+ GB. Agents must **never** buffer entire XML backups or decoded files in memory.
- **SQLite Staging Database**: Merging uses a temporary disk-backed SQLite database (`staging_records`) with MD5 deduplication keys:
  - Deduplicates SMS by `(Address, Date, Type, Body, Account)`.
  - Deduplicates MMS by `(Address, Date, msg_box, Body, PartCount, Account)`.
  - Deduplicates Calls by `(Number, Date, Type, Duration, Account)`.
- **Richness Priority**: If a duplicate key matches, the merger preserves the richer record (e.g., records containing contact names instead of placeholders, and records containing embedded media parts).

### C. Phone Number Normalization vs. Account Segregation
- **"My Number" Normalization**:
  - Detects current and former phone numbers across MMS headers, Signal records, and Google Voice `Phones.vcf`.
  - When enabled, aliases former numbers to the user's primary number and strips user numbers from group MMS addresses (`Alice~Bob~OldNumber` &rarr; `Alice~Bob`), unifying fragmented conversations and eliminating cross-backup duplicates.
- **Account Segregation**:
  - When normalization is disabled, records retain their originating identity via the `account` attribute (e.g., `account="+16465043037"`).
  - The frontend `AccountSelector` dropdown allows users to view either `All Accounts` or isolate message threads, calls, activity, and analytics for a specific number.

### D. Google Voice Takeout Normalization
- Converts Takeout HTML files (`Voice/Calls/*.html`) and `.zip` archives into the standard Android XML schema:
  - 1-on-1 SMS: mapped to `<sms>` (type 1 for incoming, 2 for sent).
  - Group Chats: `<div class="participants">` mapped to `<mms>` with tilde-delimited recipient strings.
  - Attachments: links relative images, audio, and video files on disk or inside the ZIP into base64 `<part>` elements.
  - Calls: parses duration (`PT29S` or `hh:mm:ss`), type (Received=1, Placed=2, Missed=3, Voicemail=4), and transcripts into `<call>` records.

---

## 5. Privacy, Git & Release Standards

### 🛡️ Privacy & Sensitive Data
- **NEVER COMMIT PERSONAL DATA**: The user's sample data folders (such as `google-voice-data/`, `test-data/`, `*.backup`, `media/`, `data/`, `*.db*`) contain personal messages, photos, and phone numbers.
- Always check `git status` before committing. Ensure sensitive test folders remain strictly in `.gitignore`.

### 🛡️ GitHub Email Privacy (Error GH007)
- GitHub account has email privacy protection enabled (`push declined due to email privacy restrictions`).
- All commits pushed to GitHub **must** use the authorized public email:
  ```powershell
  git config user.name "alex"
  git config user.email "alex.aminov@geenuity.com"
  ```
- If an amend is ever needed:
  ```powershell
  $env:GIT_COMMITTER_EMAIL = "alex.aminov@geenuity.com"
  git commit --amend --author="alex <alex.aminov@geenuity.com>" --no-edit
  ```

### 🛡️ Zero Redundancy
- Maintain a single, streamlined builder architecture.
- Do not create secondary builder scripts (e.g., `builder-portable/`, `portable-builder/`). Everything is orchestrated by `Build-SBV-Windows.ps1` and `Start SBV.cmd`.

---

## 6. Checklist for Future Agents

Before finishing any task in this codebase, verify:
1. [ ] **Tests Pass**: `go test -tags "fts5 heic" ./internal/...` exits code 0.
2. [ ] **Frontend Builds**: `cd frontend; npm run build` completes cleanly without warnings or errors.
3. [ ] **Portable Release Updated**: `sbv.exe` and `frontend/dist` are copied into `SBV-Windows-Portable` and zipped into `SBV-Windows-Portable.zip`.
4. [ ] **Git Cleanliness**: No private data (`google-voice-data`, `.db`, `.zip`) is staged.
5. [ ] **Author Email Verified**: Commits use `alex.aminov@geenuity.com`.
6. [ ] **Documentation**: `README.md` and this **`AGENTS.md`** are updated with any new capabilities or behavior changes.
