.PHONY: build release frontend check validate-config test-release build-dry-run release-dry-run

build:
	./scripts/build.sh

release:
	./scripts/release.sh

frontend:
	./scripts/frontend.sh

check:
	./scripts/go-check.sh

validate-config:
	go run ./scripts/validate-config data/config.example.yaml

test-release:
	./scripts/test-release.sh

build-dry-run:
	DRY_RUN=1 VERSION=dev COMMIT=unknown BUILD_DATE=unknown ./scripts/build.sh

release-dry-run:
	DRY_RUN=1 VERSION=dev COMMIT=unknown BUILD_DATE=unknown ./scripts/release.sh
