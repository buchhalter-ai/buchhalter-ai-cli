.DEFAULT_GOAL := help

.PHONY: help
help: ## Outputs the help
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | sort | awk 'BEGIN {FS = ":.*?## "}; {printf "\033[36m%-30s\033[0m %s\n", $$1, $$2}'

.PHONY: build
build: ## Compiles the application
	go build -race -ldflags "-X main.cliVersion=`git rev-parse --abbrev-ref HEAD` -X 'main.buildTime=`date "+%Y-%m-%d %H:%M:%S %z"`' -X main.commitHash=`git log --pretty=format:'%H' -n 1`" -o bin/buchhalter

.PHONY: sync
sync: ## Synchronize all invoices from your suppliers
	go run main.go sync

.PHONY: run
run: ## Runs the application via the standard go tooling
	go run main.go

.PHONY: test
test: ## Runs all unit tests
	go test -v -race ./...