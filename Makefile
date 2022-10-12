.PHONY: build clean run help

GO=go

all: build run 

build:
	@$(GO) build loach.go

clean:
	rm loach

run:
	./loach -list=http://localhost:24023,http://localhost:24024,http://localhost:24025,http://localhost:24026

help:
	./loach -h
