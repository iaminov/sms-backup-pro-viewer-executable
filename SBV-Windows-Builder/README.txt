SBV Windows Builder
===================

Purpose
-------
Build lowcarbdev/SBV as a native Windows x64 executable and package the frontend plus
native runtime dependencies, including libheif for HEIC/HEIF MMS images.

How to use
----------
1. Extract this ZIP to a normal writable folder, e.g. C:\SBV-Builder.
2. Right-click Build-SBV-Windows.ps1 -> Run with PowerShell.
   If Windows blocks script execution, open PowerShell in the folder and run:

      powershell -ExecutionPolicy Bypass -File .\Build-SBV-Windows.ps1

3. The script installs/updates Git, Go, Node.js LTS and MSYS2 as needed.
4. It installs the UCRT64 GCC/pkg-config/libheif packages inside MSYS2.
5. It downloads the current SBV source, builds the React frontend, builds sbv.exe with:

      -tags "fts5 heic"

6. It copies libheif and dependent UCRT64 DLLs into the portable output.
7. It creates SBV-Windows-Portable.zip beside this script.

Why the dependency bundle matters
---------------------------------
SBV's HEIC code uses github.com/lowcarbdev/libheif-go. On Windows this requires the
native libheif library. The script deliberately packages the runtime DLL dependency
chain rather than assuming libheif is installed globally on the destination PC.

Important
---------
- This builder itself does NOT contain an SBV binary or libheif binary. It downloads
  and builds them on your own Windows machine, then packages the runtime DLLs.
- Keep the original SMS Backup & Restore XML as your archival copy.
- A 4 GB XML may require considerably more than 4 GB free disk space during import.
- The generated launcher binds the modified SBV build to 127.0.0.1 (localhost).
