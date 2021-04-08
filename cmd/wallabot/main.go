package main

import (
	"context"
	"flag"
	"log"
	"os"
	"os/signal"

	"github.com/igolaizola/wallabot"
)

func main() {
	// Parse flags
	token := flag.String("token", "", "telegram bot token")
	db := flag.String("db", "wallabot.db", "database file path")
	admin := flag.Int("admin", 0, "admin chat id that controls the bot")
	flag.Parse()
	if *token == "" {
		log.Fatal("token not provided")
	}
	if *db == "" {
		log.Fatal("db not provided")
	}
	if *admin <= 0 {
		log.Fatal("admin provided")
	}

	// Create signal based context
	ctx, cancel := context.WithCancel(context.Background())
	c := make(chan os.Signal, 1)
	signal.Notify(c, os.Interrupt, os.Kill)
	go func() {
		select {
		case <-c:
			cancel()
		case <-ctx.Done():
			cancel()
		}
		signal.Stop(c)
	}()

	// Run bot
	if err := wallabot.Run(ctx, *token, *db, *admin); err != nil {
		log.Fatal(err)
	}
}
