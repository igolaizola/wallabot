package wallabot

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	tgbot "github.com/go-telegram-bot-api/telegram-bot-api"
	"github.com/igolaizola/wallabot/internal/api"
	"github.com/igolaizola/wallabot/internal/store"
)

type bot struct {
	*tgbot.BotAPI
	db      *store.Store
	searchs sync.Map
	dups    sync.Map
	admin   int
	client  *api.Client
	wg      sync.WaitGroup
	elapsed time.Duration
}

func Run(ctx context.Context, token, dbPath string, admin int, users []int) error {
	db, err := store.New(dbPath)
	if err != nil {
		log.Fatal(err)
	}
	defer db.Close()

	client := &http.Client{
		Transport: &transport{
			ctx: ctx,
		},
	}
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
		admin:  admin,
	}

	bot.log(fmt.Sprintf("wallabot started, bot %s", bot.Self.UserName))
	defer bot.log(fmt.Sprintf("wallabot stoped, bot %s", bot.Self.UserName))
	defer bot.wg.Wait()

	if err != nil {
		bot.log(fmt.Errorf("couldn't get keys: %w", err))
	}
	keys, err := db.Keys()
	for _, k := range keys {
		if _, err := parseArgs(k, 0); err != nil {
			bot.log(fmt.Errorf("couldn't parse key %s: %w", k, err))
			continue
		}
		bot.searchs.Store(k, struct{}{})
		bot.log(fmt.Sprintf("loaded from db: %s", k))
	}

	bot.wg.Add(1)
	go func() {
		defer log.Println("search routine finished")
		defer bot.wg.Done()
		for {
			select {
			case <-ctx.Done():
				return
			default:
			}
			start := time.Now()
			var keys []string
			bot.searchs.Range(func(k interface{}, _ interface{}) bool {
				keys = append(keys, k.(string))
				return true
			})

			sort.Strings(keys)
			for _, k := range keys {
				log.Println(fmt.Sprintf("searching: %s", k))
				select {
				case <-ctx.Done():
					return
				default:
				}
				if _, ok := bot.searchs.Load(k); !ok {
					continue
				}
				parsed, err := parseArgs(k, 0)
				if err != nil {
					bot.log(fmt.Errorf("couldn't parse key %s: %w", k, err))
					continue
				}
				bot.search(ctx, parsed)
			}
			bot.elapsed = time.Since(start)
			log.Println(fmt.Sprintf("elapsed: %s", bot.elapsed))
		}
	}()

	u := tgbot.NewUpdate(0)
	u.Timeout = 60
	updates, err := bot.GetUpdatesChan(u)
	for {
		var update tgbot.Update
		select {
		case <-ctx.Done():
			log.Println("stopping bot")
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
				} else {
					bot.searchs.Store(parsed.id, struct{}{})
				}
				bot.message(user, fmt.Sprintf("searching %s", parsed.id))
			case "status":
				bot.message(user, "status info:")
				bot.searchs.Range(func(k interface{}, _ interface{}) bool {
					bot.message(user, fmt.Sprintf("running %s", k.(string)))
					return true
				})
				bot.log(fmt.Sprintf("elapsed: %s", bot.elapsed))
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
	p.chat = strings.ToLower(strings.Trim(p.chat, " "))
	p.query = strings.ReplaceAll(strings.Trim(strings.ToLower(p.query), " "), " ", "+")
	p.id = fmt.Sprintf("%s/%s", p.chat, p.query)
	return p, nil
}

func (b *bot) search(ctx context.Context, parsed parsedArgs) {
	if parsed.query == "" {
		return
	}

	items := make(map[string]api.Item)
	if err := b.db.Get(parsed.id, &items); err != nil {
		b.log(err)
		return
	}
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
	if err := b.client.Search(parsed.query, items, func(i api.Item) error {
		dupID := fmt.Sprintf("%s/%s/%.2f-%.2f", parsed.chat, i.ID, i.Price, i.PreviousPrice)
		// TODO(igolaizola): sync this
		if _, ok := b.dups.Load(dupID); ok {
			return nil
		}
		text := newAdMessage(i, parsed.chat)
		if i.PreviousPrice > i.Price {
			text = priceDownMessage(i, parsed.chat)
		}
		b.message(parsed.chat, text)
		b.dups.Store(dupID, struct{}{})
		return nil
	}); err != nil {
		b.log(err)
	}
	if len(items) == 0 {
		return
	}
	if _, ok := b.searchs.Load(parsed.id); !ok {
		return
	}
	if err := b.db.Put(parsed.id, items); err != nil {
		b.log(err)
		return
	}
}

func (b *bot) stopAll() {
	b.log("stopping all")
	var keys []string
	b.searchs.Range(func(k interface{}, _ interface{}) bool {
		keys = append(keys, k.(string))
		return true
	})
	for _, k := range keys {
		b.log(fmt.Sprintf("stopping %s", k))
		b.searchs.Delete(k)
		if err := b.db.Delete(k); err != nil {
			b.log(err)
		}
	}
}
func (b *bot) stop(parsed parsedArgs) {
	if _, ok := b.searchs.Load(parsed.id); ok {
		b.log(fmt.Sprintf("stopping %s", parsed.id))
		b.searchs.Delete(parsed.id)
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
	<-time.After(100 * time.Millisecond)
}

func (b *bot) log(obj interface{}) {
	text := fmt.Sprintf("%s", obj)
	log.Println(text)
	if _, err := b.Send(tgbot.NewMessage(int64(b.admin), text)); err != nil {
		log.Println(fmt.Errorf("couldn't send error to admin %d: %w", b.admin, err))
	}
	<-time.After(100 * time.Millisecond)
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
