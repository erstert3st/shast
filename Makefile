.PHONY: all build check fmt vet staticcheck test test-short image verify

BIN := shast

all: check build

build:
	go build -o $(BIN) .

check: fmt vet staticcheck test

fmt:
	@out="$$(gofmt -l .)"; if [ -n "$$out" ]; then echo "gofmt needed:"; echo "$$out"; exit 1; fi

vet:
	go vet ./...

staticcheck:
	go tool staticcheck ./...

test:
	go test ./...

test-short:
	go test -short ./...

image: build
	./$(BIN) image build

verify: build
	./$(BIN) verify --runs 2
