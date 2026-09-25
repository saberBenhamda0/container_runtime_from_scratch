package main

import (
	"log"

	"docker-go/internal/filesystem"
)

func main() {
	if err := filesystem.Run(); err != nil {
		log.Fatal(err)
	}
}
