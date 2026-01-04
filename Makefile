run:
	go run ./...

build:
	GOOS=linux GOARCH=arm64 go build -o dpf

generate-templ:
	templ generate

# Make sure to run templ generate before building
build: generate-templ
	go build