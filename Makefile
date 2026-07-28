BINARY   := codebuild-watcher
VERSION  ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
DIST     := dist

LDFLAGS  := -ldflags "-s -w -X main.version=$(VERSION)"

.PHONY: all clean release

all: release

release: $(DIST)/$(BINARY)-linux-amd64 $(DIST)/$(BINARY)-windows-amd64.exe

$(DIST):
	mkdir -p $(DIST)

$(DIST)/$(BINARY)-linux-amd64: $(DIST)
	GOOS=linux GOARCH=amd64 go build $(LDFLAGS) -o $@ .

$(DIST)/$(BINARY)-windows-amd64.exe: $(DIST)
	GOOS=windows GOARCH=amd64 go build $(LDFLAGS) -o $@ .

clean:
	rm -rf $(DIST)
