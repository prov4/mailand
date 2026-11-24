GO        ?= go
TOOLS_BIN := $(CURDIR)/.tools/bin
PROTOC    ?= protoc
BIN_DIR   := $(CURDIR)/build/bin

.PHONY: all tools proto backend frontend test run-daemon clean release install uninstall package enable-daemon

all: backend frontend

tools:
	GOBIN=$(TOOLS_BIN) $(GO) -C backend install google.golang.org/protobuf/cmd/protoc-gen-go@v1.36.12
	GOBIN=$(TOOLS_BIN) $(GO) -C backend install google.golang.org/grpc/cmd/protoc-gen-go-grpc@v1.6.2

proto: tools
	PATH=$(TOOLS_BIN):$$PATH $(PROTOC) -I proto \
		--go_out=backend/internal/rpc/pb --go_opt=paths=source_relative \
		--go-grpc_out=backend/internal/rpc/pb --go-grpc_opt=paths=source_relative \
		proto/mailand.proto

backend:
	$(GO) -C backend build -o $(BIN_DIR)/mailand-daemon ./cmd/mailand-daemon
	$(GO) -C backend build -o $(BIN_DIR)/mailand-seed ./cmd/mailand-seed

frontend:
	cmake -S frontend -B build/frontend -DCMAKE_BUILD_TYPE=Debug
	cmake --build build/frontend -j

test:
	$(GO) -C backend test ./...

run-daemon: backend
	$(BIN_DIR)/mailand-daemon

clean:
	rm -rf build

# --- Packaging / install -----------------------------------------------------
PREFIX  ?= /usr
DESTDIR ?=
RELDIR  := $(CURDIR)/build/release
# systemd user units go under the *system* lib dir, which is /usr/lib on Arch.
UNITDIR ?= $(PREFIX)/lib/systemd/user

# Optimised build of both binaries into build/release (what `install` ships).
# mailand-seed is a dev tool and is not built or installed here.
release:
	CGO_ENABLED=0 $(GO) -C backend build -trimpath -buildmode=pie -mod=readonly \
		-ldflags "-s -w" -o $(RELDIR)/mailand-daemon ./cmd/mailand-daemon
	cmake -S frontend -B $(RELDIR)/frontend -DCMAKE_BUILD_TYPE=Release -DCMAKE_INSTALL_PREFIX=$(PREFIX)
	cmake --build $(RELDIR)/frontend -j
	cp $(RELDIR)/frontend/mailand-ui $(RELDIR)/mailand-ui

# Honors PREFIX and DESTDIR, e.g. `make install PREFIX=$$HOME/.local` (then copy
# the unit by hand if your prefix is not /usr) or `make install DESTDIR=/tmp/stage`.
install: release
	install -Dm755 $(RELDIR)/mailand-daemon $(DESTDIR)$(PREFIX)/bin/mailand-daemon
	install -Dm755 $(RELDIR)/mailand-ui $(DESTDIR)$(PREFIX)/bin/mailand-ui
	sed 's|^ExecStart=.*|ExecStart=$(PREFIX)/bin/mailand-daemon|' \
		deploy/mailand-daemon.service > $(RELDIR)/mailand-daemon.service
	install -Dm644 $(RELDIR)/mailand-daemon.service $(DESTDIR)$(UNITDIR)/mailand-daemon.service
	install -Dm644 deploy/mailand-ui.desktop $(DESTDIR)$(PREFIX)/share/applications/mailand-ui.desktop
	install -Dm644 deploy/mailand.svg $(DESTDIR)$(PREFIX)/share/icons/hicolor/scalable/apps/mailand.svg

uninstall:
	rm -f $(DESTDIR)$(PREFIX)/bin/mailand-daemon $(DESTDIR)$(PREFIX)/bin/mailand-ui \
		$(DESTDIR)$(UNITDIR)/mailand-daemon.service \
		$(DESTDIR)$(PREFIX)/share/applications/mailand-ui.desktop \
		$(DESTDIR)$(PREFIX)/share/icons/hicolor/scalable/apps/mailand.svg

# Builds mailand-git-*.pkg.tar.zst in deploy/ from the local tree; install it
# yourself with `sudo pacman -U deploy/mailand-git-*.pkg.tar.zst`.
# -C wipes deploy/src first: a stale CMake build dir there breaks the QML cache link.
# If go comes from mise/asdf rather than pacman: `make package MAKEPKG_FLAGS=-d`.
MAKEPKG_FLAGS ?=
package:
	cd deploy && makepkg -Cf $(MAKEPKG_FLAGS)

# Run by YOU after `make install` or installing the package; never run by the build.
enable-daemon:
	systemctl --user daemon-reload && systemctl --user enable --now mailand-daemon
