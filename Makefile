include .env

PROJECTNAME=$(shell basename "$(PWD)")

# Go related variables.
GOBASE=$(shell pwd)
GOPATH="$(GOBASE)/vendor:$(GOBASE)"
CMD_SERVER=cmd/proxy
GOBIN=$(GOBASE)/$(CMD_SERVER)
GOFILES=$(wildcard *.go)

# Redirect error output to a file, so we can show it in development mode.
STDERR=/tmp/.$(PROJECTNAME)-server-stderr.txt

# PID file will keep the process id of the proxy
PID_PROXY=/tmp/.$(PROJECTNAME)-server.pid

RANDOM=$(shell date +%s)
RND1=$(shell echo "(48080 + "$RANDOM" % 9)" | bc)
PROXY_PORT=$(RND1)
RND2=$(shell echo "(47070 + "$RANDOM" % 9)" | bc)
METRICS_PORT=$(RND2)
ADDRESS=localhost:$(PROXY_PORT)
TEMP_FILE=$(shell mktemp)
DOCS_DIR=./docs
DOCS_GO=$(DOCS_DIR)/swagger.yaml
MAIN_GO=./$(CMD_SERVER)/main.go

# Make is verbose in Linux. Make it silent.
MAKEFLAGS += --silent

## run: Compile and run server and agent
run: go-compile

## coverage: Calculate coverage
coverage: go-coverage

## start: Start in development mode. Auto-starts when code changes.
start: start-proxy

## stop: Stop development mode. PROXY
stop: stop-proxy

start-proxy: stop-proxy
	@echo "  >  $(PROJECTNAME) is available at $(ADDRESS)"
	@-$(GOBIN)/$(PROJECTNAME)-server -metrics-addr "$(METRICS_PORT)" -proxy-addr "$(ADDRESS)" & echo $$! > $(PID_PROXY)
	@cat $(PID_PROXY) | sed "/^/s/^/  \>  PID: /"

stop-proxy:
	@-touch $(PID_PROXY)
	@-kill `cat $(PID_PROXY)` 2> /dev/null || true
	@-rm $(PID_PROXY)

restart-proxy: stop-proxy start-proxy

build: go-build-proxy

## clean: Clean build files. Runs `go clean` internally.
clean: go-clean
	@-rm $(GOBIN)/$(PROJECTNAME)-server

## compile: Compile the binary.
go-compile: go-build-proxy

go-build-proxy:
	@echo "  >  Building gopher $(PROJECTNAME)-server binary..."
	@GOPATH=$(GOPATH) GOBIN=$(GOBIN) cd ./$(CMD_SERVER) && go build -o $(GOBIN)/$(PROJECTNAME)-server $(GOFILES)

go-generate:
	@echo "  >  Generating dependency files..."
	@GOPATH=$(GOPATH) GOBIN=$(GOBIN) go generate $(generate)

# 1. Generate the list of packages to include, excluding 'mocks'
# go list ./... lists all packages; grep -v excludes lines containing 'mocks'
go-coverage:
	CVPKG=$(go list ./... | grep -v '/mocks' | tr '\n' ',')
	# 2. Run tests with coverage analysis applied only to the specified packages
	# @GOPATH=$(GOPATH) GOBIN=$(GOBIN) go test -coverpkg=$(CVPKG) -coverprofile=coverage.out ./...
	@GOPATH=$(GOPATH) GOBIN=$(GOBIN) go test -coverprofile=coverage.out ./...
	# 3. (Optional) Generate the HTML report
	@GOPATH=$(GOPATH) GOBIN=$(GOBIN) go tool cover -html=coverage.out -o docs/coverage.html

go-get:
	@echo "  >  Checking if there is any missing dependencies..."
	@GOPATH=$(GOPATH) GOBIN=$(GOBIN) go get $(get)

.PHONY: go-update-deps
go-update-deps:
	@echo ">> updating Go dependencies"
	@for m in $$(go list -mod=readonly -m -f '{{ if and (not .Indirect) (not .Main)}}{{.Path}}{{end}}' all); do \
		go get $$m; \
	done
	go mod tidy
ifneq (,$(wildcard vendor))
	go mod vendor
endif

go-install:
	@GOPATH=$(GOPATH) GOBIN=$(GOBIN) go install $(GOFILES)

go-clean:
	@echo "  >  Cleaning build cache"
	@GOPATH=$(GOPATH) GOBIN=$(GOBIN) go clean

.PHONY: help
all: help
help: Makefile
	@echo
	@echo " Choose a command run in "$(PROJECTNAME)":"
	@echo
	@sed -n 's/^##//p' $< | column -t -s ':' |  sed -e 's/^/ /'
	@echo
