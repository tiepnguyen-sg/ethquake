SHELL := /bin/sh

ETHQUAKE_LOCAL_BIN ?= $(HOME)/.local/bin

.PHONY: preflight verify-images access-start access-stop access-status \
	access-smoke kurtosis devnet-up devnet-down devnet-status devnet-verify
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
