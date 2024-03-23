.DEFAULT_GOAL := help

.PHONY: help
help: ## Outputs the help
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | sort | awk 'BEGIN {FS = ":.*?## "}; {printf "\033[36m%-30s\033[0m %s\n", $$1, $$2}'

.PHONY: build
build: ## Compiles the application
	go build -o bin/buchhalter main.go

.PHONY: sync
sync: ## Synchronize all invoices from your suppliers
	go run main.go sync

.PHONY: run
run: ## Runs the application via the standard go tooling
	go run main.go
