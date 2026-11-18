.PHONY: up down logs test migrate build webdev

up:            ## docker compose up --build -d
	docker compose up --build -d

down:          ## docker compose down
	docker compose down

logs:          ## tail logs
	docker compose logs -f gpuhub

test:          ## go unit + integration tests
	go test ./...

migrate:       ## apply DB migrations and exit
	go run ./cmd/gpuhub --migrate-only

build:         ## local build (requires web/dist built)
	go build -o bin/gpuhub ./cmd/gpuhub

web:           ## build frontend
	cd web && npm install && npm run build

smoke:         ## run smoke test against localhost:8080
	bash scripts/smoke.sh
