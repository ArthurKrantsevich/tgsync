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

# Another tgsync (for example bin\tgsync.exe run in a terminal) would poll with
# the same token (409 Conflict).
$other = Get-Process -Name tgsync -ErrorAction SilentlyContinue | Where-Object { $_.Path -ne $Bin }
if ($other) {
    Write-Host "! Запущен другой процесс tgsync (pid $($other.Id -join ', ')). Останови его и запусти установку ещё раз."
    exit 1
}

Stop-ScheduledTask -TaskName $TaskName -ErrorAction SilentlyContinue
Get-Process -Name tgsync -ErrorAction SilentlyContinue | Stop-Process -Force

New-Item -ItemType Directory -Force -Path $BinDir, (Join-Path $Conf 'data') | Out-Null
if ((Get-Command go -ErrorAction SilentlyContinue) -and (Test-Path (Join-Path $Repo 'go.mod'))) {
    Write-Host "→ сборка $Bin"
    Push-Location $Repo
    try { go build -o $Bin ./cmd/tgsync } finally { Pop-Location }
    if ($LASTEXITCODE -ne 0) { exit 1 }
} elseif (Test-Path (Join-Path $Repo 'tgsync.exe')) {
    # Release archive: the binary sits next to scripts\.
    Copy-Item (Join-Path $Repo 'tgsync.exe') $Bin -Force
} else {
    Write-Host '! Нет Go и нет tgsync.exe рядом со скриптом.'
    exit 1
}

$envFile = Join-Path $Conf '.env'
if (-not (Test-Path $envFile)) {
    if (Test-Path (Join-Path $Repo '.env')) {
        # Moved, not copied: a second copy of the token must not stay in a project folder.
        Move-Item (Join-Path $Repo '.env') $envFile
        Write-Host "→ .env перенесён в $Conf"
    } else {
        Copy-Item (Join-Path $Repo '.env.example') $envFile
        Write-Host "Заполни $envFile и запусти установку ещё раз."
        exit 1
    }
}

Write-Host '→ проверка установки'
$env:TGSYNC_HOME = $Conf
Push-Location $Conf
try { & $Bin check } finally { Pop-Location; Remove-Item Env:TGSYNC_HOME }
if ($LASTEXITCODE -ne 0) {
    Write-Host 'Исправь ошибки выше и запусти установку ещё раз.'
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
Write-Host "→ задача запущена: Get-ScheduledTask $TaskName"
Write-Host "  логи: Get-Content -Wait '$Log'"
Write-Host '  нода работает, пока ты залогинен в Windows'
