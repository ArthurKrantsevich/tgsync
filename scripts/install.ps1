# Installs tgsync as a Task Scheduler task for the current user. Safe to run again to update.
# Run: powershell -ExecutionPolicy Bypass -File scripts\install.ps1
# Saved as UTF-8 with BOM: Windows PowerShell 5.1 reads files without BOM as ANSI.
$ErrorActionPreference = 'Stop'

$Repo = Split-Path -Parent $PSScriptRoot
$BinDir = Join-Path $env:LOCALAPPDATA 'tgsync\bin'
$Bin = Join-Path $BinDir 'tgsync.exe'
$Conf = Join-Path $env:APPDATA 'tgsync'
$Log = Join-Path $Conf 'tgsync.log'
$TaskName = 'tgsync'

# Interface language of the messages below: BOT_LANGUAGE from the environment,
# else the last BOT_LANGUAGE= line of the first given .env that sets it, else en.
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
# The node's .env wins; before the first install it may still sit in the repository.
$UiLang = Get-UiLang @((Join-Path $Conf '.env'), (Join-Path $Repo '.env'))

# Another tgsync (for example bin\tgsync.exe run in a terminal) would poll with
# the same token (409 Conflict).
$other = Get-Process -Name tgsync -ErrorAction SilentlyContinue | Where-Object { $_.Path -ne $Bin }
if ($other) {
    Msg "! Another tgsync process is running (pid $($other.Id -join ', ')). Stop it and run the install again." `
        "! Запущен другой процесс tgsync (pid $($other.Id -join ', ')). Останови его и запусти установку ещё раз."
    exit 1
}

# Stop-ScheduledTask ends only the powershell.exe wrapper of the task: its
# tgsync.exe child keeps running (and keeps the binary locked), and a wrapper
# that survived would start tgsync again. Stop both, then wait until the
# binary can be replaced.
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
if (-not (Stop-Node $Bin)) {
    Msg "! $Bin is in use by another process. Close it and run the install again." `
        "! $Bin занят другим процессом. Закрой его и запусти установку ещё раз."
    exit 1
}

New-Item -ItemType Directory -Force -Path $BinDir, (Join-Path $Conf 'data') | Out-Null
if ((Get-Command go -ErrorAction SilentlyContinue) -and (Test-Path (Join-Path $Repo 'go.mod'))) {
    Msg "→ building $Bin" "→ сборка $Bin"
    Push-Location $Repo
    # Stamp the version the way release builds do.
    $Version = $null
    # try: Windows PowerShell 5.1 turns git's stderr into a terminating error under Stop.
    try { $Version = git -C $Repo describe --tags --always --dirty 2>$null } catch { }
    if (-not $Version) { $Version = "dev" }
    try { go build -ldflags "-X main.version=$Version" -o $Bin ./cmd/tgsync } finally { Pop-Location }
    if ($LASTEXITCODE -ne 0) { exit 1 }
} elseif (Test-Path (Join-Path $Repo 'tgsync.exe')) {
    # Release archive: the binary sits next to scripts\.
    Copy-Item (Join-Path $Repo 'tgsync.exe') $Bin -Force
} else {
    Msg '! Neither Go nor tgsync.exe next to the script was found.' '! Нет Go и нет tgsync.exe рядом со скриптом.'
    exit 1
}

$envFile = Join-Path $Conf '.env'
if (-not (Test-Path $envFile)) {
    if (Test-Path (Join-Path $Repo '.env')) {
        # Moved, not copied: a second copy of the token must not stay in a project folder.
        Move-Item (Join-Path $Repo '.env') $envFile
        Msg "→ .env moved to $Conf" "→ .env перенесён в $Conf"
    } else {
        Copy-Item (Join-Path $Repo '.env.example') $envFile
        Msg "Fill in $envFile and run the install again." "Заполни $envFile и запусти установку ещё раз."
        exit 1
    }
}

Msg '→ checking the setup' '→ проверка установки'
$env:TGSYNC_HOME = $Conf
Push-Location $Conf
try { & $Bin check } finally { Pop-Location; Remove-Item Env:TGSYNC_HOME }
if ($LASTEXITCODE -ne 0) {
    Msg 'Fix the errors above and run the install again.' 'Исправь ошибки выше и запусти установку ещё раз.'
    exit 1
}

# PowerShell starts tgsync with its window hidden and passes the log file.
# Task Scheduler restarts a task only when it fails to launch, not when it
# exits with an error, so the loop restarts tgsync after a crash.
# Single quotes inside paths are doubled for the PowerShell string.
function Quote([string]$s) { "'" + $s.Replace("'", "''") + "'" }
$command = "`$env:TGSYNC_HOME=$(Quote $Conf); `$env:TGSYNC_LOG=$(Quote $Log); " +
    "while (`$true) { & $(Quote $Bin) run; if (`$LASTEXITCODE -eq 0) { break }; Start-Sleep 5 }"
$action = New-ScheduledTaskAction -Execute 'powershell.exe' `
    -Argument "-NoProfile -NonInteractive -WindowStyle Hidden -Command `"$command`"" `
    -WorkingDirectory $Conf
$trigger = New-ScheduledTaskTrigger -AtLogOn -User $env:USERNAME
$settings = New-ScheduledTaskSettingsSet -AllowStartIfOnBatteries -DontStopIfGoingOnBatteries `
    -ExecutionTimeLimit ([TimeSpan]::Zero) -RestartCount 999 -RestartInterval (New-TimeSpan -Minutes 1) `
    -MultipleInstances IgnoreNew
Register-ScheduledTask -TaskName $TaskName -Action $action -Trigger $trigger -Settings $settings `
    -Description 'tgsync: Claude Code over Telegram' -Force | Out-Null
Start-ScheduledTask -TaskName $TaskName
Msg "→ task started: Get-ScheduledTask $TaskName" "→ задача запущена: Get-ScheduledTask $TaskName"
Msg "  logs: Get-Content -Wait '$Log'" "  логи: Get-Content -Wait '$Log'"
Msg '  the node runs while you are logged in to Windows' '  нода работает, пока ты залогинен в Windows'
