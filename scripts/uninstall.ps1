# Removes the tgsync task and binary. Configuration and database stay.
# Saved as UTF-8 with BOM: Windows PowerShell 5.1 reads files without BOM as ANSI.
$ErrorActionPreference = 'Stop'
$TaskName = 'tgsync'
$BinDir = Join-Path $env:LOCALAPPDATA 'tgsync\bin'
$Bin = Join-Path $BinDir 'tgsync.exe'
$Conf = Join-Path $env:APPDATA 'tgsync'

# Interface language of the messages below: BOT_LANGUAGE from the environment,
# else the last BOT_LANGUAGE= line of the node's .env, else en.
function Get-UiLang([string[]]$Files) {
    $v = $env:BOT_LANGUAGE
    foreach ($f in $Files) {
        if ($v) { break }
        if (Test-Path -LiteralPath $f -PathType Leaf) {
            $line = Get-Content -LiteralPath $f -ErrorAction SilentlyContinue |
                Where-Object { $_ -match '^\s*(export\s+)?BOT_LANGUAGE\s*=' } | Select-Object -Last 1
            if ($line) { $v = (($line -replace '^[^=]*=', '') -replace '#.*', '') -replace "[\s`"']", '' }
        }
    }
    if ($v -and $v.Trim() -ieq 'ru') { 'ru' } else { 'en' }
}
# Msg "English" "Русский" prints the line in the interface language.
function Msg([string]$En, [string]$Ru) {
    if ($script:UiLang -eq 'ru') { Write-Host $Ru } else { Write-Host $En }
}
$UiLang = Get-UiLang @(Join-Path $Conf '.env')

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
    Msg "! Could not remove ${Bin}: the file is in use. Close tgsync and delete the folder manually." `
        "! Не удалось удалить ${Bin}: файл занят. Закрой tgsync и удали папку вручную."
}
Msg "Task removed. Configuration and database remain in $Conf" "Задача удалена. Конфиг и база остались в $Conf"
