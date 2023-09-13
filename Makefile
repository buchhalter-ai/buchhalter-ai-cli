build:
	go build -o bin/buchhalter main.go

sync:
	go run main.go sync

run:
	go run main.go
