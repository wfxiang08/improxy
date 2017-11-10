package cache

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	log "github.com/wfxiang08/cyutils/utils/rolling_log"
	"io"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
	"sync"
	"time"
)

const (
	stale = iota
	fresh
	transparent
	XFromCache = "X-From-Cache"
)

// Request --> CacheKey
func CacheKey(req *http.Request) string {
	cacheKey := req.URL.String()
	return strings.Replace(cacheKey, "#", "_", -1)
}

// URL --> CacheKey
func CacheKeyForURL(req *url.URL) string {
	cacheKey := req.String()
	return strings.Replace(cacheKey, "#", "_", -1)
}

//
// 除了HttpCache内部，我们的所有的请求都只Cache图片的body, 而不处理外部的ContentType等信息
//
func DataCacheKey(req *http.Request) string {
	cacheKey := req.URL.String()
	return fmt.Sprintf("v2:%s", strings.Replace(cacheKey, "#", "_", -1))
}

//
// 除了HttpCache内部，我们的所有的请求都只Cache图片的body, 而不处理外部的ContentType等信息
//
func DataCacheKeyForURL(req *url.URL) string {
	cacheKey := req.String()
	return fmt.Sprintf("v2:%s", strings.Replace(cacheKey, "#", "_", -1))
}

//
// 网络层的缓存: 缓存的是整个http response数据
// 和imageproxy层的缓存不同，imageproxy层的缓存缓存的是: http headers(cache related) + image data
//
func CachedResponse(c Cache, req *http.Request) (resp *http.Response, err error) {

	// req, err := NewRequest(r, p.DefaultBaseURL)
	cachedVal, ok := c.Get(CacheKey(req))
	if !ok {
		return
	}

	b := bytes.NewBuffer(cachedVal)
	return http.ReadResponse(bufio.NewReader(b), req)
}

// MemoryCache is an implemtation of Cache that stores responses in an in-memory map.
type MemoryCache struct {
	mu    sync.RWMutex
	items map[string][]byte
}

// Get returns the []byte representation of the response and true if present, false if not
func (c *MemoryCache) Get(key string) (resp []byte, ok bool) {
	c.mu.RLock()
	resp, ok = c.items[key]
	c.mu.RUnlock()
	return resp, ok
}

// Set saves response resp to the cache with key
func (c *MemoryCache) Exists(key string) bool {
	c.mu.RLock()
	_, ok := c.items[key]
	c.mu.RUnlock()
	return ok
}

// Set saves response resp to the cache with key
func (c *MemoryCache) Set(key string, resp []byte) {
	c.mu.Lock()
	c.items[key] = resp
	c.mu.Unlock()
}

// Delete removes key from the cache
func (c *MemoryCache) Delete(key string) {
	c.mu.Lock()
	delete(c.items, key)
	c.mu.Unlock()
}

// NewMemoryCache returns a new Cache that will store items in an in-memory map
func NewMemoryCache() *MemoryCache {
	c := &MemoryCache{items: map[string][]byte{}}
	return c
}

// onEOFReader executes a function on reader EOF or close
type onEOFReader struct {
	rc io.ReadCloser
	fn func()
}

func (r *onEOFReader) Read(p []byte) (n int, err error) {
	n, err = r.rc.Read(p)
	if err == io.EOF {
		r.runFunc()
	}
	return
}

func (r *onEOFReader) Close() error {
	err := r.rc.Close()
	r.runFunc()
	return err
}

func (r *onEOFReader) runFunc() {
	if fn := r.fn; fn != nil {
		fn()
		r.fn = nil
	}
}

//
// 和默认的 Transport相比，增加了很多http cache相关的逻辑
// Transport底层的传输层
//
type Transport struct {
	// The RoundTripper interface actually used to make requests
	// If nil, http.DefaultTransport is used
	Transport http.RoundTripper
	Cache     Cache
	// If true, responses returned from the cache will be given an extra header, X-From-Cache
	MarkCachedResponses bool

	// Mapping of original request => cloned
	mu     sync.RWMutex
	modReq map[*http.Request]*http.Request
}

