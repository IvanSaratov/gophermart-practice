# Gophermart

«Гофермарт» — учебная накопительная система лояльности на Go. Функциональные
требования описаны в [SPECIFICATION.md](SPECIFICATION.md).

## Требования

- Go 1.26

## Запуск

Параметр PostgreSQL обязателен. `JWT_SECRET` задаёт постоянный ключ сессий.
Если он не указан, при запуске генерируется случайный ключ: пользователи
сохраняются, но после перезапуска им потребуется войти заново. Специально задаем такую логику так как это все таки
тестовый проект и нету смысла слишком сильно запариваться.

```sh
DATABASE_URI='postgresql://gophermart:gophermart@localhost:5432/gophermart?sslmode=disable' \
go run ./cmd/gophermart
```

Параметры запуска:

| Назначение | Флаг | Переменная окружения | По умолчанию |
| --- | --- | --- | --- |
| HTTP-адрес | `-a`, `--address` | `RUN_ADDRESS` | `localhost:8080` |
| PostgreSQL URI | `-d`, `--database-uri` | `DATABASE_URI` | - |
| Адрес системы начислений | `-r`, `--accrual-system-address` | `ACCRUAL_SYSTEM_ADDRESS` | - |
| Ключ подписи сессий | - | `JWT_SECRET` | - |

Секрет намеренно не имеет CLI-флага, чтобы он не отображался в списке
запущенных процессов.

## Авторизация

```sh
curl \
  -H 'Content-Type: application/json' \
  -d '{"login":"foo","password":"bar"}' \
  http://localhost:8080/api/user/register

curl \
  -H 'Content-Type: application/json' \
  -d '{"login":"foo","password":"bar"}' \
  http://localhost:8080/api/user/login
```

## Операционные endpoints

- `GET /health` — процесс запущен;
- `GET /ready` — обязательные зависимости готовы;
- `GET /metrics` — метрики в формате Prometheus.

## Проверка

Запустить Compose:

```sh
docker compose -f infra/compose.yaml up --build -d
curl http://localhost:8080/health
curl http://localhost:8080/ready
```

Статические проверки:

```sh
go test -race ./...
go vet ./...
go build -o cmd/gophermart/gophermart ./cmd/gophermart
```
