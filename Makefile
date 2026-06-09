.PHONY: test vet verify validate-example validate-container-example dry-run-example build admin-install admin-build admin-sync admin-restore

GOCACHE ?= $(CURDIR)/.gocache
NPM_CONFIG_CACHE ?= $(CURDIR)/.npm-cache
export GOCACHE
export NPM_CONFIG_CACHE

test:
	go test ./...

vet:
	go vet ./...

verify: test vet validate-example validate-container-example dry-run-example build

validate-example:
	go run ./cmd/muxmail config validate -c config.example.yaml

validate-container-example:
	go test ./internal/config -run TestContainerExampleConfigValidatesInStrictMode -count=1

dry-run-example:
	go run ./cmd/muxmail send dry-run -c config.example.yaml --app project_a --scene register_code --to user@gmail.com --locale en-US --var code=123456 --var expire_minutes=10

build:
	node web/admin/scripts/build-binary.mjs

admin-install:
	cd web/admin && npm ci

admin-build: admin-install
	cd web/admin && npm run build

admin-sync: admin-build
	node web/admin/scripts/sync-admin-dist.mjs

admin-restore:
	node web/admin/scripts/restore-admin-placeholder.mjs
