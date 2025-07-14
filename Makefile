all: clean vendor build

build:
	go build

vendor:
	go mod vendor

clean:
	rm -Rf kafka-connector vendor

format:
	go fmt

