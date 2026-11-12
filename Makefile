.PHONY: web server test web-test run
web:
	cd apps/web && npm ci && npm run build
server:
	cd apps/server && go build ./cmd/seekwell
test:
	cd apps/server && go test ./internal/core
	$(MAKE) web-test
web-test:
	cd apps/web && npm ci && npm run test
run:
	cd apps/server && go run ./cmd/seekwell
