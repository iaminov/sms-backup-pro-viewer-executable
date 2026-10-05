@echo off
setlocal
cd /d "%~dp0"
set "DB_PATH_PREFIX=%~dp0data"
set "PORT=8085"
set "LIBHEIF_PLUGIN_PATH=%~dp0libheif-plugins;%~dp0"
if not exist "%~dp0data" mkdir "%~dp0data"
if not exist "%~dp0media" mkdir "%~dp0media"
start "" http://127.0.0.1:8085
"%~dp0sbv.exe"
endlocal
