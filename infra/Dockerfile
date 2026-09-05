FROM golang:1.26-alpine AS build

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/gophermart ./cmd/gophermart

FROM alpine:3.23

RUN apk add --no-cache ca-certificates \
    && addgroup -S gophermart \
    && adduser -S -G gophermart gophermart
COPY --from=build /out/gophermart /usr/local/bin/gophermart

USER gophermart
EXPOSE 8080
ENTRYPOINT ["/usr/local/bin/gophermart"]
