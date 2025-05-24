# Этап сборки
FROM golang:1.24 AS builder
WORKDIR /app
# Копируем все файлы (исходники и rates.json) в контейнер
COPY . .
# Компилируем бинарь с именем "arbitrage"
RUN go build -o arbitrage .

# Этап исполнения
FROM alpine:latest
WORKDIR /app
# Устанавливаем список корневых сертификатов (если требуется для go https)
RUN apk --no-cache add ca-certificates
# Копируем скомпилированный бинарь и файл rates.json из этапа сборки
COPY --from=builder /app/arbitrage .
COPY rates.json .
# Запускаем приложение, передавая файл с курсами в качестве параметра
CMD ["./arbitrage", "-f", "rates.json"]