// NewTransport returns a new Transport with the
// provided Cache implementation and MarkCachedResponses set to true
func NewTransport(c Cache) *Transport {
	return &Transport{Cache: c, MarkCachedResponses: true}
}

// Client returns an *http.Client that caches responses.
func (t *Transport) Client() *http.Client {
	return &http.Client{Transport: t}
}

// varyMatches will return false unless all of the cached values for the headers listed in Vary
// match the new request
func varyMatches(cachedResp *http.Response, req *http.Request) bool {
	// 1. 获取所有的vary指定的header names
	//    这些字段的不一样，可以认为需要返回不同的结果
	for _, header := range headerAllCommaSepValues(cachedResp.Header, "vary") {
		header = http.CanonicalHeaderKey(header)

		if header != "" && req.Header.Get(header) != cachedResp.Header.Get("X-Varied-"+header) {
			log.Printf("Vary not match: %s vs. %s", req.Header.Get(header), cachedResp.Header.Get("X-Varied-"+header))
			return false
		}
	}
	return true
}

// setModReq maintains a mapping between original requests and their associated cloned requests
func (t *Transport) setModReq(orig, mod *http.Request) {
	t.mu.Lock()
	if t.modReq == nil {
		t.modReq = make(map[*http.Request]*http.Request)
	}
	if mod == nil {
		delete(t.modReq, orig)
	} else {
		t.modReq[orig] = mod
	}
	t.mu.Unlock()
}

func (t *Transport) RoundTrip(req *http.Request) (resp *http.Response, err error) {
	// 如何处理实际的请求呢?
	cacheKey := CacheKey(req)

	// GET/HEAD&&非Range请求可以Cache
	cacheable := (req.Method == "GET" || req.Method == "HEAD") && req.Header.Get("range") == ""

	// 1. 首先请求Cache
	var cachedResp *http.Response
	if cacheable {
		cachedResp, err = CachedResponse(t.Cache, req)
	} else {
		// Need to invalidate an existing value
		t.Cache.Delete(cacheKey)
	}

	transport := t.Transport
	if transport == nil {
		transport = http.DefaultTransport
	}

	// 2.1 如果可以缓存，且cachedResp正常解析
	if cacheable && cachedResp != nil && err == nil {
		// 标记数据从Cache中返回
		if t.MarkCachedResponses {
			// log.Printf("Mark Cache 1")
			cachedResp.Header.Set(XFromCache, "1")
		}

		// 验证: vary是否一致
		if varyMatches(cachedResp, req) {
			// Can only use cached value if the new request doesn't Vary significantly
			freshness := getFreshness(cachedResp.Header, req.Header)

			// 如果Response有效，则直接返回
			if freshness == fresh {
				// log.Printf("Fresh Cache Response")
				return cachedResp, nil
			}

			// 需要验证
			if freshness == stale {
				// 构建一个新的Request(支持etag, last-modified)
				var req2 *http.Request
				etag := cachedResp.Header.Get("etag")
				if etag != "" && req.Header.Get("etag") == "" {
					req2 = cloneRequest(req)
					req2.Header.Set("if-none-match", etag)
				}
				lastModified := cachedResp.Header.Get("last-modified")
				if lastModified != "" && req.Header.Get("last-modified") == "" {
					if req2 == nil {
						req2 = cloneRequest(req)
					}
					req2.Header.Set("if-modified-since", lastModified)
				}

				if req2 != nil {
					// Associate original request with cloned request so we can refer to
					// it in CancelRequest(). Release the mapping when it's no longer needed.
					t.setModReq(req, req2)
					defer func(originalReq *http.Request) {
						// Release req/clone mapping on error
						if err != nil {
							t.setModReq(originalReq, nil)
						}
						if resp != nil {
							// Release req/clone mapping on body close/EOF
							resp.Body = &onEOFReader{
								rc: resp.Body,
								fn: func() {
									t.setModReq(originalReq, nil)
								},
							}
						}
					}(req)
					req = req2
				}
			}
		}

		// 如果缓存等条件不满足，则直接发起一次请求
		// log.Printf("Transport To backend")
		resp, err = transport.RoundTrip(req)

		if err == nil && req.Method == "GET" && resp.StatusCode == http.StatusNotModified {
			// Replace the 304 response with the one from cache, but update with some new headers
			endToEndHeaders := getEndToEndHeaders(resp.Header)
			for _, header := range endToEndHeaders {
				cachedResp.Header[header] = resp.Header[header]
			}
			// 直接返回200
			cachedResp.Status = fmt.Sprintf("%d %s", http.StatusOK, http.StatusText(http.StatusOK))
			cachedResp.StatusCode = http.StatusOK

			// log.Printf("Transport 304")
			resp = cachedResp
		} else if (err != nil || (cachedResp != nil && resp.StatusCode >= 500)) &&
			req.Method == "GET" && canStaleOnError(cachedResp.Header, req.Header) {
			// In case of transport failure and stale-if-error activated, returns cached content
			// when available
			// 错误情况下，直接返回
			// log.Printf("Transport Fail-safe")
			cachedResp.Status = fmt.Sprintf("%d %s", http.StatusOK, http.StatusText(http.StatusOK))
			cachedResp.StatusCode = http.StatusOK
			return cachedResp, nil

		} else {
			log.Printf("Transport Update Cache: %s", cacheKey)
			if err != nil || resp.StatusCode != http.StatusOK {
				t.Cache.Delete(cacheKey)
			}
			if err != nil {
				return nil, err
			}
		}
	} else {
		// 2.2 没有读取到缓存
		reqCacheControl := parseCacheControl(req.Header)

		// 3.1 这个请求除了测试之外，似乎没有什么重要意义
		if _, ok := reqCacheControl["only-if-cached"]; ok {
			resp = newGatewayTimeoutResponse(req)
		} else {
			// 3.2 正常的请求
			// 交给transport去处理
			log.Printf("Transport Direct: %s", cacheKey)
			resp, err = transport.RoundTrip(req)
			if err != nil {
				return nil, err
			}
		}
	}

	// 做数据缓存
	if cacheable && canStore(parseCacheControl(req.Header), parseCacheControl(resp.Header)) {
		for _, varyKey := range headerAllCommaSepValues(resp.Header, "vary") {
			varyKey = http.CanonicalHeaderKey(varyKey)

			// 缓存数据
			fakeHeader := "X-Varied-" + varyKey
			reqValue := req.Header.Get(varyKey)
			if reqValue != "" {
				resp.Header.Set(fakeHeader, reqValue)
			}
		}

		// 如何序列化数据？
		respBytes, err := httputil.DumpResponse(resp, true)
		if err == nil {
			t.Cache.Set(cacheKey, respBytes)
		}
	} else {
		t.Cache.Delete(cacheKey)
	}
	return resp, nil
}

