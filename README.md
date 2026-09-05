# Gophermart

«Гофермарт» — учебная накопительная система лояльности на Go. Функциональные
требования описаны в [SPECIFICATION.md](SPECIFICATION.md).

## Требования

- Go 1.26

## Запуск

```sh
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

```sh
go test -race ./...
go vet ./...
go build -o cmd/gophermart/gophermart ./cmd/gophermart
```
