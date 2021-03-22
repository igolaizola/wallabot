package main

import (
	"context"
	"flag"
	"log"

	"github.com/igolaizola/wallabot"
)

func main() {
	token := flag.String("token", "", "")
	flag.Parse()
	if *token == "" {
		log.Fatal("token not provided")
	}
	if err := wallabot.Run(context.Background(), *token); err != nil {
		log.Fatal(err)
	}
}
