.PHONY: test startserver

startserver: 
	go run cmd/main.go --address=:3000

test: 
	go test videocaller --parallel=1 -timeout 30s -v 