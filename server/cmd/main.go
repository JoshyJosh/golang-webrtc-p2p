package main

import (
	"flag"

	"videocaller"

	"github.com/sirupsen/logrus"
)

func main() {
	addr := flag.String("port", ":80", "Port to host the HTTP server on.")
	// logLevel := flag.String("logging", "", "Logging level")
	flag.Parse()

	// if logLevel != "" {
	// 	switch logLevel {
	// 	case "info":

	// 	}
	// }

	logrus.SetLevel(logrus.DebugLevel)
	logrus.Println("Listening on", *addr)
	err := videocaller.StartServer(*addr)
	if err != nil {
		logrus.Fatalf("Failed to serve: %v", err)
	}
}
