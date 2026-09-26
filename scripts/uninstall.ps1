# Removes the tgsync task and binary. Configuration and database stay.
# Saved as UTF-8 with BOM: Windows PowerShell 5.1 reads files without BOM as ANSI.
$ErrorActionPreference = 'Stop'
$TaskName = 'tgsync'
Stop-ScheduledTask -TaskName $TaskName -ErrorAction SilentlyContinue
Unregister-ScheduledTask -TaskName $TaskName -Confirm:$false -ErrorAction SilentlyContinue
Get-Process -Name tgsync -ErrorAction SilentlyContinue | Stop-Process -Force
Remove-Item (Join-Path $env:LOCALAPPDATA 'tgsync\bin') -Recurse -Force -ErrorAction SilentlyContinue
Write-Host "Задача удалена. Конфиг и база остались в $(Join-Path $env:APPDATA 'tgsync')"
