.PHONY: web server test web-test run release
web:
	cd apps/web && npm ci && npm run build
server:
	cd apps/server && go build ./cmd/seekwell
test:
	cd apps/server && go test ./internal/core
	$(MAKE) web-test
web-test:
	cd apps/web && npm ci && npm run test
release:
	cd apps/web && node scripts/build-release.mjs --check
	cd apps/web && npm ci && npm run build:release
	cd apps/server && go build ./cmd/seekwell
run:
	cd apps/server && go run ./cmd/seekwell
