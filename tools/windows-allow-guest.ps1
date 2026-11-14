#Requires -Version 5.1
<#
  Enable/disable Windows insecure guest SMB authentication (needed for anonymous
  SMB on Windows 10/11).

  Double-click or run normally: if not elevated, the script re-launches itself
  through UAC automatically and waits for the result.

  Security note: this is a PC-wide setting. It allows guest access to any SMB
  server that asks for it. Disable with -Disable when you no longer need it.
#>
[CmdletBinding()]
param([switch]$Disable)

$ErrorActionPreference = "Stop"
$RegPath = "HKLM:\SYSTEM\CurrentControlSet\Services\LanmanWorkstation\Parameters"

function Test-Admin {
  $identity = [Security.Principal.WindowsIdentity]::GetCurrent()
  $principal = New-Object Security.Principal.WindowsPrincipal($identity)
  return $principal.IsInRole([Security.Principal.WindowsBuiltInRole]::Administrator)
}

function Get-PowerShellExe {
  $exe = Join-Path $PSHOME "powershell.exe"
  if (Test-Path $exe) { return $exe }
  $cmd = Get-Command powershell.exe -ErrorAction SilentlyContinue
  if ($cmd) { return $cmd.Source }
  throw "找不到 powershell.exe，无法请求管理员权限。"
}

if (-not (Test-Admin)) {
  Write-Host "权限不足，正在请求管理员权限..." -ForegroundColor Cyan
  $exe = Get-PowerShellExe
  $quotedSelf = '"' + $PSCommandPath + '"'
  $argList = @("-NoProfile", "-ExecutionPolicy", "Bypass", "-File", $quotedSelf)
  if ($Disable) { $argList += "-Disable" }
  try {
    $proc = Start-Process -FilePath $exe -ArgumentList $argList -Verb RunAs -Wait -PassThru
    exit $proc.ExitCode
  } catch {
    Write-Host "已取消管理员授权，设置未修改。" -ForegroundColor Red
    exit 2
  }
}

$value = if ($Disable) { 0 } else { 1 }
New-Item -Path $RegPath -Force | Out-Null
Set-ItemProperty -Path $RegPath -Name AllowInsecureGuestAuth -Value $value -Type DWord
if ($Disable) {
  Write-Host "AllowInsecureGuestAuth = 0（已关闭访客 SMB）。" -ForegroundColor Yellow
} else {
  Write-Host "AllowInsecureGuestAuth = 1（已允许匿名 SMB）。" -ForegroundColor Green
}
