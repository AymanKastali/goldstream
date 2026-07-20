# goldstream — common tasks. Run `make help` for the list.

BINARY := goldstream
IMAGE  := goldstream

# Load a local .env (GOLDAPI_KEY, PORT, ...) if present, so `make run` just works.
ifneq (,$(wildcard .env))
include .env
export
endif

.PHONY: help run build test race vet fmt docker compose clean

help: ## Show this help
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(firstword $(MAKEFILE_LIST)) | \
		awk 'BEGIN {FS = ":.*?## "}; {printf "  \033[36m%-10s\033[0m %s\n", $$1, $$2}'

run: ## Run the server (needs GOLDAPI_KEY in the environment or .env)
	go run ./cmd/goldstream

build: ## Compile the binary into ./goldstream
	go build -o $(BINARY) ./cmd/goldstream

test: ## Run the unit tests
	go test ./...

race: ## Run the tests with the race detector
	go test -race ./...

vet: ## Run go vet
	go vet ./...

fmt: ## Format the code
	gofmt -w .

docker: ## Build the Docker image
	docker build -t $(IMAGE) .

compose: ## Build and start with Docker Compose
	docker compose up --build

clean: ## Remove the built binary
	rm -f $(BINARY)