// CancelRequest calls CancelRequest on the underlaying transport if implemented or
// throw a warning otherwise.
func (t *Transport) CancelRequest(req *http.Request) {
	type canceler interface {
		CancelRequest(*http.Request)
	}
	tr, ok := t.Transport.(canceler)
	if !ok {
		log.Printf("httpcache: Client Transport of type %T doesn't support CancelRequest; Timeout not supported", t.Transport)
		return
	}

	t.mu.RLock()
	if modReq, ok := t.modReq[req]; ok {
		t.mu.RUnlock()
		t.mu.Lock()
		delete(t.modReq, req)
		t.mu.Unlock()
		tr.CancelRequest(modReq)
	} else {
		t.mu.RUnlock()
		tr.CancelRequest(req)
	}
}

// ErrNoDateHeader indicates that the HTTP headers contained no Date header.
var ErrNoDateHeader = errors.New("no Date header")

// Date parses and returns the value of the Date header.
func Date(respHeaders http.Header) (date time.Time, err error) {
	dateHeader := respHeaders.Get("date")
	if dateHeader == "" {
		err = ErrNoDateHeader
		return
	}

	return time.Parse(time.RFC1123, dateHeader)
}

type realClock struct{}

func (c *realClock) since(d time.Time) time.Duration {
	return time.Since(d)
}

type timer interface {
	since(d time.Time) time.Duration
}

var clock timer = &realClock{}

