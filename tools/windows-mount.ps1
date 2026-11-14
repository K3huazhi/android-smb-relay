#Requires -Version 5.1
<#
  Android SMB Relay - Windows quick mount (anonymous by default).

  Normal use:
    powershell -ExecutionPolicy Bypass -File .\windows-mount.ps1 -PhoneIp 192.168.1.223
  Or double-click windows-mount.cmd.

  The module defaults to anonymous access, so no password is requested. If you
  later turn authentication back on, pass -User android and the script will
  prompt securely. The password is never written to disk.

  If Windows blocks guest SMB, the script requests administrator permission
  automatically (UAC) and enables AllowInsecureGuestAuth. Use -NoElevate to
  refuse automatic elevation and print manual instructions instead.
#>
[CmdletBinding()]
param(
  [string]$PhoneIp = $env:ASR_PHONE_IP,
  [string]$Share = "Internal",
  [string]$Drive = "Z",
  [string]$User = "",
  [switch]$Unmount,
  [switch]$ListOnly,
  [switch]$EnableGuestAuth,
  [switch]$NoElevate
)

$ErrorActionPreference = "Stop"
$RegPath = "HKLM:\SYSTEM\CurrentControlSet\Services\LanmanWorkstation\Parameters"

function Write-Info([string]$Message) { Write-Host $Message -ForegroundColor Cyan }
function Write-Ok([string]$Message) { Write-Host $Message -ForegroundColor Green }
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

function Test-GuestPolicy {
  $value = (Get-ItemProperty -Path $RegPath -Name AllowInsecureGuestAuth -ErrorAction SilentlyContinue).AllowInsecureGuestAuth
  return ($null -ne $value -and [int]$value -eq 1)
}

function Get-PowerShellExe {
  $exe = Join-Path $PSHOME "powershell.exe"
  if (Test-Path $exe) { return $exe }
  $cmd = Get-Command powershell.exe -ErrorAction SilentlyContinue
  if ($cmd) { return $cmd.Source }
  throw "找不到 powershell.exe，无法请求管理员权限。"
}

function Enable-GuestAuthElevated {
  $helper = Join-Path $PSScriptRoot "windows-allow-guest.ps1"
  if (-not (Test-Path $helper)) {
    Fail "缺少 windows-allow-guest.ps1，无法自动开启访客 SMB。请把两个脚本放在同一文件夹。"
  }

  # Already elevated: run the helper directly (no second UAC prompt).
  if (Test-Admin) {
    & $helper
    if (-not (Test-GuestPolicy)) { Fail "开启访客 SMB 失败，请手动以管理员身份运行 windows-allow-guest.ps1。" }
    return
  }

  Write-Info "权限不足：正在请求管理员权限以开启 Windows 访客 SMB..."
  $exe = Get-PowerShellExe
  $quotedHelper = '"' + $helper + '"'
  $argList = @("-NoProfile", "-ExecutionPolicy", "Bypass", "-File", $quotedHelper)
  try {
    $proc = Start-Process -FilePath $exe -ArgumentList $argList -Verb RunAs -Wait -PassThru
  } catch {
    Fail "已取消管理员授权或 UAC 不可用。请右键以管理员身份运行 windows-allow-guest.ps1，或使用 -NoElevate 查看手动步骤。"
  }
  if ($proc.ExitCode -ne 0) {
    Fail "管理员授权已取消，访客 SMB 未开启。"
  }
  if (-not (Test-GuestPolicy)) {
    Fail "提权后仍未检测到 AllowInsecureGuestAuth=1，请手动运行 windows-allow-guest.ps1。"
  }
}

$letter = Normalize-Drive $Drive

if ($Unmount) {
  $local = "${letter}:"
  try { Remove-SmbMapping -LocalPath $local -Force -ErrorAction Stop | Out-Null }
  catch { cmd /c "net use $local /delete /y" | Out-Null }
  Write-Ok "已卸载 $local"
  exit 0
}

if (-not $PhoneIp) { $PhoneIp = Read-Host "手机 IP 地址（WebUI 概览/网络页显示）" }
if ($PhoneIp -notmatch '^([0-9]{1,3}\.){3}[0-9]{1,3}$') { Fail "IPv4 地址无效：$PhoneIp" }

$remote = "\\$PhoneIp\$Share"
Write-Info "手机：$PhoneIp"
Write-Info "共享：$remote"

if ($ListOnly) {
  if (-not (Test-NetConnection -ComputerName $PhoneIp -Port 445 -InformationLevel Quiet -WarningAction SilentlyContinue)) {
    Fail "TCP 445 不可达。请检查 Wi-Fi 和 SMB 服务状态。"
  }
  Write-Ok "TCP 445 可达"
  Write-Host "可尝试：$remote"
  exit 0
}

if (-not (Test-NetConnection -ComputerName $PhoneIp -Port 445 -InformationLevel Quiet -WarningAction SilentlyContinue)) {
  Fail "TCP 445 不可达。请检查 Wi-Fi 和 SMB 服务状态。"
}

# Guest policy only matters for anonymous access; authenticated mode skips it.
if (-not $User -and -not (Test-GuestPolicy)) {
  if ($NoElevate) {
    Write-Host ""
    Write-Host "Windows 当前阻止匿名 SMB，且已指定 -NoElevate。" -ForegroundColor Yellow
    Write-Host "请以管理员身份运行 windows-allow-guest.ps1，然后重试。" -ForegroundColor Yellow
    exit 2
  }
  Enable-GuestAuthElevated
}

$mapped = $false
if ($User) {
  $secure = $null
  if ($env:ASR_SMB_PASSWORD) {
    $secure = ConvertTo-SecureString $env:ASR_SMB_PASSWORD -AsPlainText -Force
  } else {
    $secure = Read-Host -AsSecureString "SMB 用户 $User 的密码"
  }
  $bstr = [Runtime.InteropServices.Marshal]::SecureStringToBSTR($secure)
  try {
    $plain = [Runtime.InteropServices.Marshal]::PtrToStringBSTR($bstr)
    try {
      New-SmbMapping -LocalPath "${letter}:" -RemotePath $remote -UserName $User -Password $plain -ErrorAction Stop | Out-Null
      $mapped = $true
    } catch {
      cmd /c "net use ${letter}: `"$remote`" /user:$User `"$plain`" /persistent:no" | Out-Null
      $mapped = ($LASTEXITCODE -eq 0)
    }
  } finally {
    if ($bstr -ne [IntPtr]::Zero) { [Runtime.InteropServices.Marshal]::ZeroFreeBSTR($bstr) }
    $plain = $null
    $secure = $null
  }
} else {
  try {
    New-SmbMapping -LocalPath "${letter}:" -RemotePath $remote -ErrorAction Stop | Out-Null
    $mapped = $true
  } catch {
    cmd /c "net use ${letter}: `"$remote`" /persistent:no" | Out-Null
    $mapped = ($LASTEXITCODE -eq 0)
  }
}

if ($mapped -and (Test-Path "${letter}:\")) {
  Write-Ok "已把 $remote 挂载到 ${letter}:"
  Write-Host "打开：${letter}:\"
  Write-Host "UNC： $remote"
  Write-Host "卸载：powershell -ExecutionPolicy Bypass -File `"$PSCommandPath`" -Drive $letter -Unmount"
} else {
  Fail "挂载失败。若本机阻止访客 SMB，请以管理员身份运行 windows-allow-guest.ps1，或检查 445/共享名。"
}
