BINARY_NAME=elencode

.PHONY: build
build:
	go build -o ./bin/$(BINARY_NAME) ./cmd/elencode

.PHONY: run
run: build
	./bin/$(BINARY_NAME)

.PHONY: test
test:
	go vet ./...
	go test -race ./...

