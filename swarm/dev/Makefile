.PHONY: build cluster test

default: build

build:
	go build -o bin/swarm    github.com/AdelineCoin/go-adln/cmd/swarm
	go build -o bin/geth     github.com/AdelineCoin/go-adln/cmd/geth
	go build -o bin/bootnode github.com/AdelineCoin/go-adln/cmd/bootnode

cluster: build
	scripts/boot-cluster.sh

test:
	go test -v github.com/AdelineCoin/go-adln/swarm/...
