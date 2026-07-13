.PHONY: build
build:
	CGO_ENABLED=0 go build -trimpath -o attestation-sidecar .

.PHONY: build-linux
build-linux:
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -o attestation-sidecar .

.PHONY: test
test:
	go test ./...

.PHONY: clean
clean:
	rm -rf attestation-sidecar dist/
