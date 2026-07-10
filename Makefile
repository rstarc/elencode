BINARY_NAME=tedd-code

.PHONY: build
build:
	go build -o ./bin/$(BINARY_NAME) ./cmd/tedd-code 

.PHONY: test
test:
	go vet ./...
	go test -race ./...

