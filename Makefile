.PHONY: web server test run
web:
	cd apps/web && npm ci && npm run build
server:
	cd apps/server && go build ./cmd/seekwell
test:
	cd apps/server && go test ./internal/core
run:
	cd apps/server && go run ./cmd/seekwell
