GO        ?= go
TOOLS_BIN := $(CURDIR)/.tools/bin
PROTOC    ?= protoc
BIN_DIR   := $(CURDIR)/build/bin

.PHONY: all tools proto backend frontend test run-daemon clean

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
