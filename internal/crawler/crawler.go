package crawler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"path"
	"runtime"
	"slices"
	"strings"
	"sync"
	"time"
)

type Crawler interface {
	ListenAndServe(ctx context.Context, address string) error
}

type CrawlRequest struct {
	URLs      []string `json:"urls"`
	Workers   int      `json:"workers"`
	TimeoutMS int      `json:"timeout_ms"`
}

type CrawlResponse struct {
	URL        string `json:"url"`
	StatusCode int    `json:"status_code,omitempty"`
	Error      string `json:"error,omitempty"`
}

var _ Crawler = (*crawlerImpl)(nil)

type crawlerImpl struct {
	client          *http.Client
	cacheMx         *sync.RWMutex
	cache           map[string]cacheEntry
	urlSingleFlight singleFlight[CrawlResponse]
}

const (
	shutdownTimeout = 10 * time.Second
	workersLimit    = 1 << 10
	cacheTTL        = time.Second
)

func New() *crawlerImpl {
	return &crawlerImpl{
		cache:           make(map[string]cacheEntry),
		urlSingleFlight: newSingleFlight[CrawlResponse](),
		cacheMx:         new(sync.RWMutex),
		client: &http.Client{
			Transport: &http.Transport{
				Proxy: http.ProxyFromEnvironment,
				DialContext: (&net.Dialer{
					Timeout:   30 * time.Second,
					KeepAlive: 30 * time.Second,
				}).DialContext,
				ForceAttemptHTTP2:     true,
				MaxIdleConns:          100,
				MaxIdleConnsPerHost:   runtime.GOMAXPROCS(-1),
				IdleConnTimeout:       90 * time.Second,
				TLSHandshakeTimeout:   10 * time.Second,
				ExpectContinueTimeout: time.Second * 3,
				ResponseHeaderTimeout: time.Minute,
			},
		},
	}
}

func (c *crawlerImpl) ListenAndServe(ctx context.Context, address string) error {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /crawl", c.crawlHandler)

	srv := &http.Server{
		Addr:    address,
		Handler: mux,
	}

	go func() {
		<-ctx.Done()

		shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
		defer cancel()

		if err := srv.Shutdown(shutdownCtx); err != nil {
			log.Printf("http server shutdown error: %v", err)
		}
	}()

	if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}

	return nil
}

var ErrInvalidRequest = errors.New("invalid request")

func (c *CrawlRequest) Validate() error {
	if c.Workers <= 0 || c.Workers > workersLimit {
		return fmt.Errorf("unexpected workers number: %d %w", c.Workers, ErrInvalidRequest)
	}

	var errs []error

	for i := range c.URLs {
		_, err := url.Parse(c.URLs[i])

		if err != nil {
			errs = append(errs, err)
		}
	}

	if len(errs) != 0 {
		return errors.Join(errs...)
	}

	return nil
}

func (c *crawlerImpl) crawlHandler(w http.ResponseWriter, r *http.Request) {
	var req CrawlRequest
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()

	if err := dec.Decode(&req); err != nil {
		http.Error(w, "bad json: "+err.Error(), http.StatusBadRequest)
		return
	}

	if err := req.Validate(); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	if len(req.URLs) == 0 {
		writeJSON(w, http.StatusOK, []CrawlResponse{})
		return
	}

	urls := make([]*url.URL, 0, len(req.URLs))
	for i := range req.URLs {
		parsed, err := url.Parse(req.URLs[i])

		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		normalizeURL(parsed)
		urls = append(urls, parsed)
	}

	ctx, cancel := context.WithTimeout(r.Context(), time.Duration(req.TimeoutMS)*time.Millisecond)
	defer cancel()

	results := make([]CrawlResponse, len(urls))

	for i := range results {
		results[i].URL = req.URLs[i]
	}

	type job struct {
		i   int
		url *url.URL
	}

	jobs := Generate(ctx, urls, func(index int, e *url.URL) job {
		return job{
			i:   index,
			url: e,
		}
	}, 1)

	Run(ctx, min(req.Workers, len(req.URLs)), jobs, func(j job) {
		if err := ctx.Err(); err != nil {
			results[j.i].Error = err.Error()
			return
		}

		res := c.fetchWithCache(ctx, j.url)
		results[j.i].Error = res.Error
		results[j.i].StatusCode = res.StatusCode
	})

	writeJSON(w, http.StatusOK, results)
}

