.PHONY: build test lint confluent-examples golden fixture

build:
	go build -o schema-evolution-gate ./cmd/schema-evolution-gate

confluent-examples:
	scripts/fetch-confluent-examples.sh

test: confluent-examples
	REQUIRE_CONFLUENT_EXAMPLES=1 go test -race ./...

lint:
	go vet ./...
	golangci-lint run ./...

golden:
	go test ./cmd/... -update

fixture:
	scripts/make-fixture-repo.sh /tmp/schema-gate-fixture
