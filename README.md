# HH AI Applier

Приложение для автоматической отправки откликов на вакансии HeadHunter. Оно читает URL поиска, использует cookies авторизованного аккаунта, генерирует сопроводительное письма и решает тесты через AI.

## Запуск и компиляция

Компиляции:

```sh
go build .
```

В релизах можно скачать готовую версию под все целевые платформы: Windows, Linux, Darwin.

Для начала установите расширение `Get cookies.txt LOCALLY` для [Chrome](https://chromewebstore.google.com/detail/get-cookiestxt-locally/cclelndahbckbenkjhflpdbgdldlbecc) или [Firefox](https://addons.mozilla.org/en-US/firefox/addon/get-cookies-txt-locally/), а затем экспортируйте cookies с `hh.ru` в `cookies.txt` (приложение по умолчанию его ищет в текущем каталоге).

Запуск приложения:

```sh
./hh-ai-applier -u "<сюда скопируйте ссылку для поиска вакансий из браузера со всеми нужными фильтрами>""
```

Используйте флаг `-h` для справки.

## Docker

Скрипируйте пример окружения:

```bash
cp example.env .env
```

И заполните `SEARCH_URL`, `AI_BASE_URL`, `AI_MODEL` и при необходимости `AI_API_KEY`, изучив пример `example.env`.

Запуск:

```bash
docker-compose up -d --build
```
