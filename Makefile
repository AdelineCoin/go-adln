# This Makefile is meant to be used by people that do not usually work
# with Go source code. If you know what GOPATH is then you probably
# don't need to bother with make.

.PHONY: adln android ios adln-cross swarm evm all test clean
.PHONY: adln-linux adln-linux-386 adln-linux-amd64 adln-linux-mips64 adln-linux-mips64le
.PHONY: adln-linux-arm adln-linux-arm-5 adln-linux-arm-6 adln-linux-arm-7 adln-linux-arm64
.PHONY: adln-darwin adln-darwin-386 adln-darwin-amd64
.PHONY: adln-windows adln-windows-386 adln-windows-amd64

GOBIN = $(shell pwd)/build/bin
GO ?= latest

adln:
	build/env.sh go run build/ci.go install ./cmd/adln
	@echo "Done building."
	@echo "Run \"$(GOBIN)/adln\" to launch adln."

swarm:
	build/env.sh go run build/ci.go install ./cmd/swarm
	@echo "Done building."
	@echo "Run \"$(GOBIN)/swarm\" to launch swarm."

all:
	build/env.sh go run build/ci.go install

android:
	build/env.sh go run build/ci.go aar --local
	@echo "Done building."
	@echo "Import \"$(GOBIN)/adln.aar\" to use the library."

ios:
	build/env.sh go run build/ci.go xcode --local
	@echo "Done building."
	@echo "Import \"$(GOBIN)/adln.framework\" to use the library."

test: all
	build/env.sh go run build/ci.go test

clean:
	rm -fr build/_workspace/pkg/ $(GOBIN)/*

# The devtools target installs tools required for 'go generate'.
# You need to put $GOBIN (or $GOPATH/bin) in your PATH to use 'go generate'.

devtools:
	env GOBIN= go get -u golang.org/x/tools/cmd/stringer
	env GOBIN= go get -u github.com/kevinburke/go-bindata/go-bindata
	env GOBIN= go get -u github.com/fjl/gencodec
	env GOBIN= go get -u github.com/golang/protobuf/protoc-gen-go
	env GOBIN= go install ./cmd/abigen
	@type "npm" 2> /dev/null || echo 'Please install node.js and npm'
	@type "solc" 2> /dev/null || echo 'Please install solc'
	@type "protoc" 2> /dev/null || echo 'Please install protoc'

# Cross Compilation Targets (xgo)

adln-cross: adln-linux adln-darwin adln-windows adln-android adln-ios
	@echo "Full cross compilation done:"
	@ls -ld $(GOBIN)/adln-*

adln-linux: adln-linux-386 adln-linux-amd64 adln-linux-arm adln-linux-mips64 adln-linux-mips64le
	@echo "Linux cross compilation done:"
	@ls -ld $(GOBIN)/adln-linux-*

adln-linux-386:
	build/env.sh go run build/ci.go xgo -- --go=$(GO) --targets=linux/386 -v ./cmd/adln
	@echo "Linux 386 cross compilation done:"
	@ls -ld $(GOBIN)/adln-linux-* | grep 386

adln-linux-amd64:
	build/env.sh go run build/ci.go xgo -- --go=$(GO) --targets=linux/amd64 -v ./cmd/adln
	@echo "Linux amd64 cross compilation done:"
	@ls -ld $(GOBIN)/adln-linux-* | grep amd64

adln-linux-arm: adln-linux-arm-5 adln-linux-arm-6 adln-linux-arm-7 adln-linux-arm64
	@echo "Linux ARM cross compilation done:"
	@ls -ld $(GOBIN)/adln-linux-* | grep arm

adln-linux-arm-5:
	build/env.sh go run build/ci.go xgo -- --go=$(GO) --targets=linux/arm-5 -v ./cmd/adln
	@echo "Linux ARMv5 cross compilation done:"
	@ls -ld $(GOBIN)/adln-linux-* | grep arm-5

adln-linux-arm-6:
	build/env.sh go run build/ci.go xgo -- --go=$(GO) --targets=linux/arm-6 -v ./cmd/adln
	@echo "Linux ARMv6 cross compilation done:"
	@ls -ld $(GOBIN)/adln-linux-* | grep arm-6

adln-linux-arm-7:
	build/env.sh go run build/ci.go xgo -- --go=$(GO) --targets=linux/arm-7 -v ./cmd/adln
	@echo "Linux ARMv7 cross compilation done:"
	@ls -ld $(GOBIN)/adln-linux-* | grep arm-7

adln-linux-arm64:
	build/env.sh go run build/ci.go xgo -- --go=$(GO) --targets=linux/arm64 -v ./cmd/adln
	@echo "Linux ARM64 cross compilation done:"
	@ls -ld $(GOBIN)/adln-linux-* | grep arm64

adln-linux-mips:
	build/env.sh go run build/ci.go xgo -- --go=$(GO) --targets=linux/mips --ldflags '-extldflags "-static"' -v ./cmd/adln
	@echo "Linux MIPS cross compilation done:"
	@ls -ld $(GOBIN)/adln-linux-* | grep mips

adln-linux-mipsle:
	build/env.sh go run build/ci.go xgo -- --go=$(GO) --targets=linux/mipsle --ldflags '-extldflags "-static"' -v ./cmd/adln
	@echo "Linux MIPSle cross compilation done:"
	@ls -ld $(GOBIN)/adln-linux-* | grep mipsle

adln-linux-mips64:
	build/env.sh go run build/ci.go xgo -- --go=$(GO) --targets=linux/mips64 --ldflags '-extldflags "-static"' -v ./cmd/adln
	@echo "Linux MIPS64 cross compilation done:"
	@ls -ld $(GOBIN)/adln-linux-* | grep mips64

adln-linux-mips64le:
	build/env.sh go run build/ci.go xgo -- --go=$(GO) --targets=linux/mips64le --ldflags '-extldflags "-static"' -v ./cmd/adln
	@echo "Linux MIPS64le cross compilation done:"
	@ls -ld $(GOBIN)/adln-linux-* | grep mips64le

adln-darwin: adln-darwin-386 adln-darwin-amd64
	@echo "Darwin cross compilation done:"
	@ls -ld $(GOBIN)/adln-darwin-*

adln-darwin-386:
	build/env.sh go run build/ci.go xgo -- --go=$(GO) --targets=darwin/386 -v ./cmd/adln
	@echo "Darwin 386 cross compilation done:"
	@ls -ld $(GOBIN)/adln-darwin-* | grep 386

adln-darwin-amd64:
	build/env.sh go run build/ci.go xgo -- --go=$(GO) --targets=darwin/amd64 -v ./cmd/adln
	@echo "Darwin amd64 cross compilation done:"
	@ls -ld $(GOBIN)/adln-darwin-* | grep amd64

adln-windows: adln-windows-386 adln-windows-amd64
	@echo "Windows cross compilation done:"
	@ls -ld $(GOBIN)/adln-windows-*

adln-windows-386:
	build/env.sh go run build/ci.go xgo -- --go=$(GO) --targets=windows/386 -v ./cmd/adln
	@echo "Windows 386 cross compilation done:"
	@ls -ld $(GOBIN)/adln-windows-* | grep 386

adln-windows-amd64:
	build/env.sh go run build/ci.go xgo -- --go=$(GO) --targets=windows/amd64 -v ./cmd/adln
	@echo "Windows amd64 cross compilation done:"
	@ls -ld $(GOBIN)/adln-windows-* | grep amd64
