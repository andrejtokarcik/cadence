.PHONY: build
build:
	go build -o ./cmd/languageserver/languageserver ./cmd/languageserver
	GOARCH=wasm GOOS=js go build -o ./cmd/languageserver/languageserver.wasm ./cmd/languageserver
