package main

import (
	"flag"

	"videocaller"

	"github.com/sirupsen/logrus"
)

func main() {
	addr := flag.String("port", ":80", "Port to host the HTTP server on.")
	flag.Parse()

	logrus.Println("Listening on", *addr)
	err := videocaller.StartServer(*addr)
	if err != nil {
		logrus.Fatalf("Failed to serve: %v", err)
	}
}
