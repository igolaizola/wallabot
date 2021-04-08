package wallabot

import (
	"context"
	"fmt"
	"log"
	"strconv"
	"strings"
	"sync"
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
	db     *store.Store
	procs  map[string]*proc
	admin  int64
	client *api.Client
	wg     sync.WaitGroup
}

func Run(ctx context.Context, token, dbPath string, admin int) error {
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
		client: api.New(ctx),
		procs:  make(map[string]*proc),
		admin:  int64(admin),
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

	for {
		var update tgbot.Update
		select {
		case <-ctx.Done():
			bot.wg.Wait()
			return nil
		case update = <-updates:
		}
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
				if len(strings.Split(args, "/")) < 2 {
					args = fmt.Sprintf("%d/%s", update.Message.Chat.ID, args)
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

	split := strings.Split(args, "/")
	if len(split) < 2 {
		b.log(fmt.Sprintf("invalid args %s", args))
		return
	}
	chat := split[0]
	keywords := split[1]

	b.wg.Add(1)
	go func() {
		defer b.wg.Done()
		items := make(map[string]api.Item)
		if err := b.db.Get(args, &items); err != nil {
			b.log(err)
			return
		}
		b.log(fmt.Sprintf("searching %s, %d items loaded", args, len(items)))
		ticker := time.NewTicker(1 * time.Minute)
		if len(items) == 0 {
			if err := b.client.Search(keywords, items, func(api.Item) error { return nil }); err != nil {
				b.log(err)
				return
			}
		}
		for {
			if err := b.client.Search(keywords, items, func(i api.Item) error {
				text := newAdMessage(i, chat)
				if i.PreviousPrice > i.Price {
					text = priceDownMessage(i, chat)
				}
				b.message(chat, text)
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

func (b *bot) message(chat, text string) {
	msg := tgbot.NewMessageToChannel(chat, text)
	if chatID, err := strconv.Atoi(chat); err == nil {
		msg = tgbot.NewMessage(int64(chatID), text)
	}
	if _, err := b.Send(msg); err != nil {
		b.log(fmt.Errorf("couldn't send message to channel %s: %w", chat, err))
	}
}

func (b *bot) log(obj interface{}) {
	text := fmt.Sprintf("%s", obj)
	log.Println(text)
	if _, err := b.Send(tgbot.NewMessage(b.admin, text)); err != nil {
		log.Println(fmt.Errorf("couldn't send error to admin %d: %w", b.admin, err))
	}
}

func newAdMessage(i api.Item, chat string) string {
	return fmt.Sprintf("‼️ NUEVO ANUNCIO\n\n%s\n\n✅ Precio: %.2f€\n\n🔗 %s\n\n📣 Más anuncios en %s",
		i.Title, i.Price, i.Link, chat)
}

func priceDownMessage(i api.Item, chat string) string {
	return fmt.Sprintf("⚡️ BAJADA DE PRECIO\n\n%s\n\n✅ Precio: %.2f€\n🚫 Anterior: %.2f€\n\n🔗 %s\n\n📣 Más anuncios en %s",
		i.Title, i.Price, i.PreviousPrice, i.Link, chat)
}
