.PHONY: test vet verify validate-example validate-container-example dry-run-example build

GOCACHE ?= $(CURDIR)/.gocache
export GOCACHE

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
	go build -o ./bin/muxmail ./cmd/muxmail