type cacheEntry struct {
	result    CrawlResponse
	expiredAt time.Time
}

func (c *crawlerImpl) tryFetchFromCache(u *url.URL) (CrawlResponse, bool) {
	var (
		result CrawlResponse
		exists bool
		fresh  bool
	)

	withLock(c.cacheMx.RLocker(), func() {
		ce, ok := c.cache[u.String()]

		if !ok {
			return
		}

		exists = true

		if time.Now().Before(ce.expiredAt) {
			fresh = true
			result = ce.result
		}
	})

	if exists && !fresh {
		withLock(c.cacheMx, func() {
			delete(c.cache, u.String())
		})
	}

	return result, exists && fresh
}

func (c *crawlerImpl) fetchWithCache(ctx context.Context, u *url.URL) CrawlResponse {
	cachedEntry, ok := c.tryFetchFromCache(u)
	strURL := u.String()

	if ok {
		return cachedEntry
	}

	v, _, err := c.urlSingleFlight.Do(u.String(), func() (CrawlResponse, error) {
		cachedEntry, ok = c.tryFetchFromCache(u)

		if ok {
			return cachedEntry, nil
		}

		res := c.doFetch(ctx, u)

		withLock(c.cacheMx, func() {
			c.cache[strURL] = cacheEntry{result: res, expiredAt: time.Now().Add(cacheTTL)}
		})

		return res, nil
	})

	if err != nil {
		return CrawlResponse{Error: err.Error()}
	}

	return v
}

func (c *crawlerImpl) doFetch(ctx context.Context, u *url.URL) CrawlResponse {
	strURL := u.String()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strURL, nil)

	if err != nil {
		return CrawlResponse{URL: strURL, Error: err.Error()}
	}

	resp, err := c.client.Do(req)

	if err != nil {
		switch {
		case errors.Is(err, context.DeadlineExceeded):
			return CrawlResponse{URL: strURL, Error: fmt.Errorf("timeout exceeded: %w", err).Error()}
		default:
			return CrawlResponse{URL: strURL, Error: err.Error()}
		}
	}

	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)

	return CrawlResponse{URL: strURL, StatusCode: resp.StatusCode}
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)

	err := json.NewEncoder(w).Encode(v)

	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
}

func withLock(mutex sync.Locker, action func()) {
	mutex.Lock()
	defer mutex.Unlock()

	action()
}

func normalizeURL(u *url.URL) {
	scheme := strings.ToLower(u.Scheme)
	u.Scheme = scheme

	host := strings.ToLower(u.Hostname())
	port := u.Port()

	if (scheme == "http" && port == "80") || (scheme == "https" && port == "443") {
		port = ""
	}

	if port != "" {
		u.Host = net.JoinHostPort(host, port)
	} else {
		u.Host = host
	}

	u.Path = url.PathEscape(path.Clean(u.Path))
	u.Fragment = ""

	if u.RawQuery != "" {
		q := u.Query()
		keys := make([]string, 0, len(q))
		for k := range q {
			keys = append(keys, k)
		}
		slices.Sort(keys)

		var b strings.Builder
		first := true
		for _, k := range keys {
			vals := q[k]
			slices.Sort(vals)
			ek := url.QueryEscape(k)
			for _, v := range vals {
				if !first {
					b.WriteByte('&')
				}
				first = false
				b.WriteString(ek)
				b.WriteByte('=')
				b.WriteString(url.QueryEscape(v))
			}
		}
		u.RawQuery = b.String()
	}
}
