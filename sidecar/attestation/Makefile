DEV_REGISTRY   ?= ghcr.io/cloud-j-luna
DEV_COMMIT     ?= $(shell git diff --quiet HEAD 2>/dev/null && git rev-parse --short HEAD || echo "$(shell git rev-parse --short HEAD)-$(shell date +%s)")

.PHONY: build
build:
	CGO_ENABLED=0 go build -trimpath -o attestation-sidecar .

.PHONY: build-linux
build-linux:
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -o attestation-sidecar .

.PHONY: test
test:
	go test ./...

.PHONY: docker
docker: build-linux
	docker buildx build --platform=linux/amd64 \
		-t $(DEV_REGISTRY)/attestation-sidecar:$(DEV_COMMIT) \
		-f Dockerfile . --load

.PHONY: docker-debug
docker-debug: build-linux
	docker buildx build --platform=linux/amd64 \
		-t $(DEV_REGISTRY)/attestation-sidecar:debug \
		-f Dockerfile.debug . --load

.PHONY: dev-push
dev-push: docker
	docker push $(DEV_REGISTRY)/attestation-sidecar:$(DEV_COMMIT)
	@echo ""
	@echo "Pushed:"
	@echo "  $(DEV_REGISTRY)/attestation-sidecar:$(DEV_COMMIT)"

.PHONY: dev-push-debug
dev-push-debug: docker-debug
	docker push $(DEV_REGISTRY)/attestation-sidecar:debug
	@echo ""
	@echo "Pushed:"
	@echo "  $(DEV_REGISTRY)/attestation-sidecar:debug"

.PHONY: clean
clean:
	rm -rf attestation-sidecar dist/
