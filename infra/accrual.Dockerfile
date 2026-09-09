# Поставленный бинарник требует glibc и доступен для Linux только на amd64.
FROM debian:bookworm-slim

COPY --chmod=755 cmd/accrual/accrual_linux_amd64 /usr/local/bin/accrual

USER 65534:65534
EXPOSE 8080
ENTRYPOINT ["/usr/local/bin/accrual"]
