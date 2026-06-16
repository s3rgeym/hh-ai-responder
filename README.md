# HH AI Applier

Приложение для автоматической рассылки откликов на HH.RU с помощью AI.

## Запуск и компиляция

Компиляции:

```sh
go build .
```

В [релизах](/s3rgeym/hh-ai-applier/releases) можно скачать готовую версию под все целевые платформы: Windows, Linux, Darwin (Mac) и Android (для запуска через Termux).

Для начала установите расширение `Get cookies.txt LOCALLY` для [Chrome](https://chromewebstore.google.com/detail/get-cookiestxt-locally/cclelndahbckbenkjhflpdbgdldlbecc) или [Firefox](https://addons.mozilla.org/en-US/firefox/addon/get-cookies-txt-locally/), а затем экспортируйте cookies с `hh.ru` в `cookies.txt` (приложение по умолчанию его ищет в текущем каталоге, если путь до него не задан).

Для запуска приложения укажите ссылку для поиска по вакансиям:

<img width="834" height="600" src="https://github.com/user-attachments/assets/9a23879b-44a6-446f-b6d3-f496d57bc234" />

```sh
./hh-ai-applier -u "https://hh.ru/search/vacancy?..."
```

Из данной ссылки берется тот же базовый домен сайта, чтобы не было лишних редиректов. Выбор различных фильтров в интерфейсе, приводит к изменению параметров **Query String** ссылки, а поэтому они будут применяться и при работе через программу, что избавляет от передачи их через флаги — просто скопируй и вставь ссылку.

Используйте флаг `-h` для справки.

## Переменные окружения

Аргументы могут передаваться не только через командную строку, но и через переменные окружения. Для docker — это предпочтительный способ передачи.

Скопируйте пример файла, содержащего переменные окружения:

```bash
cp example.env .env
```

> Приложение автоматически грузит переменные окружения из `.env` в текущем рабочем каталоге. У аргументов, переданных через командную строку, более высокий приоритет.

И заполните `HH_SEARCH_URL`, `HH_AI_BASE_URL`, `HH_AI_MODEL` и при необходимости `HH_AI_API_KEY`, изучив пример `example.env`.

Поддерживаемые переменные приложения:

| Переменная | Назначение |
| --- | --- |
| `HH_SEARCH_URL` | URL поисковой выдачи HH с нужными фильтрами. |
| `HH_AI_BASE_URL` | Базовый URL OpenAI-compatible API. |
| `HH_AI_MODEL` | Модель AI. |
| `HH_AI_API_KEY` | API key для OpenAI-compatible API. |

## Docker

Требует наличия `docker` и `docker-compose`, а так же файла `.env` (см. пред. пункт).

Запуск:

```bash
docker compose up -d --build
```
