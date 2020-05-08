FROM golang:latest

COPY . /app

WORKDIR /app

RUN export GOPATH=$GOPATH:/app

RUN go get github.com/stretchr/testify/require

RUN go get -v -d ./...

# RUN go install -v ./...

CMD ls && go run cmd/main.go --address=:3000