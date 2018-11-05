GOCMD=go
GOBUILD=$(GOCMD) build
GOCLEAN=$(GOCMD) clean
GOGET=$(GOCMD) get
GOPATH?=~/go
BINARY_PATH=$(GOPATH)/bin/cip
REGISTRY?=gcr.io/gke-release-staging

all: test build
build:
	$(GOBUILD) -o $(BINARY_PATH) -v
build-static:
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 $(GOBUILD) -a -ldflags '-s' -o $(BINARY_PATH) -v
docker-image:
	docker build -t $(REGISTRY)/cip:latest .
test:
	./lint.sh
	./test.sh
clean:
	$(GOCLEAN)
	rm -f $(BINARY_PATH)
deps:
	$(GOGET) github.com/golang/dep/cmd/dep
	dep ensure
