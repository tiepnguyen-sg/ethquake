SHELL := /bin/sh

ETHQUAKE_LOCAL_BIN ?= $(HOME)/.local/bin
GO ?= go

.PHONY: preflight verify-images access-start access-stop access-status \
	access-smoke kurtosis devnet-up devnet-down devnet-status devnet-verify \
	go-preflight fmt fmt-check test test-race test-e2e vet build verify \
	scenario-validate phase3-prepare phase3-preflight phase3-run
preflight:
	@PATH="$(ETHQUAKE_LOCAL_BIN):$(PATH)" ./scripts/devnet/preflight.sh

verify-images:
	@./scripts/devnet/verify-images.sh

access-start:
	@./scripts/devnet/access.sh gateway-start

access-stop:
	@./scripts/devnet/access.sh gateway-stop

access-status:
	@./scripts/devnet/access.sh status

access-smoke:
	@./scripts/devnet/access-smoke.sh

kurtosis:
	@./scripts/devnet/access.sh kurtosis $(KURTOSIS_ARGS)

devnet-up: preflight verify-images
	@./scripts/devnet/devnet.sh up

devnet-down:
	@./scripts/devnet/devnet.sh down

devnet-status:
	@./scripts/devnet/devnet.sh status

devnet-verify:
	@./scripts/devnet/devnet.sh verify

go-preflight:
	@GO="$(GO)" ./scripts/toolchain/preflight.sh

fmt: go-preflight
	@files="$$(find cmd internal -name '*.go' -type f)"; \
	if [ -n "$$files" ]; then $(GO)fmt -w $$files; fi

fmt-check: go-preflight
	@files="$$(find cmd internal -name '*.go' -type f)"; \
	unformatted=""; \
	if [ -n "$$files" ]; then unformatted="$$($(GO)fmt -l $$files)"; fi; \
	if [ -n "$$unformatted" ]; then \
		printf '[FAIL] Go files require formatting:\n%s\n' "$$unformatted" >&2; \
		exit 1; \
	fi; \
	printf '[PASS] Go formatting\n'

test: go-preflight
	@GOTOOLCHAIN=local $(GO) test ./...

test-race: go-preflight
	@GOTOOLCHAIN=local $(GO) test -race ./...

test-e2e: go-preflight
	@GO="$(GO)" ./scripts/observer/e2e.sh

vet: go-preflight
	@GOTOOLCHAIN=local $(GO) vet ./...

build: go-preflight
	@mkdir -p bin
	@CGO_ENABLED=0 GOTOOLCHAIN=local $(GO) build -trimpath -o bin/ethquake ./cmd/ethquake

verify: fmt-check test vet build

scenario-validate: build
	@./bin/ethquake scenario validate --file scenarios/cl-p2p-partition.yaml

phase3-prepare:
	@./scripts/experiment/prepare-helm.sh
	@./scripts/experiment/prepare-dependencies.sh
	@./scripts/experiment/prepare-chaos-mesh.sh

phase3-preflight: build
	@./scripts/experiment/preflight.sh local

phase3-run:
	@./scripts/experiment/phase3.sh
