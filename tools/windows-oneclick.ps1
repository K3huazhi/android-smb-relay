#Requires -Version 5.1
<#
  Android SMB Relay - Windows 一键连接

  只需输入手机 IP。脚本会：
  1. 自动请求 UAC，开启 Windows 来宾 SMB 并关闭客户端签名要求；
  2. 尝试匿名挂载；
  3. Windows 11 24H2 Pro/Enterprise 已禁用 guest 回退时，自动创建/使用
     手机端 android/admin 账号并映射，不需要用户输入密码。

  用法：
    windows-oneclick.cmd 192.168.1.223
    powershell -ExecutionPolicy Bypass -File .\windows-oneclick.ps1 -PhoneIp 192.168.1.223
#>
[CmdletBinding()]
param(
  [string]$PhoneIp = $env:ASR_PHONE_IP,
  [string]$Share = "Internal",
  [string]$Drive = "Z",
  [switch]$AnonymousOnly,
  [switch]$Unmount
)

$ErrorActionPreference = "Stop"

function Write-Info([string]$Message) { Write-Host $Message -ForegroundColor Cyan }
function Write-Ok([string]$Message) { Write-Host $Message -ForegroundColor Green }
function Write-Warn([string]$Message) { Write-Host $Message -ForegroundColor Yellow }
function Fail([string]$Message) { Write-Host $Message -ForegroundColor Red; exit 1 }

function Test-Admin {
  $identity = [Security.Principal.WindowsIdentity]::GetCurrent()
  $principal = New-Object Security.Principal.WindowsPrincipal($identity)
  return $principal.IsInRole([Security.Principal.WindowsBuiltInRole]::Administrator)
}

function Test-Port445 {
  return (Test-NetConnection -ComputerName $PhoneIp -Port 445 -InformationLevel Quiet -WarningAction SilentlyContinue)
}

function Normalize-Drive([string]$Value) {
  $letter = $Value.Trim().TrimEnd(":").ToUpperInvariant()
  if ($letter -notmatch '^[A-Z]$') { Fail "驱动器盘符必须是单个字母。" }
  return $letter
}

function Invoke-Policy([string]$Mode) {
  $helper = Join-Path $PSScriptRoot "windows-oneclick-policy.ps1"
  if (-not (Test-Path $helper)) { Fail "缺少 windows-oneclick-policy.ps1。" }
  if (Test-Admin) {
    & $helper -Mode $Mode
    return
  }
  Write-Info "正在请求管理员权限（$Mode 策略）..."
  $exe = Join-Path $PSHOME "powershell.exe"
  $args = @("-NoProfile", "-ExecutionPolicy", "Bypass", "-File", ('"' + $helper + '"'), "-Mode", $Mode)
  try {
    $p = Start-Process -FilePath $exe -ArgumentList $args -Verb RunAs -Wait -PassThru
  } catch {
    Fail "已取消管理员授权，无法修改 Windows SMB 策略。"
  }
  if ($p.ExitCode -ne 0) { Fail "配置 Windows SMB 策略失败。" }
}

function Remove-TargetMappings {
  try {
    Get-SmbMapping -ErrorAction SilentlyContinue | Where-Object { $_.RemotePath -like "\\$PhoneIp\*" } | ForEach-Object {
      Remove-SmbMapping -LocalPath $_.LocalPath -Force -ErrorAction SilentlyContinue
    }
  } catch { }
}

function Try-Anonymous {
  Remove-TargetMappings
  try {
    New-SmbMapping -LocalPath $local -RemotePath $remote -ErrorAction Stop | Out-Null
    return $true
  } catch {
    cmd /c "net use $local `"$remote`" /persistent:no" | Out-Null
    return ($LASTEXITCODE -eq 0)
  }
}

function Ensure-PhoneAccount {
  # Latest WebUI has auth disabled, so this is a local-LAN one-time setup.
  try {
    $status = Invoke-RestMethod -Uri "http://${PhoneIp}:8080/api/auth/status" -TimeoutSec 3
    if ($status.auth_required) { return }
    $body = @{ name = "android"; password = "admin"; enabled = $true } | ConvertTo-Json
    try {
      Invoke-RestMethod -Uri "http://${PhoneIp}:8080/api/users" -Method Post -Body $body -ContentType "application/json" -TimeoutSec 5 | Out-Null
    } catch {
      # 409/400 usually means the account already exists; mapping will verify it.
    }
  } catch {
    # Phone API may be unavailable; still try the built-in account.
  }
}

function Try-Account {
  Remove-TargetMappings
  try {
    New-SmbMapping -LocalPath $local -RemotePath $remote -UserName "android" -Password "admin" -ErrorAction Stop | Out-Null
    return $true
  } catch {
    cmd /c "net use $local `"$remote`" /user:android admin /persistent:no" | Out-Null
    return ($LASTEXITCODE -eq 0)
  }
}

$letter = Normalize-Drive $Drive
$local = "${letter}:"

if ($Unmount) {
  try { Remove-SmbMapping -LocalPath $local -Force -ErrorAction Stop | Out-Null }
  catch { cmd /c "net use $local /delete /y" | Out-Null }
  Write-Ok "已卸载 $local"
  exit 0
}

if (-not $PhoneIp) { $PhoneIp = Read-Host "手机 IP（例如 192.168.1.223）" }
if ($PhoneIp -notmatch '^([0-9]{1,3}\.){3}[0-9]{1,3}$') { Fail "IPv4 地址无效：$PhoneIp" }
$remote = "\\$PhoneIp\$Share"

Write-Info "手机：$PhoneIp"
Write-Info "共享：$remote"
if (Test-Admin) { Write-Warn "当前是管理员窗口；映射的盘符可能不会出现在普通资源管理器。建议从桌面普通双击运行。" }
if (-not (Test-Port445)) { Fail "TCP 445 不可达。检查 Wi-Fi、手机 SMB 服务和防火墙。" }
Write-Ok "TCP 445 可达"

# 1) Configure anonymous policy and try guest.
if (-not $AnonymousOnly) {
  Invoke-Policy "Anonymous"
  Write-Info "尝试匿名访问..."
  if (Try-Anonymous) {
    Write-Ok "匿名挂载成功：$remote -> $local"
    exit 0
  }
  Write-Warn "匿名失败。Windows 11 24H2 Pro/Enterprise 已移除 guest 回退，改用 android/admin 账号。"
} else {
  Invoke-Policy "Anonymous"
  if (Try-Anonymous) { Write-Ok "匿名挂载成功：$remote -> $local"; exit 0 }
  Fail "匿名挂载失败（Windows 11 24H2 Pro/Enterprise 默认禁用 guest）。"
}

# 2) Account fallback: Windows 24H2 requires signing, restore client signing.
Invoke-Policy "Account"
Ensure-PhoneAccount
Write-Info "使用兼容账号 android / admin 连接..."
if (Try-Account) {
  Write-Ok "挂载成功：$remote -> $local"
  exit 0
}

Fail @"
挂载失败。
- 请确认已安装最新版 0.1.3（修复 samba-dcerpcd / 0xC000020C）。
- 请确认手机 WebUI 可访问 http://${PhoneIp}:8080。
- 首次修改 SMB 策略后，Windows 可能需要重启一次。
- 若仍失败，请把本窗口完整输出发回。
"@
