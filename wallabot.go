package wallabot

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"

	tgbot "github.com/go-telegram-bot-api/telegram-bot-api"
)

var procs = map[string]*proc{}

type proc struct {
	name   string
	cancel context.CancelFunc
}

func Run(parent context.Context, token string) error {
	bot, err := tgbot.NewBotAPI(token)
	if err != nil {
		log.Panic(err)
	}

	bot.Debug = true

	log.Printf("Authorized on account %s", bot.Self.UserName)

	u := tgbot.NewUpdate(0)
	u.Timeout = 60

	updates, err := bot.GetUpdatesChan(u)

	for update := range updates {
		if update.Message == nil {
			continue
		}
		chatID := update.Message.Chat.ID
		if update.Message.IsCommand() {
			switch update.Message.Command() {
			case "search":
				args := update.Message.CommandArguments()
				if len(args) < 1 {
					msg := tgbot.NewMessage(chatID, "Search arguments not provided")
					bot.Send(msg)
					break
				}
				pID := fmt.Sprintf("%d_%s", chatID, args)
				if p, ok := procs[pID]; ok {
					p.cancel()
				}

				ctx, cancel := context.WithCancel(parent)
				procs[pID] = &proc{name: args, cancel: cancel}

				msg := tgbot.NewMessage(chatID, fmt.Sprintf("Searching %s", args))
				bot.Send(msg)

				go func() {
					ticker := time.NewTicker(1 * time.Minute)
					objs := make(map[string]struct{})
					if err := search(args, objs, func(string) error { return nil }); err != nil {
						log.Println(err)
						bot.Send(tgbot.NewMessage(chatID, err.Error()))
						return
					}
					for {
						fmt.Println("Searching newest...")
						if err := search(args, objs, func(text string) error {
							msg := tgbot.NewMessage(chatID, text)
							bot.Send(msg)
							return nil
						}); err != nil {
							log.Println(err)
							bot.Send(tgbot.NewMessage(chatID, err.Error()))
						}
						select {
						case <-ticker.C:
						case <-ctx.Done():
							ticker.Stop()
							return
						}
					}
				}()
				go func() {
					ticker := time.NewTicker(1 * time.Minute)
					objs := make(map[string]float64)
					for {
						fmt.Println("Searching price changes...")
						start := 0
						for {
							<-time.After(1 * time.Second)
							fmt.Println("Searching price changes", start)
							n, err := changes(args, start, objs, func(text string) error {
								msg := tgbot.NewMessage(chatID, text)
								bot.Send(msg)
								return nil
							})
							if err != nil {
								log.Println(err)
								bot.Send(tgbot.NewMessage(chatID, err.Error()))
							}
							if n == 0 {
								break
							}
							start += n
						}
						select {
						case <-ticker.C:
						case <-ctx.Done():
							ticker.Stop()
							return
						}
					}
				}()
			case "status":
				for _, p := range procs {
					msg := tgbot.NewMessage(chatID, fmt.Sprintf("Running %s", p.name))
					bot.Send(msg)
				}
			case "stop":
				args := update.Message.CommandArguments()
				if len(args) < 1 {
					msg := tgbot.NewMessage(update.Message.Chat.ID, "Stopping all")
					bot.Send(msg)
					for _, p := range procs {
						p.cancel()
					}
				}
				chatID := update.Message.Chat.ID
				pID := fmt.Sprintf("%d_%s", chatID, args)
				if p, ok := procs[pID]; ok {
					msg := tgbot.NewMessage(update.Message.Chat.ID, fmt.Sprintf("Stopping %s", args))
					bot.Send(msg)
					p.cancel()
				}
			}
		}
	}
	return nil
}

type Response struct {
	Objects []Object `json:"search_objects"`
}

type Object struct {
	Id          string  `json:"id"`
	Title       string  `json:"title"`
	Price       float64 `json:"price"`
	Currency    string  `json:"currency"`
	Description string  `json:"description"`
	Distance    float64 `json:"distance"`
	WebSlug     string  `json:"web_slug"`
}

func (o Object) Link() string {
	return fmt.Sprintf("http://p.wallapop.com/i/%s", o.ID())
}

func (o Object) ID() string {
	split := strings.Split(o.WebSlug, "-")
	return split[len(split)-1]
}

type Images struct {
	Original string `json:"original"`
}

func search(keywords string, objs map[string]struct{}, callback func(string) error) error {
	client := &http.Client{Timeout: 10 * time.Second}
	url := fmt.Sprintf("https://api.wallapop.com/api/v3/general/search?time_filter=today&keywords=%s&order_by=newest", keywords)
	r, err := client.Get(url)
	if err != nil {
		return fmt.Errorf("get request failed: %w", err)
	}
	if r.StatusCode != 200 {
		return fmt.Errorf("invalid status code: %s", r.Status)
	}
	defer r.Body.Close()
	var resp Response
	if err := json.NewDecoder(r.Body).Decode(&resp); err != nil {
		return fmt.Errorf("couldn't decode json: %w", err)
	}
	for _, obj := range resp.Objects {
		id := obj.ID()
		if _, ok := objs[id]; ok {
			continue
		}
		objs[id] = struct{}{}
		msg := fmt.Sprintf("%d€ %s %s", int(obj.Price), obj.Title, obj.Link())
		if err := callback(msg); err != nil {
			return err
		}
	}
	return nil
}

func changes(keywords string, start int, objs map[string]float64, callback func(string) error) (int, error) {
	client := &http.Client{Timeout: 10 * time.Second}
	url := fmt.Sprintf("https://api.wallapop.com/api/v3/general/search?keywords=%s&order_by=newest&start=%d", keywords, start)
	r, err := client.Get(url)
	if err != nil {
		return 0, fmt.Errorf("get request failed: %w", err)
	}
	if r.StatusCode != 200 {
		return 0, fmt.Errorf("invalid status code: %s", r.Status)
	}
	defer r.Body.Close()
	var resp Response
	if err := json.NewDecoder(r.Body).Decode(&resp); err != nil {
		return 0, fmt.Errorf("couldn't decode json: %w", err)
	}
	for _, obj := range resp.Objects {
		id := obj.ID()
		prev, ok := objs[id]
		objs[id] = obj.Price
		if !ok || prev == obj.Price {
			continue
		}
		msg := fmt.Sprintf("%d->%d€ %s %s", int(prev), int(obj.Price), obj.Title, obj.Link())
		if err := callback(msg); err != nil {
			return 0, err
		}
	}
	return len(resp.Objects), nil
}
