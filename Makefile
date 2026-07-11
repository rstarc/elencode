BINARY_NAME=tedd-code

.PHONY: build
build:
	go build -o ./bin/$(BINARY_NAME) ./cmd/tedd-code 

.PHONY: run
run: build
	./bin/$(BINARY_NAME)

.PHONY: test
test:
	go vet ./...
	go test -race ./...