// getFreshness will return one of fresh/stale/transparent based on the cache-control
// values of the request and the response
//
// fresh indicates the response can be returned
// stale indicates that the response needs validating before it is returned
// transparent indicates the response should not be used to fulfil the request
//
// Because this is only a private cache, 'public' and 'private' in cache-control aren't
// signficant. Similarly, smax-age isn't used.
func getFreshness(respHeaders, reqHeaders http.Header) (freshness int) {
	// 如何判断是否新鲜呢?
	respCacheControl := parseCacheControl(respHeaders)
	reqCacheControl := parseCacheControl(reqHeaders)

	if _, ok := reqCacheControl["no-cache"]; ok {
		return transparent
	}
	if _, ok := respCacheControl["no-cache"]; ok {
		return stale
	}
	if _, ok := reqCacheControl["only-if-cached"]; ok {
		return fresh
	}

	// 如果返回数据没有date, 这认为数据需要验证
	date, err := Date(respHeaders)
	if err != nil {
		return stale
	}
	currentAge := clock.since(date)

	var lifetime time.Duration
	var zeroDuration time.Duration // 默认长度就为0

	// If a response includes both an Expires header and a max-age directive,
	// the max-age directive overrides the Expires header, even if the Expires header is more restrictive.
	if maxAge, ok := respCacheControl["max-age"]; ok {
		lifetime, err = time.ParseDuration(maxAge + "s")
		if err != nil {
			lifetime = zeroDuration
		}
	} else {
		expiresHeader := respHeaders.Get("Expires")
		if expiresHeader != "" {
			expires, err := time.Parse(time.RFC1123, expiresHeader)
			if err != nil {
				lifetime = zeroDuration
			} else {
				lifetime = expires.Sub(date)
			}
		}
	}

	// 这个不用考虑
	if maxAge, ok := reqCacheControl["max-age"]; ok {
		// the client is willing to accept a response whose age is no greater than the specified time in seconds
		lifetime, err = time.ParseDuration(maxAge + "s")
		if err != nil {
			lifetime = zeroDuration
		}
	}
	if minfresh, ok := reqCacheControl["min-fresh"]; ok {
		//  the client wants a response that will still be fresh for at least the specified number of seconds.
		minfreshDuration, err := time.ParseDuration(minfresh + "s")
		if err == nil {
			currentAge = time.Duration(currentAge + minfreshDuration)
		}
	}

	if maxstale, ok := reqCacheControl["max-stale"]; ok {
		// Indicates that the client is willing to accept a response that has exceeded its expiration time.
		// If max-stale is assigned a value, then the client is willing to accept a response that has exceeded
		// its expiration time by no more than the specified number of seconds.
		// If no value is assigned to max-stale, then the client is willing to accept a stale response of any age.
		//
		// Responses served only because of a max-stale value are supposed to have a Warning header added to them,
		// but that seems like a  hassle, and is it actually useful? If so, then there needs to be a different
		// return-value available here.
		if maxstale == "" {
			//log.Printf("fresh xxx ")
			return fresh
		}
		maxstaleDuration, err := time.ParseDuration(maxstale + "s")
		if err == nil {
			currentAge = time.Duration(currentAge - maxstaleDuration)
		}
	}

	if lifetime > currentAge {
		//log.Printf("fresh xxx ")
		return fresh
	}

	//log.Printf("stale end ")
	return stale
}

// Returns true if either the request or the response includes the stale-if-error
// cache control extension: https://tools.ietf.org/html/rfc5861
func canStaleOnError(respHeaders, reqHeaders http.Header) bool {
	respCacheControl := parseCacheControl(respHeaders)
	reqCacheControl := parseCacheControl(reqHeaders)

	var err error
	lifetime := time.Duration(-1)

	if staleMaxAge, ok := respCacheControl["stale-if-error"]; ok {
		if staleMaxAge != "" {
			lifetime, err = time.ParseDuration(staleMaxAge + "s")
			if err != nil {
				return false
			}
		} else {
			return true
		}
	}
	if staleMaxAge, ok := reqCacheControl["stale-if-error"]; ok {
		if staleMaxAge != "" {
			lifetime, err = time.ParseDuration(staleMaxAge + "s")
			if err != nil {
				return false
			}
		} else {
			return true
		}
	}

	if lifetime >= 0 {
		date, err := Date(respHeaders)
		if err != nil {
			return false
		}
		currentAge := clock.since(date)
		if lifetime > currentAge {
			return true
		}
	}

	return false
}

