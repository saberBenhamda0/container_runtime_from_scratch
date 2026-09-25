package main

import (
	"log"

	"docker-go/internal/networking"
)

func main() {
	if err := networking.Run(); err != nil {
		log.Fatal(err)
	}
}
