FROM golang:latest

COPY . /app

WORKDIR /app

RUN go mod tidy

CMD go run cmd/main.go --port=:3000