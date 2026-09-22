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

# Пересобираем, если есть Go (чтобы подхватить изменения промпта)
if command -v go >/dev/null 2>&1; then
  go build -o "$APP" . || exit 4
fi

# Если ссылки нет ни в аргументах, ни в .env (HH_SEARCH_URL), спрашиваем
if [ $# -eq 0 ] && [ -z "${HH_SEARCH_URL:-}" ]; then
  read -r -p "Ссылка на поиск вакансий: " url
  [ -n "$url" ] && set -- -u "$url"
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

exec "./$APP" "$@"
