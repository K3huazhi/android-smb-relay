@echo off
setlocal
REM Android SMB Relay - Windows quick mount (double-click friendly).
set "PHONE_IP=%~1"
set "SHARE=%~2"
if "%SHARE%"=="" set "SHARE=Internal"
if "%PHONE_IP%"=="" set /p "PHONE_IP=Phone IP (WebUI): "
powershell -NoProfile -ExecutionPolicy Bypass -File "%~dp0windows-mount.ps1" -PhoneIp "%PHONE_IP%" -Share "%SHARE%"
if errorlevel 1 pause
endlocal
