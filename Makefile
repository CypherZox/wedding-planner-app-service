.PHONY: run test migrate lint docs

run:
	set -a; . ./.env; set +a; go run ./cmd/api

test:
	go test ./...

migrate:
	go run ./cmd/migrate

lint:
	go vet ./...

docs:
	go run github.com/swaggo/swag/cmd/swag@v1.16.4 init -g cmd/api/main.go -d ./ --parseInternal -o api --outputTypes yaml
