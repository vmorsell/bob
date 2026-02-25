.PHONY: up down test test-go test-ui build build-go build-ui

up:
	docker compose up --build

down:
	docker compose down

test: test-go test-ui

test-go:
	go test ./... -count=1

test-ui:
	cd ui && npm test

build: build-go build-ui

build-go:
	go build -o bob .

build-ui:
	cd ui && npm run build
