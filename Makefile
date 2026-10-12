# Tools pinned and run via `go run` / npx: no global installs needed.
OAPI    = go run github.com/oapi-codegen/oapi-codegen/v2/cmd/oapi-codegen@v2.5.0
SQLC    = go run github.com/sqlc-dev/sqlc/cmd/sqlc@v1.30.0
GOLINT  = go run github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.5.0
DC      = docker compose

.PHONY: up down seed gen drift lint test test-unit ci

up:
	$(DC) up -d --build

down:
	$(DC) down

seed:
	$(DC) run --rm api seed

gen:
	cd services/api && $(OAPI) -config oapi-codegen.yaml ../../docs/openapi.yaml
	$(SQLC) generate
	npm run gen -w @pachanga/api-client

drift: gen
	git diff --exit-code -- services/api/internal/api services/api/internal/db packages/api-client/src

lint:
	cd services/api && $(GOLINT) run ./...
	npm run lint --workspaces --if-present
	npm run typecheck --workspaces --if-present

test-unit:
	cd services/api && go test -short ./...

test:
	cd services/api && go test ./...

ci: lint drift test
	npm run build -w @pachanga/admin
	npm run export -w @pachanga/mobile
