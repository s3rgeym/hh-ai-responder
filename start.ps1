$ErrorActionPreference = "Stop"

$App = "hh-ai-responder.exe"

try {
    # Переходим в директорию, где находится скрипт
    $ScriptDir = Split-Path -Parent $MyInvocation.MyCommand.Path
    Set-Location $ScriptDir
}
catch {
    Write-Error "ERROR: cannot change directory"
    exit 1
}

# Загружаем .env если существует
if (Test-Path ".env") {
    Get-Content ".env" |
        Where-Object { $_ -and ($_ -notmatch '^\s*#') } |
        ForEach-Object {
            $parts = $_ -split '=', 2
            if ($parts.Count -eq 2) {
                $name = $parts[0].Trim()
                $value = $parts[1].Trim().Trim('"')
                if ($name) {
                    [System.Environment]::SetEnvironmentVariable($name, $value)
                }
            }
        }
}

# Проверяем наличие бинарника
if (-not (Test-Path $App)) {
    Write-Error "ERROR: $App not found"
    exit 2
}

# Проверяем, что это исполняемый файл
if (-not ($App.ToLower().EndsWith(".exe"))) {
    Write-Error "ERROR: $App is not an executable file"
    exit 3
}

# Запускаем приложение без передачи аргументов
& ".\\$App"
exit $LASTEXITCODE
