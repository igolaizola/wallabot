package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strings"
	"sync"
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

type Client struct {
	client *http.Client
	ctx    context.Context
}

func New(ctx context.Context) *Client {
	return &Client{
		ctx: ctx,
		client: &http.Client{
			Timeout: 10 * time.Second,
			Transport: &transport{
				ctx: ctx,
			},
		},
	}
}

func (c *Client) Search(keywords string, items map[string]Item, callback func(Item) error) error {
	start := 0
	for {
		select {
		case <-c.ctx.Done():
			return nil
		default:
		}
		n, err := c.search(keywords, start, items, callback)
		var netErr net.Error
		if errors.As(err, &netErr) && netErr.Timeout() {
			continue
		}
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

func (c *Client) search(keywords string, start int, items map[string]Item, callback func(Item) error) (int, error) {
	url := fmt.Sprintf("https://api.wallapop.com/api/v3/general/search?keywords=%s&order_by=newest&start=%d", keywords, start)
	r, err := c.client.Get(url)
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

type transport struct {
	lock sync.Mutex
	ctx  context.Context
}

func (t *transport) RoundTrip(r *http.Request) (*http.Response, error) {
	t.lock.Lock()
	defer func() {
		select {
		case <-t.ctx.Done():
		case <-time.After(500 * time.Millisecond):
		}
		t.lock.Unlock()
	}()
	return http.DefaultTransport.RoundTrip(r)
}
