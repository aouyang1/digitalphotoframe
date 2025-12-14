run:
	go run ./...

build:
	GOOS=linux GOARCH=arm64 go build -o dpf
