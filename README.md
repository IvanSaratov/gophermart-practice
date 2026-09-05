# Gophermart

«Гофермарт» — учебная накопительная система лояльности на Go. Функциональные
требования описаны в [SPECIFICATION.md](SPECIFICATION.md).

## Требования

- Go 1.26

## Проверка

```sh
go test ./...
go vet ./...
go build -o cmd/gophermart/gophermart ./cmd/gophermart
```
