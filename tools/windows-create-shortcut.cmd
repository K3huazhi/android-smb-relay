@echo off
setlocal
REM Create a desktop shortcut for Android SMB Relay.
powershell -NoProfile -ExecutionPolicy Bypass -File "%~dp0windows-create-shortcut.ps1"
if errorlevel 1 pause
endlocal
