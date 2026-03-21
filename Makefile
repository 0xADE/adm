-include .env
ifneq ($(wildcard .env),)
export $(shell sed 's/=.*//' .env)
endif

PRJ := $(shell head -n1 go.mod | sed 's/^module //')
# Build with this module's go.mod even if a parent go.work omits this repo.
export GOWORK=off

BINDIR=dist
DISTFILE=adm
BUILD_VERSION ?= `git describe --tags 2>/dev/null || echo v0.1.0`
LDFLAGS := -ldflags "-X main.version=$(VERSION) -X main.gitCommit=$(COMMIT)"
DESTDIR?=/usr/local

ifdef TAGS
	TAGS_ARGS = -tags ${TAGS}
endif

.PHONY: all
all: build

.PHONY: build
build:
	$(foreach dir,$(wildcard cmd/*), echo "$(dir) building..."; go build $(FLAGS) -o $(BINDIR)/ ./$(dir);)

.PHONY: docker-build
docker-build: # set token into GITHUB_TOKEN environment variable
	docker build -f Dockerfile -t local:$(PRJ) --secret id=github_token,env=GITHUB_TOKEN .

.PHONY: test
test:
	go tool ginkgo ./...

.PHONY: run
run: build
	./$(BINDIR)/$(APP)

.PHONY: run-log
run-log: tidy build
	SLOG_LEVEL=debug ./$(BINDIR)/$(APP)

.PHONY: run-race
run-race: tidy
	go run -race $(LDFLAGS) ./cmd/$(APP)

# Code quality checks
.PHONY: lint
lint:
	go tool golangci-lint run ./...

# Code quality checks with autofix
.PHONY: lint-fix
lint-fix:
	go tool golangci-lint run -v --fix ./...

.PHONY: tidy
tidy:
	go mod tidy

.PHONY: install
install: build
	@echo "Installing..."
	@install -DZs dist/${DISTFILE} -m 755 -t ${DESTDIR}/bin
	@install -D dist/adm.1.gz -t ${DESTDIR}/share/man/man1
	@echo "Done"

.PHONY: install-config
install-config:
	@install -DZ res/conf -m 644 -T ${DESTDIR}/etc/adm/conf

.PHONY: install-pam
install-pam:
	@install -DZ res/pam -m 644 -T ${DESTDIR}/etc/pam.d/adm

.PHONY: install-systemd
install-systemd:
	@install -DZ res/systemd-service -m 644 -T ${DESTDIR}/lib/systemd/system/adm.service

.PHONY: sloc
sloc:
	cloc * >sloc.stats

.PHONY: clean
clean:
	go clean
	rm -rf $(BINDIR)
