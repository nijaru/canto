.PHONY: fmt test test-redis build check hooks

fmt:
	@files="$$(git ls-files '*.go')"; \
	if [ -z "$$files" ]; then \
		echo "no tracked Go files to format"; \
	else \
		goimports -w $$files; \
		golines --base-formatter gofumpt -w $$files; \
	fi

test:
	go test ./...

test-redis:
	go test -tags redis ./x/redis

build:
	go build ./...

check: fmt test build

hooks:
	git config core.hooksPath .githooks
