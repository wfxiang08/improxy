package imageproxy

import (
	"media_utils"
	"cache"
	"github.com/aws/aws-sdk-go/aws/awserr"
	"github.com/aws/aws-sdk-go/aws/session"
	log "github.com/wfxiang08/cyutils/utils/rolling_log"
	"io/ioutil"
	"net/http"
	"config"
)

//
// 一个具体的 http.RoundTripper 的实现，可以通过Request的Fragment来控制图片的缩放等逻辑
//
type TransformingTransport struct {
	// Transport is the underlying http.RoundTripper used to satisfy
	// non-transform requests (those that do not include a URL fragment).
	Transport http.RoundTripper

	// CachingClient is used to fetch images to be resized.  This client is
	// used rather than Transport directly in order to ensure that
	// responses are properly cached.
	CacheClient *http.Client
	Cache       cache.Cache
}

func (t *TransformingTransport) S3ResourceProcess(req *http.Request) (*http.Response, error) {

	start := Microseconds()

	// DataCache只保留原始数据, 各种resize, format处理之后的数据会在外层被直接cache; 不会到达当前函数

	// 1. 下载原始的图片
	originImageUrl := *req.URL
	originImageUrl.Fragment = ""
	var cacheData *ImageWithMeta
	originDataCacheKey := cache.DataCacheKeyForURL(&originImageUrl)

	// 2. 如果存在原始版本，则在本地Cache中存在原始版本
	// log.Printf("OriginCacheKey: %s", originCacheKey)
	if data, ok := t.Cache.Get(originDataCacheKey); ok && len(data) > 0 {
		cacheData = NewImageWithMetaFromCache(data)
		log.Printf("Elapsed %.1fms, S3 Hit cache origin, Key: %s", float64(Microseconds()-start)*0.001, originDataCacheKey)
	}

	// 3. 从S3下载原始版本
	if cacheData == nil {
		// 下载数据
		var s3session *session.Session
		s3session = media_utils.GetS3Session()
		s3Key := req.URL.Path[1:]

		img, headers, err := media_utils.GetContentFromAWSWithMeta(s3session, config.AWSBuckets, s3Key)

		if aerr, ok := err.(awserr.Error); ok {
			switch aerr.Code() {
			case "NoSuchBucket":
				fallthrough
			case "NoSuchKey":
				// 找不到数据，直接返回404
				return Http404Response(req)
			}
		}

		// 未知错误
		if err != nil {
			log.ErrorErrorf(err, "Failed to get object： %s", s3Key)
			return nil, err
		}

		cacheData = &ImageWithMeta{Headers: headers, Image: img}

		// 保存原始版本的数据
		// 只在不直接请求原始版本时调用，因为在transform中会有另外的持久化
		t.Cache.Set(originDataCacheKey, cacheData.Bytes())
	}

	// 4. 然后再做Resize
	return t.transform(req, cacheData, false)
}

//
// http.RoundTripper 接口，在里面可以处理图片的缩放缓存等逻辑
//
func (t *TransformingTransport) RoundTrip(req *http.Request) (*http.Response, error) {

	// http://localhost/options/http://media_utils/key
	// 如果是S3的数据，则单独处理
	if req.URL.Host == AWS_S3_PREFIX {
		return t.S3ResourceProcess(req)
	}

	start := Microseconds()

	var bytes []byte
	var err error
	// 读取外网的原始文件
	var response *http.Response

	if req.URL.Fragment == "" {
		// 如果没有Fragment, 那就直接返回
		response, err = t.Transport.RoundTrip(req)

		log.Printf("Elapsed: %.1fms, Crawl: %s, Fragment: %s", float64(Microseconds()-start)*0.001,
			req.URL.String(), req.URL.Fragment)
		return response, err
	} else {
		u := *req.URL
		u.Fragment = ""
		// 这个会再次触发一次完整的请求
		response, err = t.CacheClient.Get(u.String())
		log.Printf("Elapsed: %.1fms, Crawl: %s, from cache client", float64(Microseconds()-start)*0.001, u.String())

	}

	if err != nil {
		log.ErrorError(err, "Crawl Image failed")
		return nil, err
	}

	defer response.Body.Close()

	headers := ParseHeadersFromResponse(response)
	// 注意这里的bytes就是文件的内容
	bytes, err = ioutil.ReadAll(response.Body)
	if err != nil {
		log.ErrorError(err, "Crawl Image IO failed")
		return nil, err
	}

	imageCache := &ImageWithMeta{Headers: headers, Image: bytes}
	response, err = t.transform(req, imageCache, true)

	log.Printf("Elapsed: %.1fms, Crawl: %s, transform complete", float64(Microseconds()-start)*0.001,
		req.URL.String())

	return response, err

}

func (t *TransformingTransport) transform(req *http.Request, imageCache *ImageWithMeta, upload2S3 bool) (*http.Response, error) {

	start := Microseconds()
	opt := ParseOptions(req.URL.Fragment, false)

	// imageCache vs. transformedImage
	// imageCache 表示从网络或者本地Cache中读取到的数据
	// transformedImage 表示被transform之后的图片数据, 最终返回给Client
	//
	transformedImage := &ImageWithMeta{
		Headers: imageCache.Headers,
		Image:   imageCache.Image,
	}

	// Crawl模式下，一定需要Transform
	needTransform := req.URL.Fragment != ""

	// 图片的缩放
	var transImage []byte
	var format string
	var err error
	if needTransform {
		transImage, format, err = Transform(imageCache.Image, opt)

		transformedImage.Image = transImage
		log.Printf("Elapsed: %.1fms, transform to %s", float64(Microseconds()-start)*0.001, opt.String())

		if err != nil {
			log.ErrorError(err, "Crawl Image Transform failed")
			return nil, err
		}
	} else {
		transImage, format, err = DetectFormat(imageCache.Image, opt)
		log.Printf("Elapsed: %.1fms, detect format", float64(Microseconds()-start)*0.001)
		if transImage != nil {
			transformedImage.Image = transImage
		}

		// 未知错误
		if err != nil {
			log.Errorf("Image Proxy DetectFormat error: %v", err)
			return nil, err
		}
	}

	contentType := FileContentType(format)

	// 不Cache非原始数据，这个由外部的httpcache层来缓存
	return ImageDataToHttpResponse(transformedImage, contentType, req)
}
