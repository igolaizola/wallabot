package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/igolaizola/wallabot/internal/geo"
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

func (c *Client) Search(query string, items map[string]Item, callback func(Item) error) error {
	keywords := query
	var excludes []string
	var code, km int
	var min, max int
	split := strings.SplitN(query, "?", 2)
	if len(split) > 1 {
		query = split[0]
		values, err := url.ParseQuery(split[1])
		if err != nil {
			return fmt.Errorf("api: couldn't parse query %s", split[1])
		}
		code, err = fromValues(values, "code")
		if err != nil {
			return err
		}
		km, err = fromValues(values, "km")
		if err != nil {
			return err
		}
		min, err = fromValues(values, "min")
		if err != nil {
			return err
		}
		max, err = fromValues(values, "max")
		if err != nil {
			return err
		}
	}
	split = strings.SplitN(query, ":", 2)
	if len(split) > 1 {
		keywords = split[0]
		for _, e := range strings.Split(split[1], "+") {
			if e == "" {
				continue
			}
			excludes = append(excludes, e)
		}
	}
	var includes []string
	for _, i := range strings.Split(keywords, "+") {
		if i == "" {
			continue
		}
		includes = append(includes, strings.Replace(i, "&", " ", -1))
	}
	keywords = strings.Replace(keywords, "&", "+", -1)
	start := 0
	for {
		select {
		case <-c.ctx.Done():
			return nil
		default:
		}
		n, err := c.search(keywords, includes, excludes, code, km, min, max, start, items, callback)
		var netErr net.Error
		if errors.As(err, &netErr) && netErr.Timeout() {
			continue
		}
		if errors.Is(err, errBadGateway) {
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

var errBadGateway = errors.New("api: 502 bad gateway")

func (c *Client) search(keywords string, includes, excludes []string, code, km, min, max, start int, items map[string]Item, callback func(Item) error) (int, error) {
	values := url.Values{}
	values.Set("keywords", keywords)
	values.Set("order_by", "newest")
	values.Set("start", strconv.Itoa(start))
	values.Encode()
	if code > 0 {
		lat, long, ok := geo.LatLong(code)
		if !ok {
			return 0, fmt.Errorf("api: lat long not found for %d", code)
		}
		values.Set("latitude", fmt.Sprintf("%.5f", lat))
		values.Set("longitude", fmt.Sprintf("%.5f", long))
		if km > 0 {
			values.Set("distance", strconv.Itoa(km*1000))
		}
	}
	if min > 0 {
		values.Set("min_sale_price", strconv.Itoa(min))
	}
	if max > 0 {
		values.Set("max_sale_price", strconv.Itoa(max))
	}
	u := fmt.Sprintf("https://api.wallapop.com/api/v3/general/search?%s", values.Encode())
	r, err := c.client.Get(u)
	if err != nil {
		return 0, fmt.Errorf("api: get request failed: %w", err)
	}
	if r.StatusCode == 502 {
		return 0, errBadGateway
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
		skip := false
		for _, e := range excludes {
			if strings.Contains(strings.ToLower(obj.Title), strings.ToLower(e)) ||
				strings.Contains(strings.ToLower(obj.Description), strings.ToLower(e)) {
				skip = true
				break
			}
		}
		for _, i := range includes {
			if !strings.Contains(strings.ToLower(obj.Title), strings.ToLower(i)) &&
				!strings.Contains(strings.ToLower(obj.Description), strings.ToLower(i)) {
				skip = true
				break
			}
		}
		if skip {
			continue
		}
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

func fromValues(values url.Values, key string) (int, error) {
	t := values.Get(key)
	if t == "" {
		return 0, nil
	}
	v, err := strconv.Atoi(t)
	if err != nil {
		return 0, fmt.Errorf("api: couldn't parse int %s", t)
	}
	return v, nil
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
		case <-time.After(1000 * time.Millisecond):
		}
		t.lock.Unlock()
	}()
	return http.DefaultTransport.RoundTrip(r)
}
