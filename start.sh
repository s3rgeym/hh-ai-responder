#!/usr/bin/env bash

set -euo pipefail

APP="hh-ai-responder"

# Переходим в директорию, где находится скрипт
cd "$(dirname "$0")" || {
  echo "ERROR: cannot change directory" >&2
  exit 1
}

# Загружаем .env если существует
if [ -f ".env" ]; then
  set -a
  # shellcheck disable=SC1091
  . ".env"
  set +a
fi

# Проверяем наличие бинарника
if [ ! -f "$APP" ]; then
  echo "ERROR: $APP not found" >&2
  exit 2
fi

# Проверяем исполняемость
if [ ! -x "$APP" ]; then
  echo "ERROR: $APP is not executable" >&2
  exit 3
fi

# Запускаем приложение без передачи аргументов
exec "./$APP"
