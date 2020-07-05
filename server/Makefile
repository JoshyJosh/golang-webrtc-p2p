.PHONY: test testracy startserver
TARGETS: test

TESTBASE=go test videocaller --parallel=1 -v

startserver: 
	go run cmd/main.go --address=:3000 -timeout=30s

test: 
	$(TESTBASE) -count=1

testracy: 
	$(TESTBASE) -count=5 -race