.PHONY: devkeys serve fetch test

devkeys:
	go run ./cmd/devkit keys $(ARGS)

# trust.Load() embeds roots/*.json at compile time via go:embed, so
# regenerating trust/roots/dev.json has no effect until the binary that
# reads it is rebuilt. serve and fetch build before running for that
# reason; devkeys writes no binary, so it needs no build step.
serve:
	go build -o bin/devkit ./cmd/devkit
	./bin/devkit serve $(ARGS)

fetch:
	go build -o bin/provisor-fetch ./cmd/provisor-fetch
	./bin/provisor-fetch --keyset-url=http://127.0.0.1:8099/keyset --channel-url=http://127.0.0.1:8099/channel --installed-release=0.17.5 --allowed-registry=ghcr.io/akash-network --last-keyset-version=1 $(ARGS)

test:
	go test ./...
