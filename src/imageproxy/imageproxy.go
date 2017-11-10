package imageproxy

import (
	"cache"
	"fmt"
	log "github.com/wfxiang08/cyutils/utils/rolling_log"
	"io"
	"io/ioutil"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
	"media_utils"
	"config"
)

const (
	AWS_S3_PREFIX = "awss3"
)

type HttpProxyResult struct {
	Succeed          bool   `json:"succeed"`
	Message          string `json:"msg"`
	ImageRelativeUrl string `json:"url"`
	DemoUrl          string `json:"demo_url"`
}

// Proxy serves image requests.
type Proxy struct {
	Client         *http.Client // client used to fetch remote URLs
	Cache          cache.Cache  // cache used to cache responses
	Whitelist      []string
	Referrers      []string
	DefaultBaseURL *url.URL
	Timeout        time.Duration
	Wg             *sync.WaitGroup
}

// NewProxy constructs a new proxy.  The provided http RoundTripper will be
// used to fetch remote URLs.  If nil is provided, http.DefaultTransport will
// be used.
func NewProxy(transport http.RoundTripper, cacheInstance cache.Cache, wg *sync.WaitGroup) *Proxy {
	if transport == nil {
		transport = http.DefaultTransport
	}
	if cacheInstance == nil {
		cacheInstance = cache.NopCache
	}

	proxy := Proxy{
		Cache: cacheInstance,
		Wg:    wg,
	}

	client := new(http.Client)

	//
	// 工作流程:
	//     ServeHTTP 对外处理请求
	//         权限控制
	//         p.Client.Get 内部处理
	//         cache.Transport 先做一层缓存处理
	//           缓存没有命中，则TransformingTransport继续处理
	//
	client.Transport = &cache.Transport{
		Transport:           &TransformingTransport{transport, client, cacheInstance},
		Cache:               cacheInstance,
		MarkCachedResponses: true,
	}

	proxy.Client = client

	return &proxy
}

func (p *Proxy) getFavicon(w http.ResponseWriter) error {
	faviconPath := config.GetConfPath("conf/favicon.ico")
	data, err := ioutil.ReadFile(faviconPath)
	if err == nil {
		w.Header().Add("ETag", "a895c786bfaf8cb3f4c24926e3279615")
		w.Header().Add("Date", time.Now().Format(time.RFC1123))
		w.Header().Add("Expires", time.Now().AddDate(1, 1, 0).Format(time.RFC1123)) // 1个月的有效期
		w.Header().Add("Content-Type", "image/x-icon")
		w.Write(data)
	}
	return err
}

// ServeHTTP handles incoming requests.
func (p *Proxy) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == "/favicon.ico" {
		p.getFavicon(w)
		return // ignore favicon requests
	}

	if r.URL.Path == "/health-check" {
		fmt.Fprint(w, "OK")
		return
	}

	p.Wg.Add(1)
	defer p.Wg.Done()

	var h http.Handler = http.HandlerFunc(p.serveImage)
	if p.Timeout > 0 {
		h = TimeoutHandler(h, p.Timeout, "Gateway timeout waiting for remote resource.")
	}
	h.ServeHTTP(w, r)
}

//
// 如何处理来自Client的请求呢?
// 1. 解析Request, 将图片请求的格式也解析出来(options)
// 2. 权限检测: allowed(签名等)
// 3. 通过client来访问后端资源，包括处理cache
//
func (p *Proxy) serveImage(w http.ResponseWriter, r *http.Request) {

	start := Microseconds()

	// 1. 解析Request
	req, err := NewRequest(r, p.DefaultBaseURL)

	// 2. 如果格式不正确，直接报错
	if err != nil {
		http.NotFound(w, r)
		return
	}

	// 4. 访问权限控制
	//    TODO: 访问的目录的控制
	var signOK bool
	if err, signOK = p.allowed(req); err != nil {
		log.Error(err)
		http.Error(w, err.Error(), http.StatusForbidden)
		return
	}

	// 5. 直接通过 Client来处理请求（然后通过中间件来处理各种Cache, Resize等）
	//    重点关注模块
	// 这里的req都是经过标准化处理之后的请求
	log.Printf("--> %s", req.String())
	resp, err := p.Client.Get(req.String())
	if err != nil {
		http.NotFound(w, r)
		return
	}

	writeResponseToWriter(resp, w, r, start, signOK)
}

