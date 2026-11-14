#Requires -Version 5.1
<#
  Create an "Android SMB Relay" shortcut on the current user's desktop.
  The shortcut runs windows-mount.cmd, which asks for the phone IP and maps
  the anonymous Internal share to Z: without a password.
#>
[CmdletBinding()]
param(
  [string]$Name = "Android SMB Relay",
  [string]$Drive = "Z"
)

$ErrorActionPreference = "Stop"
$root = Split-Path -Parent $MyInvocation.MyCommand.Path
$target = Join-Path $root "windows-connect.cmd"
if (-not (Test-Path $target)) { throw "windows-connect.cmd not found next to this script: $target" }

$desktop = [Environment]::GetFolderPath("Desktop")
$linkPath = Join-Path $desktop "$Name.lnk"
$shell = New-Object -ComObject WScript.Shell
$shortcut = $shell.CreateShortcut($linkPath)
$shortcut.TargetPath = $target
$shortcut.WorkingDirectory = $root
$shortcut.Description = "Android SMB Relay - 匿名挂载手机 Internal 共享"
$shortcut.IconLocation = "$env:SystemRoot\System32\shell32.dll,17"
$shortcut.Save()

Write-Host "已创建桌面快捷方式：" -ForegroundColor Green
Write-Host "  $linkPath"
Write-Host "双击后输入手机 IP（WebUI 顶部/Network 页显示），即可匿名挂载到 $Drive`:。"
