#Requires -Version 5.1
<#
  Android SMB Relay - Windows 连接工具（完整诊断 + 映射）

  用法：
    powershell -ExecutionPolicy Bypass -File .\windows-connect.ps1 -PhoneIp 192.168.1.223
    powershell -ExecutionPolicy Bypass -File .\windows-connect.ps1 -PhoneIp 192.168.1.223 -User android
    powershell -ExecutionPolicy Bypass -File .\windows-connect.ps1 -Drive Z -Unmount

  说明：
  - 先尝试匿名；Windows 11 24H2 的 Pro/Enterprise 默认强制 SMB 签名，
    匿名 guest 会话通常无法协商签名，会失败。此时工具提示输入 SMB 用户名/密码。
  - 账号映射使用普通权限进程，避免 UAC 提权后映射在资源管理器不可见。
  - 不修改系统 SMB 签名策略，不停止 Workstation 服务。
#>
[CmdletBinding()]
param(
  [string]$PhoneIp = $env:ASR_PHONE_IP,
  [string]$Share = "Internal",
  [string]$Drive = "Z",
  [string]$User = "android",
  [string]$Password = "admin",
  [switch]$Anonymous,
  [switch]$Remember,
  [switch]$Unmount,
  [switch]$NoElevate
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

function Normalize-Drive([string]$Value) {
  $letter = $Value.Trim().TrimEnd(":").ToUpperInvariant()
  if ($letter -notmatch '^[A-Z]$') { Fail "驱动器盘符必须是单个字母，例如 Z。" }
  return $letter
}

function Test-Port445 {
  return (Test-NetConnection -ComputerName $PhoneIp -Port 445 -InformationLevel Quiet -WarningAction SilentlyContinue)
}

function Test-GuestPolicy {
  $path = "HKLM:\SYSTEM\CurrentControlSet\Services\LanmanWorkstation\Parameters"
  $value = (Get-ItemProperty -Path $path -Name AllowInsecureGuestAuth -ErrorAction SilentlyContinue).AllowInsecureGuestAuth
  return ($null -ne $value -and [int]$value -eq 1)
}

function Ensure-GuestPolicy {
  if (Test-GuestPolicy) { return }
  if ($NoElevate) {
    Write-Warn "Windows 未允许 guest SMB；已指定 -NoElevate，跳过自动提权。"
    return
  }
  $helper = Join-Path $PSScriptRoot "windows-allow-guest.ps1"
  if (-not (Test-Path $helper)) {
    Write-Warn "缺少 windows-allow-guest.ps1，无法自动开启 guest；将直接尝试账号模式。"
    return
  }
  if (Test-Admin) {
    & $helper
    return
  }
  Write-Info "权限不足：正在请求管理员权限以允许 guest SMB..."
  $exe = Join-Path $PSHOME "powershell.exe"
  $args = @("-NoProfile", "-ExecutionPolicy", "Bypass", "-File", ('"' + $helper + '"'))
  try {
    Start-Process -FilePath $exe -ArgumentList $args -Verb RunAs -Wait | Out-Null
  } catch {
    Write-Warn "已取消管理员授权；继续尝试账号模式。"
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

if (-not $PhoneIp) { $PhoneIp = Read-Host "手机 IP（WebUI 概览/网络页显示）" }
if ($PhoneIp -notmatch '^([0-9]{1,3}\.){3}[0-9]{1,3}$') { Fail "IPv4 地址无效：$PhoneIp" }
$remote = "\\$PhoneIp\$Share"

Write-Info "Windows SMB 连接诊断"
Write-Info "  手机：$PhoneIp"
Write-Info "  共享：$remote"
Write-Info "  系统：$([Environment]::OSVersion.Version)"
$config = Get-SmbClientConfiguration -ErrorAction SilentlyContinue
if ($config) {
  Write-Info "  客户端签名要求：$($config.RequireSecuritySignature)；允许 guest：$($config.EnableInsecureGuestLogons)"
}
if (-not (Test-Port445)) { Fail "TCP 445 不可达。检查 Wi-Fi、手机 SMB 服务和 Windows 防火墙。" }
Write-Ok "TCP 445 可达"

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

function Try-Credentials([string]$Name, [string]$Password) {
  Remove-TargetMappings
  try {
    New-SmbMapping -LocalPath $local -RemotePath $remote -UserName $Name -Password $Password -ErrorAction Stop | Out-Null
    return $true
  } catch {
    cmd /c "net use $local `"$remote`" /user:$Name `"$Password`" /persistent:no" | Out-Null
    return ($LASTEXITCODE -eq 0)
  }
}

$mapped = $false
if ($Anonymous) {
  $User = ""
  Write-Info "只尝试匿名访问..."
  Ensure-GuestPolicy
  $mapped = Try-Anonymous
} else {
  if (-not $User) { $User = "android" }
  if (-not $Password) { $Password = "admin" }
  Write-Info "使用 SMB 账号 $User 连接..."
  $mapped = Try-Credentials $User $Password
  if ($mapped -and $Remember) {
    cmd /c "cmdkey /add:$PhoneIp /user:$User /pass:`"$Password`"" | Out-Null
    Write-Ok "已把凭据保存到 Windows 凭据管理器（仅这台电脑）。"
  }
}

if (-not $mapped) {
  Write-Warn "自动连接失败；如果手机端改过账号，可执行："
  Write-Warn "  windows-connect.ps1 -PhoneIp $PhoneIp -User android -Password admin"
  Fail "挂载失败。请确认共享名“$Share”、账号“$User”和密码。"
}

if ($mapped -and (Test-Path "$local\")) {
  Write-Ok "已挂载 $remote -> $local"
  Write-Host "打开：$local\"
  Write-Host "卸载：powershell -ExecutionPolicy Bypass -File `"$PSCommandPath`" -Drive $letter -Unmount"
} else {
  Fail "挂载失败。请确认共享名“$Share”、SMB 账号密码，以及手机 WebUI 中该账号已启用。"
}
