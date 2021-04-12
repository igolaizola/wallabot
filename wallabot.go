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
	cancel context.CancelFunc
}

type bot struct {
	*tgbot.BotAPI
	db     *store.Store
	procs  map[string]*proc
	dups   map[string]struct{}
	admin  int
	client *api.Client
	wg     sync.WaitGroup
}

func Run(ctx context.Context, token, dbPath string, admin int, users []int) error {
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

	users = append(users, admin)
	allowedUsers := make(map[int]struct{})
	for _, u := range users {
		allowedUsers[u] = struct{}{}
	}

	bot := &bot{
		BotAPI: botAPI,
		db:     db,
		client: api.New(ctx),
		procs:  make(map[string]*proc),
		admin:  admin,
		dups:   make(map[string]struct{}),
	}

	bot.log(fmt.Sprintf("wallabot started, bot %s", bot.Self.UserName))
	defer bot.log(fmt.Sprintf("wallabot stoped, bot %s", bot.Self.UserName))

	keys, err := db.Keys()
	if err != nil {
		bot.log(fmt.Errorf("couldn't get keys: %w", err))
	}
	for _, k := range keys {
		parsed, err := parseArgs(k, 0)
		if err != nil {
			bot.log(fmt.Errorf("couldn't parse key %s: %w", k, err))
			continue
		}
		bot.search(ctx, parsed)
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
		user := int(update.Message.Chat.ID)
		if _, ok := allowedUsers[user]; !ok {
			continue
		}
		if update.Message.IsCommand() {
			args := update.Message.CommandArguments()
			switch update.Message.Command() {
			case "search":
				if args == "" {
					bot.message(user, "search arguments not provided")
					continue
				}
				parsed, err := parseArgs(args, user)
				if err != nil {
					bot.message(user, err.Error())
				}
				bot.search(ctx, parsed)
				bot.message(user, fmt.Sprintf("searching %s", parsed.id))
			case "status":
				bot.message(user, "status info:")
				for k := range bot.procs {
					bot.message(user, fmt.Sprintf("running %s", k))
				}
			case "stop":
				if args == "" {
					bot.message(user, "stop arguments not provided")
					continue
				}
				parsed, err := parseArgs(args, user)
				if err != nil {
					bot.message(user, err.Error())
				}
				if parsed.query == "*" {
					bot.stopAll()
					bot.message(user, "stopped all")
				} else {
					bot.stop(parsed)
					bot.message(user, fmt.Sprintf("stopped %s", parsed.id))
				}
			}
		}
	}
}

type parsedArgs struct {
	id    string
	chat  string
	query string
}

func parseArgs(args string, user int) (parsedArgs, error) {
	split := strings.Split(args, "/")
	p := parsedArgs{
		chat:  strconv.Itoa(user),
		query: split[0],
	}
	switch len(split) {
	case 1:
	default:
		p.chat = split[0]
		p.query = split[1]
	}
	p.chat = strings.ToLower(strings.Trim(p.chat, ""))
	p.query = strings.ReplaceAll(strings.ToLower(p.query), " ", "+")
	p.id = fmt.Sprintf("%s/%s", p.chat, p.query)
	return p, nil
}

func (b *bot) search(ctx context.Context, parsed parsedArgs) {
	if parsed.query == "" {
		return
	}
	if p, ok := b.procs[parsed.id]; ok {
		p.cancel()
	}

	ctx, cancel := context.WithCancel(ctx)
	b.procs[parsed.id] = &proc{cancel: cancel}

	b.wg.Add(1)
	go func() {
		defer b.wg.Done()
		items := make(map[string]api.Item)
		if err := b.db.Get(parsed.id, &items); err != nil {
			b.log(err)
			return
		}
		b.log(fmt.Sprintf("searching %s, %d items loaded", parsed.id, len(items)))
		ticker := time.NewTicker(1 * time.Minute)
		if len(items) == 0 {
			// store search with empty items on db
			if err := b.db.Put(parsed.id, items); err != nil {
				b.log(err)
				return
			}
			if err := b.client.Search(parsed.query, items, func(api.Item) error { return nil }); err != nil {
				b.log(err)
				return
			}
		}
		for {
			if err := b.client.Search(parsed.query, items, func(i api.Item) error {
				dupID := fmt.Sprintf("%s/%s/%.2f-%.2f", parsed.chat, i.ID, i.Price, i.PreviousPrice)
				// TODO(igolaizola): sync this
				if _, ok := b.dups[dupID]; ok {
					return nil
				}
				text := newAdMessage(i, parsed.chat)
				if i.PreviousPrice > i.Price {
					text = priceDownMessage(i, parsed.chat)
				}
				b.message(parsed.chat, text)
				b.dups[dupID] = struct{}{}
				return nil
			}); err != nil {
				b.log(err)
			}
			if len(items) > 0 {
				if err := b.db.Put(parsed.id, items); err != nil {
					b.log(err)
					return
				}
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

func (b *bot) stopAll() {
	b.log("stopping all")
	for k, p := range b.procs {
		b.log(fmt.Sprintf("stopping %s", k))
		p.cancel()
		delete(b.procs, k)
		if err := b.db.Delete(k); err != nil {
			b.log(err)
		}
	}
}
func (b *bot) stop(parsed parsedArgs) {
	if p, ok := b.procs[parsed.id]; ok {
		b.log(fmt.Sprintf("stopping %s", parsed.id))
		p.cancel()
		delete(b.procs, parsed.id)
		if err := b.db.Delete(parsed.id); err != nil {
			b.log(err)
		}
	}
}

func (b *bot) message(chat interface{}, text string) {
	var msg tgbot.MessageConfig
	switch v := chat.(type) {
	case string:
		msg = tgbot.NewMessageToChannel(v, text)
	case int64:
		msg = tgbot.NewMessage(v, text)
	case int:
		msg = tgbot.NewMessage(int64(v), text)
	default:
		b.log(fmt.Sprintf("invalid type for message: %T", chat))
	}
	if _, err := b.Send(msg); err != nil {
		b.log(fmt.Errorf("couldn't send message to %v: %w", chat, err))
	}
}

func (b *bot) log(obj interface{}) {
	text := fmt.Sprintf("%s", obj)
	log.Println(text)
	if _, err := b.Send(tgbot.NewMessage(int64(b.admin), text)); err != nil {
		log.Println(fmt.Errorf("couldn't send error to admin %d: %w", b.admin, err))
	}
}

func newAdMessage(i api.Item, chat string) string {
	bottom := ""
	if strings.HasPrefix(chat, "@") {
		bottom = fmt.Sprintf("\n\n📣 Más anuncios en %s", chat)
	}
	return fmt.Sprintf("‼️ NUEVO ANUNCIO\n\n%s\n\n✅ Precio: %.2f€\n\n🔗 %s%s",
		i.Title, i.Price, i.Link, bottom)
}

func priceDownMessage(i api.Item, chat string) string {
	bottom := ""
	if strings.HasPrefix(chat, "@") {
		bottom = fmt.Sprintf("\n\n📣 Más anuncios en %s", chat)
	}
	return fmt.Sprintf("⚡️ BAJADA DE PRECIO\n\n%s\n\n✅ Precio: %.2f€\n🚫 Anterior: %.2f€\n\n🔗 %s%s",
		i.Title, i.Price, i.PreviousPrice, i.Link, bottom)
}
