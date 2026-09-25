package crawl

import (
	"context"
	"database/sql"
	"errors"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/lutzifer/burpsuite-clone/internal/repeater"
	"github.com/lutzifer/burpsuite-clone/internal/store"
)

var (
	ErrInvalidInput = errors.New("invalid crawl input")
	ErrScopeDenied  = errors.New("crawl target outside scope")
	ErrBusy         = errors.New("crawler already running")
)

type History interface {
	GetExchange(context.Context, int64) (*store.Exchange, error)
}
type Scope interface{ Allows(string) bool }
type Request struct {
	HistoryID   int64 `json:"historyId"`
	MaxPages    int   `json:"maxPages"`
	MaxDepth    int   `json:"maxDepth"`
	Acknowledge bool  `json:"acknowledge"`
}
type Report struct {
	RunID int64  `json:"runId"`
	State string `json:"state"`
}
type Crawler struct {
	history    History
	repository store.CrawlStore
	scope      Scope
	sender     repeater.Sender
	mu         sync.Mutex
	runID      int64
	cancel     context.CancelFunc
}

func New(history History, repository store.CrawlStore, scope Scope, sender repeater.Sender) (*Crawler, error) {
	if history == nil || repository == nil || scope == nil || sender == nil {
		return nil, errors.New("crawler dependencies unavailable")
	}
	return &Crawler{history: history, repository: repository, scope: scope, sender: sender}, nil
}

func (c *Crawler) Start(ctx context.Context, input Request) (Report, error) {
	if input.HistoryID < 1 || input.MaxPages < 1 || input.MaxPages > 25 || input.MaxDepth < 0 || input.MaxDepth > 3 || !input.Acknowledge {
		return Report{}, ErrInvalidInput
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.cancel != nil {
		return Report{}, ErrBusy
	}
	exchange, err := c.history.GetExchange(ctx, input.HistoryID)
	if err != nil {
		return Report{}, err
	}
	if exchange.Method != "GET" || !exchange.InScope || exchange.Error || (exchange.Scheme != "http" && exchange.Scheme != "https") {
		return Report{}, ErrInvalidInput
	}
	seed, err := NormalizeURL((&url.URL{Scheme: exchange.Scheme, Host: exchange.Host, Path: exchange.Path, RawQuery: exchange.Query}).String())
	if err != nil {
		return Report{}, ErrInvalidInput
	}
	if !c.scope.Allows(seed) {
		return Report{}, ErrScopeDenied
	}
	id, err := c.repository.CreateCrawlRun(ctx, exchange.ID, input.MaxPages, input.MaxDepth)
	if err != nil {
		return Report{}, err
	}
	runCtx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	c.runID = id
	c.cancel = cancel
	go c.run(runCtx, id, seed, input)
	return Report{RunID: id, State: "running"}, nil
}

func (c *Crawler) Cancel(id int64) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if id < 1 || c.runID != id || c.cancel == nil {
		return sql.ErrNoRows
	}
	c.cancel()
	return nil
}

type queuedPage struct {
	url   string
	depth int
}

func (c *Crawler) run(ctx context.Context, id int64, seed string, input Request) {
	state, reason := "completed", ""
	defer func() {
		persistCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = c.repository.FinishCrawlRun(persistCtx, id, state, reason)
		c.mu.Lock()
		c.cancel()
		c.cancel = nil
		c.runID = 0
		c.mu.Unlock()
	}()
	seedURL, _ := url.Parse(seed)
	queue := []queuedPage{{seed, 0}}
	seen := map[string]bool{seed: true}
	lastSend := time.Time{}
	fieldCount := 0
	formCount := 0
	for visited := 0; len(queue) > 0 && visited < input.MaxPages; visited++ {
		item := queue[0]
		queue = queue[1:]
		if delay := time.Until(lastSend.Add(500 * time.Millisecond)); delay > 0 {
			timer := time.NewTimer(delay)
			select {
			case <-ctx.Done():
				timer.Stop()
				state = "cancelled"
				reason = "cancelled"
				return
			case <-timer.C:
			}
		}
		if ctx.Err() != nil {
			state = "cancelled"
			reason = "cancelled"
			return
		}
		if !c.scope.Allows(item.url) {
			state = "scope_revoked"
			reason = "scope_revoked"
			return
		}
		lastSend = time.Now()
		result, sendErr := c.sender.Send(ctx, repeater.SendRequest{Method: "GET", URL: item.url, Headers: map[string][]string{"User-Agent": {"BurpSuiteClone-Crawler/1"}}}, repeater.SendOptions{Timeout: 5 * time.Second, BodyLimitBytes: 64 << 10})
		page := store.CrawlPage{URL: item.url, Depth: item.depth}
		var links []string
		var forms []Form
		if sendErr != nil {
			if ctx.Err() != nil {
				state = "cancelled"
				reason = "cancelled"
				return
			}
			page.Error = "request_failed"
		} else {
			page.Status = result.Status
			page.Truncated = result.Truncated
			page.ContentType = result.ContentType
			if len(page.ContentType) > 128 {
				page.ContentType = page.ContentType[:128]
			}
			if strings.HasPrefix(strings.ToLower(result.ContentType), "text/html") {
				links, forms, _ = ExtractHTML(item.url, result.Body)
			}
		}
		var savedForms []store.CrawlForm
		for _, form := range forms {
			if fieldCount >= 100 || formCount >= 100 {
				break
			}
			if len(form.Method) > 16 || len(form.PageURL) > 4096 || len(form.ActionURL) > 4096 {
				continue
			}
			converted := store.CrawlForm{PageURL: form.PageURL, ActionURL: form.ActionURL, Method: form.Method, Fields: []store.CrawlField{}}
			for _, field := range form.Fields {
				if fieldCount >= 100 {
					break
				}
				converted.Fields = append(converted.Fields, store.CrawlField{Name: field.Name, Type: field.Type})
				fieldCount++
			}
			savedForms = append(savedForms, converted)
			formCount++
		}
		persistCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		err := c.repository.AppendCrawlPage(persistCtx, id, page, savedForms)
		cancel()
		if err != nil {
			state = "failed"
			reason = "storage_failed"
			return
		}
		if item.depth >= input.MaxDepth {
			continue
		}
		for _, link := range links {
			if seen[link] || len(seen) >= input.MaxPages*10 {
				continue
			}
			target, _ := url.Parse(link)
			if target.Scheme != seedURL.Scheme || !strings.EqualFold(target.Host, seedURL.Host) || !c.scope.Allows(link) {
				continue
			}
			seen[link] = true
			queue = append(queue, queuedPage{link, item.depth + 1})
		}
	}
}
