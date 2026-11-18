# The project builds with the Go toolchain and the viva command, which go.mod
# pins as a tool, so `go tool viva` needs no separate install.

.PHONY: all build test vet run package shots clean

all: vet test build

build:
	go tool viva build

run:
	go tool viva dev .

test:
	go test ./... -count=1

# viva vet finds page mistakes that compile but paint the wrong thing.
vet:
	gofmt -l . | tee /dev/stderr | wc -l | grep -q '^0$$'
	go vet ./...
	go tool viva vet

package:
	go tool viva package --target linux

# Render every page in both themes without opening a window.
shots:
	go run -tags vivashots ./cmd/shots build/shots

# The live checks reach the network and the sound card, so they are opt-in.
live:
	go test ./internal/ncm ./internal/player -tags live -count=1

clean:
	rm -rf build