func writeResponseToWriter(resp *http.Response, w http.ResponseWriter, r *http.Request, start int64, signOK bool) {
	defer resp.Body.Close()

	// 6. 如何处理返回的数据
	copyHeader(w, resp, "Cache-Control")
	copyHeader(w, resp, "Last-Modified")
	copyHeader(w, resp, "Expires")
	copyHeader(w, resp, "Etag")
	copyHeader(w, resp, "Link")

	if is304 := check304(r, resp); is304 {
		w.Header().Add("Vary", "Accept")
		w.WriteHeader(http.StatusNotModified)
		cached := resp.Header.Get(cache.XFromCache)

		log.Printf("Elapsed: %.1fms, Status: %d, cache: %v, URL: %s, sign: %v",
			float64(Microseconds() - start) * 0.001, resp.StatusCode, cached == "1", r.URL.String(), signOK)
		return
	}

	copyHeader(w, resp, "Content-Length")
	copyHeader(w, resp, "Content-Type")

	w.Header().Add("Vary", "Accept")
	// 方便Ajax读取修改图片
	w.Header().Add("Access-Control-Allow-Origin", "*")
	w.WriteHeader(resp.StatusCode)

	// 注意Http请求的格式
	// 这里 serveImage 实际上就是一个Proxy
	io.Copy(w, resp.Body)

	cached := resp.Header.Get(cache.XFromCache)
	log.Printf("Elapsed: %.1fms, Status: %d, cache: %v, URL: %s, sign: %v",
		float64(Microseconds() - start) * 0.001, resp.StatusCode, cached == "1", r.URL.String(), signOK)
}

func copyHeader(w http.ResponseWriter, r *http.Response, header string) {
	key := http.CanonicalHeaderKey(header)
	if value, ok := r.Header[key]; ok {
		w.Header()[key] = value
	}
}

//
// 访问权限控制
//
func (p *Proxy) allowed(r *Request) (error, bool) {

	// 文件格式不合法，则直接报错
	contentType := FileContentType(r.Options.Format)
	if len(contentType) == 0 && len(r.Options.Format) > 0 {
		return fmt.Errorf(fmt.Sprintf("Invalid file format %s", r.Options.Format)), false
	}

	if len(p.Referrers) > 0 && !validReferrer(p.Referrers, r.Original) {
		return fmt.Errorf("request does not contain an allowed referrer: %v", r), false
	}

	// 防止别人的域名直接引用我们的服务
	if len(p.Whitelist) > 0 && !validHost(p.Whitelist, r.URL) {
		return fmt.Errorf("request host not allowed: %v", r), false
	}

	// 如何验证签名呢?
	// 如果指定了: SignatureKey ?
	validSign := validSignature(r)

	// 暂时不验证签名，先试运行
	// log.Printf("URL: %s, Sign OK: %v", r.URL.String(), validSign)
	return nil, validSign

	//if validSign {
	//	return nil
	//}
	//return fmt.Errorf("request does not contain an allowed host or valid signature: %v", r)
}

// validHost returns whether the host in u matches one of hosts.
func validHost(hosts []string, u *url.URL) bool {
	for _, host := range hosts {
		if u.Host == host {
			return true
		}
		if strings.HasPrefix(host, "*.") && strings.HasSuffix(u.Host, host[2:]) {
			return true
		}
	}

	return false
}

// returns whether the referrer from the request is in the host list.
func validReferrer(hosts []string, r *http.Request) bool {
	u, err := url.Parse(r.Header.Get("Referer"))
	if err != nil {
		// malformed or blank header, just deny
		return false
	}

	return validHost(hosts, u)
}

//
// 如何验证签名呢?
//
func validSignature(r *Request) bool {
	// key 暂时ignored, hard coding

	var queries url.Values
	var path string
	if r.Original != nil {
		queries = r.Original.URL.Query()
		path = r.Original.URL.Path // 包含各种缩放参数
	} else {
		queries = r.URL.Query()
		path = r.URL.Path
	}

	ts := queries.Get(media_utils.ParamVersionTs) // 版本
	token := queries.Get(media_utils.ParamToken)
	if len(token) <= 5 {
		return false
	}

	return media_utils.SimpleVerify(path, ts, token, true)
}

// check304 checks whether we should send a 304 Not Modified in response to
// req, based on the response resp.  This is determined using the last modified
// time and the entity tag of resp.
func check304(req *http.Request, resp *http.Response) bool {

	// 验证Etag是否一致
	etag := resp.Header.Get("Etag")
	if etag != "" && etag == req.Header.Get("If-None-Match") {
		return true
	}

	// 验证Last-Modified是否一致
	lastModified, err := time.Parse(time.RFC1123, resp.Header.Get("Last-Modified"))
	if err != nil {
		return false
	}
	ifModSince, err := time.Parse(time.RFC1123, req.Header.Get("If-Modified-Since"))
	if err != nil {
		return false
	}
	if lastModified.Before(ifModSince) {
		return true
	}

	return false
}

//
// 当前时间(单位微秒), * 0.001 得到ms, * 0.000001得到s
//
func Microseconds() int64 {
	return time.Now().UnixNano() / int64(time.Microsecond)
}
