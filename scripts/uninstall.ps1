# Removes the tgsync task and binary. Configuration and database stay.
# Saved as UTF-8 with BOM: Windows PowerShell 5.1 reads files without BOM as ANSI.
$ErrorActionPreference = 'Stop'
$TaskName = 'tgsync'
$BinDir = Join-Path $env:LOCALAPPDATA 'tgsync\bin'
$Bin = Join-Path $BinDir 'tgsync.exe'

# Stop-ScheduledTask ends only the powershell.exe wrapper of the task: its
# tgsync.exe child keeps running (and keeps the binary locked), and a wrapper
# that survived would start tgsync again. Stop both, then wait until the
# binary can be removed. Same as in install.ps1.
function Stop-Node([string]$Bin) {
    $quoted = $Bin.Replace("'", "''")
    Get-CimInstance Win32_Process -Filter "Name = 'powershell.exe'" -ErrorAction SilentlyContinue |
        Where-Object { $_.ProcessId -ne $PID -and $_.CommandLine -and $_.CommandLine.Contains($quoted) } |
        ForEach-Object { Stop-Process -Id $_.ProcessId -Force -ErrorAction SilentlyContinue }
    $procs = @(Get-Process -Name tgsync -ErrorAction SilentlyContinue | Where-Object { $_.Path -eq $Bin })
    $procs | Stop-Process -Force -ErrorAction SilentlyContinue
    $procs | Wait-Process -Timeout 15 -ErrorAction SilentlyContinue
    if (-not (Test-Path $Bin)) { return $true }
    for ($i = 0; $i -lt 30; $i++) {
        try { [System.IO.File]::Open($Bin, 'Open', 'ReadWrite', 'None').Dispose(); return $true }
        catch { Start-Sleep -Milliseconds 500 }
    }
    return $false
}

Stop-ScheduledTask -TaskName $TaskName -ErrorAction SilentlyContinue
Unregister-ScheduledTask -TaskName $TaskName -Confirm:$false -ErrorAction SilentlyContinue
$unlocked = Stop-Node $Bin
Remove-Item $BinDir -Recurse -Force -ErrorAction SilentlyContinue
if (-not $unlocked -or (Test-Path $Bin)) {
    Write-Host "! Не удалось удалить ${Bin}: файл занят. Закрой tgsync и удали папку вручную."
}
Write-Host "Задача удалена. Конфиг и база остались в $(Join-Path $env:APPDATA 'tgsync')"
