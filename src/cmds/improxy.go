package main

import (
	"cache"
	"cache/diskcache"
	"cache/diskv"
	"flag"
	"fmt"
	log "github.com/wfxiang08/cyutils/utils/rolling_log"
	"imageproxy"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
)

var (
	VERSION    = "HEAD"
	BUILD_DATE string
)

// 设置各种参数的Flag
var (
	addr      = flag.String("addr", "localhost:8080", "TCP address to listen on")
	whitelist = flag.String("whitelist", "", "comma separated list of allowed remote hosts")
	referrers = flag.String("referrers", "", "comma separated list of allowed referring hosts")
	logFile   = flag.String("logfile", "", "logFile path")
	cacheDir  = flag.String("cache", "", "location to cache images")
	timeout   = flag.Duration("timeout", 0, "time limit for requests served by this proxy")
	signurl   = flag.String("signurl", "", "print version information")
	version   = flag.Bool("version", false, "print version information")
)

func main() {
	flag.Parse()

	if *version {
		fmt.Printf("Improxy %v\n 版本: %v\n", VERSION, BUILD_DATE)
		return
	}

	localCache, err := parseCache()
	if err != nil {
		log.ErrorError(err, "Improxy parse cache failed")
		return
	}

	// set output log file
	if len(*logFile) > 0 {
		f, err := log.NewRollingFile(*logFile, 3)
		if err != nil {
			log.PanicErrorf(err, "ImProxy open rolling log file failed: %s", *logFile)
		} else {
			defer f.Close()
			log.StdLog = log.New(f, "")
		}
	}
	log.SetLevel(log.LEVEL_INFO)
	log.SetFlags(log.Flags() | log.Lshortfile)

	awsUrl, _ := url.Parse("http://awss3")
	var wg sync.WaitGroup
	proxy := imageproxy.NewProxy(nil, localCache, &wg)
	proxy.DefaultBaseURL = awsUrl

	if *whitelist != "" {
		proxy.Whitelist = strings.Split(*whitelist, ",")
	}
	if *referrers != "" {
		proxy.Referrers = strings.Split(*referrers, ",")
	}

	proxy.Timeout = *timeout

	// 创建Http Server, 以及Proxy
	server := &http.Server{
		Addr:    *addr,
		Handler: proxy,
	}

	log.Printf(">>>>> Improxy (version %s) listening on %s\n", VERSION, server.Addr)

	sigchan := make(chan os.Signal, 1)
	signal.Notify(sigchan, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		// 对外提供服务
		server.ListenAndServe()
	}()

	sig := <-sigchan

	log.Printf("<<<<< Improxy Caught signal %v: terminating\n", sig)
	// 不在接受新的请求

	server.Close()

	wg.Wait()
	log.Printf("<<<<< Improxy terminated\n")
}



// parseCache parses the cache-related flags and returns the specified Cache implementation.
func parseCache() (cache.Cache, error) {
	// 直接使用磁盘cache
	if len(*cacheDir) > 0 {
		return diskCache(*cacheDir), nil
	} else {
		return nil, nil
	}
}

//
// 设置DiskCache
//
func diskCache(path string) *diskcache.Cache {
	path, _ = filepath.Abs(path)
	log.Printf("Improxy, disk cache: %s", path)

	//
	// 文件名如何获取呢?
	// key --> md5 --> tranform: path --> path / md5
	//
	d := diskv.New(diskv.Options{
		BasePath:     path,
		CacheSizeMax: 1024 * 1024 * 1024, // 默认磁盘缓存: 1G
		Transform: func(s string) []string {
			return []string{s[0:2], s[2:4]}
		},
	})
	return diskcache.NewWithDiskv(d)
}
