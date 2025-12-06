FROM golang:alpine AS builder

WORKDIR /app

COPY . .

RUN go mod tidy

RUN CGO_ENABLED=0 GOOS=linux go build -o execution main.go

FROM alpine:latest
WORKDIR /root/
COPY --from=builder /app/execution .

EXPOSE 80

CMD ["./execution"]