func getEndToEndHeaders(respHeaders http.Header) []string {
	// These headers are always hop-by-hop
	hopByHopHeaders := map[string]struct{}{
		"Connection":          struct{}{},
		"Keep-Alive":          struct{}{},
		"Proxy-Authenticate":  struct{}{},
		"Proxy-Authorization": struct{}{},
		"Te":                struct{}{},
		"Trailers":          struct{}{},
		"Transfer-Encoding": struct{}{},
		"Upgrade":           struct{}{},
	}

	for _, extra := range strings.Split(respHeaders.Get("connection"), ",") {
		// any header listed in connection, if present, is also considered hop-by-hop
		if strings.Trim(extra, " ") != "" {
			hopByHopHeaders[http.CanonicalHeaderKey(extra)] = struct{}{}
		}
	}
	endToEndHeaders := []string{}
	for respHeader, _ := range respHeaders {
		if _, ok := hopByHopHeaders[respHeader]; !ok {
			endToEndHeaders = append(endToEndHeaders, respHeader)
		}
	}
	return endToEndHeaders
}

//
// Request & Response都不包含 no-store, 则可以缓存
//
func canStore(reqCacheControl, respCacheControl cacheControl) (canStore bool) {
	if _, ok := respCacheControl["no-store"]; ok {
		return false
	}
	if _, ok := reqCacheControl["no-store"]; ok {
		return false
	}
	return true
}

//
// 返回504
//
func newGatewayTimeoutResponse(req *http.Request) *http.Response {
	var braw bytes.Buffer
	braw.WriteString("HTTP/1.1 504 Gateway Timeout\r\n\r\n")
	resp, err := http.ReadResponse(bufio.NewReader(&braw), req)
	if err != nil {
		panic(err)
	}
	return resp
}

// cloneRequest returns a clone of the provided *http.Request.
// The clone is a shallow copy of the struct and its Header map.
// (This function copyright goauth2 authors: https://code.google.com/p/goauth2)
func cloneRequest(r *http.Request) *http.Request {
	// shallow copy of the struct
	r2 := new(http.Request)
	*r2 = *r
	// deep copy of the Header
	r2.Header = make(http.Header)
	for k, s := range r.Header {
		r2.Header[k] = s
	}
	return r2
}

type cacheControl map[string]string

func parseCacheControl(headers http.Header) cacheControl {
	cc := cacheControl{}
	ccHeader := headers.Get("Cache-Control")
	for _, part := range strings.Split(ccHeader, ",") {
		part = strings.Trim(part, " ")
		if part == "" {
			continue
		}
		if strings.ContainsRune(part, '=') {
			keyval := strings.Split(part, "=")
			cc[strings.Trim(keyval[0], " ")] = strings.Trim(keyval[1], ",")
		} else {
			cc[part] = ""
		}
	}
	return cc
}

// headerAllCommaSepValues returns all comma-separated values (each
// with whitespace trimmed) for header name in headers. According to
// Section 4.2 of the HTTP/1.1 spec
// (http://www.w3.org/Protocols/rfc2616/rfc2616-sec4.html#sec4.2),
// values from multiple occurrences of a header should be concatenated, if
// the header's value is a comma-separated list.
// 同一个header名可以出现多次
func headerAllCommaSepValues(headers http.Header, name string) []string {
	var vals []string
	for _, val := range headers[http.CanonicalHeaderKey(name)] {
		// 同一个val可以包含多种信息
		fields := strings.Split(val, ",")
		for i, f := range fields {
			fields[i] = strings.TrimSpace(f)
		}
		vals = append(vals, fields...)
	}
	return vals
}

// NewMemoryCacheTransport returns a new Transport using the in-memory cache implementation
func NewMemoryCacheTransport() *Transport {
	c := NewMemoryCache()
	t := NewTransport(c)
	return t
}
