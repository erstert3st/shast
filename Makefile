.PHONY: all build check fmt vet staticcheck test test-short image verify seed-determinism

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

# Rebuilds the sandbox image twice without cache and compares the seed
# manifests (slow, needs network for apt).
seed-determinism:
	SHAST_SEED_REBUILD=1 go test -run TestSeedDeterministic -count=1 -v -timeout 20m ./internal/sandbox
