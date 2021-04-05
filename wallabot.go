package wallabot

import (
	"context"
	"fmt"
	"log"
	"strconv"
	"time"

	tgbot "github.com/go-telegram-bot-api/telegram-bot-api"
	"github.com/igolaizola/wallabot/internal/api"
	"github.com/igolaizola/wallabot/internal/store"
)

type proc struct {
	name   string
	cancel context.CancelFunc
}

type bot struct {
	*tgbot.BotAPI
	db    *store.Store
	procs map[string]*proc
	admin int64
	chat  string
}

func Run(ctx context.Context, token, dbPath string, admin int, chat string) error {
	db, err := store.New(dbPath)
	if err != nil {
		log.Fatal(err)
	}
	defer db.Close()

	botAPI, err := tgbot.NewBotAPI(token)
	if err != nil {
		return fmt.Errorf("couldn't create bot api: %w", err)
	}
	botAPI.Debug = true
	bot := &bot{
		BotAPI: botAPI,
		db:     db,
		procs:  make(map[string]*proc),
		admin:  int64(admin),
		chat:   chat,
	}

	bot.log(fmt.Sprintf("wallabot started, bot %s", bot.Self.UserName))
	defer bot.log(fmt.Sprintf("wallabot stoped, bot %s", bot.Self.UserName))

	keys, err := db.Keys()
	if err != nil {
		bot.log(fmt.Errorf("couldn't get keys: %w", err))
	}
	for _, k := range keys {
		bot.search(ctx, k)
	}

	u := tgbot.NewUpdate(0)
	u.Timeout = 60

	updates, err := bot.GetUpdatesChan(u)

	for update := range updates {
		if update.Message == nil {
			continue
		}
		if update.Message.Chat.ID != bot.admin {
			continue
		}
		if update.Message.IsCommand() {
			args := update.Message.CommandArguments()
			switch update.Message.Command() {
			case "search":
				if args == "" {
					bot.log("search arguments not provided")
					break
				}
				bot.search(ctx, args)
			case "status":
				bot.log("status info:")
				for _, p := range bot.procs {
					bot.log(fmt.Sprintf("running %s", p.name))
				}
			case "stop":
				bot.stop(args)
			}
		}
	}
	return nil
}

func (b *bot) search(ctx context.Context, args string) {
	if args == "" {
		return
	}
	if p, ok := b.procs[args]; ok {
		p.cancel()
	}

	ctx, cancel := context.WithCancel(ctx)
	b.procs[args] = &proc{name: args, cancel: cancel}

	go func() {
		items := make(map[string]api.Item)
		if err := b.db.Get(args, &items); err != nil {
			b.log(err)
			return
		}
		b.log(fmt.Sprintf("searching %s, %d items loaded", args, len(items)))
		ticker := time.NewTicker(1 * time.Minute)
		if err := api.Search(args, items, func(api.Item) error { return nil }); err != nil {
			b.log(err)
			return
		}
		for {
			if err := api.Search(args, items, func(i api.Item) error {
				text := newAdMessage(i)
				if i.PreviousPrice > i.Price {
					text = priceDownMessage(i)
				}
				b.message(text)
				return nil
			}); err != nil {
				b.log(err)
			}
			if err := b.db.Put(args, items); err != nil {
				b.log(err)
				return
			}
			select {
			case <-ticker.C:
			case <-ctx.Done():
				ticker.Stop()
				return
			}
		}
	}()
}

func (b *bot) stop(args string) {
	if len(args) < 1 {
		b.log("stopping all")
		for k, p := range b.procs {
			b.log(fmt.Sprintf("stopping %s", k))
			p.cancel()
			delete(b.procs, k)
		}
	}
	if p, ok := b.procs[args]; ok {
		b.log(fmt.Sprintf("stopping %s", args))
		p.cancel()
		delete(b.procs, args)
	}
}

func (b *bot) message(text string) {
	msg := tgbot.NewMessageToChannel(b.chat, text)
	if chatID, err := strconv.Atoi(b.chat); err == nil {
		msg = tgbot.NewMessage(int64(chatID), text)
	}
	if _, err := b.Send(msg); err != nil {
		b.log(fmt.Errorf("couldn't send message to channel %s: %w", b.chat, err))
	}
}

func (b *bot) log(obj interface{}) {
	text := fmt.Sprintf("%s", obj)
	log.Println(text)
	if _, err := b.Send(tgbot.NewMessage(b.admin, text)); err != nil {
		log.Println(fmt.Errorf("couldn't send error to admin %d: %w", b.admin, err))
	}
}

func newAdMessage(i api.Item) string {
	return fmt.Sprintf("‼️ NUEVO ANUNCIO\n\n%s\n\n✅ Precio: %.2f€\n\n🔗 %s\n\n📣 Más anuncios en @stadiapop",
		i.Title, i.Price, i.Link)
}

func priceDownMessage(i api.Item) string {
	return fmt.Sprintf("⚡️ BAJADA DE PRECIO\n\n%s\n\n✅ Precio: %.2f€\n🚫 Anterior: %.2f€\n\n🔗 %s\n\n📣 Más anuncios en @stadiapop",
		i.Title, i.Price, i.PreviousPrice, i.Link)
}
