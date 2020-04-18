module github.com/onflow/cadence/languageserver

go 1.13

require (
	github.com/mattn/go-isatty v0.0.12
	github.com/onflow/cadence v0.0.0-20200417232004-d4a1bfe50192
	github.com/onflow/flow-go-sdk v0.0.0-20200418000618-c361886aa450
	github.com/sourcegraph/jsonrpc2 v0.0.0-20191222043438-96c4efab7ee2
	google.golang.org/grpc v1.28.1
)

replace github.com/onflow/cadence => ../