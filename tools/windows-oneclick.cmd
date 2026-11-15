@echo off
setlocal
set "PHONE_IP=%~1"
if "%PHONE_IP%"=="" set /p "PHONE_IP=Phone IP (e.g. 192.168.1.223): "
if "%PHONE_IP%"=="" exit /b 1
powershell -NoProfile -ExecutionPolicy Bypass -File "%~dp0windows-oneclick.ps1" -PhoneIp "%PHONE_IP%"
if errorlevel 1 pause
endlocal
