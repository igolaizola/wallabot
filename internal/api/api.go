package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
)

type Item struct {
	ID            string    `json:"id"`
	Link          string    `json:"link"`
	Title         string    `json:"title"`
	Price         float64   `json:"price"`
	PreviousPrice float64   `json:"previous_price"`
	CreatedAt     time.Time `json:"created_at"`
}

type response struct {
	Objects []object `json:"search_objects"`
}

type object struct {
	Id          string  `json:"id"`
	Title       string  `json:"title"`
	Price       float64 `json:"price"`
	Currency    string  `json:"currency"`
	Description string  `json:"description"`
	Distance    float64 `json:"distance"`
	WebSlug     string  `json:"web_slug"`
}

func Search(keywords string, items map[string]Item, callback func(Item) error) error {
	start := 0
	for {
		<-time.After(1 * time.Second)
		n, err := search(keywords, start, items, callback)
		if err != nil {
			return err
		}
		if n == 0 {
			break
		}
		start += n
	}
	return nil
}

func search(keywords string, start int, items map[string]Item, callback func(Item) error) (int, error) {
	client := &http.Client{Timeout: 10 * time.Second}
	url := fmt.Sprintf("https://api.wallapop.com/api/v3/general/search?keywords=%s&order_by=newest&start=%d", keywords, start)
	r, err := client.Get(url)
	if err != nil {
		return 0, fmt.Errorf("api: get request failed: %w", err)
	}
	if r.StatusCode != 200 {
		return 0, fmt.Errorf("api: invalid status code: %s", r.Status)
	}
	defer r.Body.Close()
	var resp response
	if err := json.NewDecoder(r.Body).Decode(&resp); err != nil {
		return 0, fmt.Errorf("api: couldn't decode json: %w", err)
	}
	for _, obj := range resp.Objects {
		split := strings.Split(obj.WebSlug, "-")
		id := split[len(split)-1]
		item := Item{
			ID:            id,
			Link:          fmt.Sprintf("http://p.wallapop.com/i/%s", id),
			Title:         obj.Title,
			Price:         obj.Price,
			PreviousPrice: -1,
			CreatedAt:     time.Now().UTC(),
		}
		prev, ok := items[item.ID]
		if ok {
			item.PreviousPrice = prev.Price
		}
		items[item.ID] = item
		if !ok || item.Price < prev.Price {
			if err := callback(item); err != nil {
				return 0, err
			}
		}

	}
	return len(resp.Objects), nil
}
