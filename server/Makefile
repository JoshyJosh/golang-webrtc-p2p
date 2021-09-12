.PHONY: test testracy startserver
TARGETS: test

TESTBASE=go test videocaller --parallel=1 -v

run: 
	go run cmd/main.go --port=:3000

test: 
	$(TESTBASE) -count=1

testracy: 
	$(TESTBASE) -count=5 -race
