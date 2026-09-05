# Gophermart

«Гофермарт» — учебная накопительная система лояльности на Go. Функциональные
требования описаны в [SPECIFICATION.md](SPECIFICATION.md).

## Требования

- Go 1.26

## Запуск

Параметр PostgreSQL обязателен.

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
