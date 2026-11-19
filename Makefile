# Cetone builds with the Go toolchain and the viva command, which go.mod pins as
# a tool, so `go tool viva` needs no separate install.

.PHONY: all build test race vet run package shots icons install-icons clean

all: vet test build

build:
	go tool viva build

run:
	go tool viva dev .

test:
	go test ./... -count=1

# The same tests with the race detector, which CI runs.
race:
	go test ./... -count=1 -race

# viva vet finds page mistakes that compile but paint the wrong thing.
vet:
	gofmt -l . | tee /dev/stderr | wc -l | grep -q '^0$$'
	go vet ./...
	go tool viva vet

package:
	go tool viva package --target linux

# The two targets a Linux host can build: macOS needs a macOS host.
package-windows:
	go tool viva package --target windows --format zip

# Everything a local machine can produce, for a release made by hand.
release-local: vet test package package-windows

# Render every page in both themes without opening a window.
shots:
	go run -tags vivashots ./cmd/shots build/shots

# The live checks reach the network and the sound card, so they are opt-in.
live:
	go test ./internal/ncm ./internal/player -tags live -count=1

# The icon set the desktop picks from, for a manual install.
ICON_SIZES := 16 22 24 32 48 64 96 128 256 512 1024

icons:
	python3 tools/makeicon.py

# Both variants: the dark tile the icon was drawn on, and the pale one. `viva
# package` ships a single icon (the one [app] icon names), so a packaged build
# carries the dark tile for the launcher and the application itself selects either
# at run time for its window and tray icons.
install-icons:
	for size in $(ICON_SIZES); do \
		install -Dm644 icons/hicolor/$${size}x$${size}/apps/cetone.png \
			/usr/share/icons/hicolor/$${size}x$${size}/apps/cetone.png; \
		install -Dm644 icons/hicolor/$${size}x$${size}/apps/cetone-light.png \
			/usr/share/icons/hicolor/$${size}x$${size}/apps/cetone-light.png; \
	done
	gtk-update-icon-cache -f -t /usr/share/icons/hicolor || true

clean:
	rm -rf build
