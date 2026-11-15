#Requires -Version 5.1
<#
  Android SMB Relay - one-time Windows client policy helper.
  Must run elevated (main script requests UAC automatically).

  Anonymous mode: allow insecure guest + disable SMB client signing.
  Account mode:  allow insecure guest + restore SMB client signing so the
                 Windows 11 24H2 account login can sign.
#>
[CmdletBinding()]
param(
  [ValidateSet("Anonymous", "Account")]
  [string]$Mode = "Anonymous"
)

$ErrorActionPreference = "Continue"
$params = "HKLM:\SYSTEM\CurrentControlSet\Services\LanmanWorkstation\Parameters"
$policy = "HKLM:\SOFTWARE\Policies\Microsoft\Windows\LanmanWorkstation"
$lsa = "HKLM:\SYSTEM\CurrentControlSet\Control\Lsa"

function Set-Dword([string]$Path, [string]$Name, [int]$Value) {
  if (-not (Test-Path $Path)) { New-Item -Path $Path -Force | Out-Null }
  Set-ItemProperty -Path $Path -Name $Name -Value $Value -Type DWord
}

Set-Dword $params "AllowInsecureGuestAuth" 1
Set-Dword $policy "AllowInsecureGuestAuth" 1

if ($Mode -eq "Anonymous") {
  Set-Dword $params "RequireSecuritySignature" 0
  Set-Dword $params "EnableSecuritySignature" 0
  Set-Dword $lsa "LmCompatibilityLevel" 1
} else {
  Set-Dword $params "RequireSecuritySignature" 1
  Set-Dword $params "EnableSecuritySignature" 1
}

# Use the supported cmdlet when available; registry values above are the
# fallback for systems where the cmdlet or SMB service is unavailable.
if (Get-Command Set-SmbClientConfiguration -ErrorAction SilentlyContinue) {
  try {
    if ($Mode -eq "Anonymous") {
      Set-SmbClientConfiguration -RequireSecuritySignature $false -EnableSecuritySignature $false -EnableInsecureGuestLogons $true -Force | Out-Null
    } else {
      Set-SmbClientConfiguration -RequireSecuritySignature $true -EnableSecuritySignature $true -EnableInsecureGuestLogons $true -Force | Out-Null
    }
  } catch {
    Write-Warning "Set-SmbClientConfiguration 失败（继续使用注册表值）：$($_.Exception.Message)"
  }
}

Write-Host "Windows SMB client policy: $Mode" -ForegroundColor Green
