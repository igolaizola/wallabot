package main

import (
	"context"
	"flag"
	"log"

	"github.com/igolaizola/wallabot"
)

func main() {
	token := flag.String("token", "", "telegram bot token")
	db := flag.String("db", "wallabot.db", "database file path")
	admin := flag.Int("admin", 0, "admin chat id that controls the bot")
	chat := flag.String("chat", "", "chat id or channel name to post messages")
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
	if *token == "" {
		log.Fatal("chat not provided")
	}
	if err := wallabot.Run(context.Background(), *token, *db, *admin, *chat); err != nil {
		log.Fatal(err)
	}
}
