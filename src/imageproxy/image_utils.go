package imageproxy

import (
	"media_utils"
	"bufio"
	"bytes"
	"crypto/md5"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
)

const (
	HTTP_HEADERS_BODY_SEP = "\r\n"
)

//
// 文件的MD5
//
func fileMD5(body []byte) string {
	hash := md5.New()
	hash.Write(body)
	hashInBytes := hash.Sum(nil)[:16]
	return hex.EncodeToString(hashInBytes)
}

//
// 根据MD5来获取文件的路径
//
func fileMD5Name(md5 string) string {
	return fmt.Sprintf("improxy/%s/%s/%s", md5[0:2], md5[2:4], md5)
}

//
// 判断客户端是否支持webp格式
//
func HasWebpSupport(r *http.Request) bool {
	accept := r.Header.Get("Accept")
	return strings.Contains(accept, media_utils.ContentTypeWebp)
}

//
// 根据扩展名返回对应的Content-Type
//
func FileContentType(format string) string {

	// 默认的encoding
	contentType := ""

	// 支持: png, jpeg, gif, webp
	switch format {
	case media_utils.ImageFormatJpeg:
		fallthrough
	case media_utils.ImageFormatJpg:
		contentType = media_utils.ContentTypeJPEG
	case media_utils.ImageFormatGif:
		contentType = media_utils.ContentTypeGIF
	case media_utils.ImageFormatPng:
		contentType = media_utils.ContentTypePNG
	case media_utils.ImageFormatWebp:
		contentType = media_utils.ContentTypeWebp
	}

	return contentType
}

//
// 图片的缓存
//
type ImageWithMeta struct {
	Headers []byte // 包含了各种缓存相关的headers, 例如: etag, last-modified, cache-control, expires 等
	Image   []byte
}

//
// Cache中的Image缓存格式：
//   header_length(big_endian_unit16t)
//   header_data
//   image_data
//
func (this *ImageWithMeta) Bytes() []byte {
	buf := new(bytes.Buffer)

	lengthBytes := make([]byte, 2)
	binary.BigEndian.PutUint16(lengthBytes, uint16(len(this.Headers)))
	buf.Write(lengthBytes)
	buf.Write(this.Headers)
	buf.Write(this.Image)
	return buf.Bytes()
}

//
// 将ImageCache解析成为 headers和img
//
func NewImageWithMetaFromCache(data []byte) *ImageWithMeta {
	headLength := binary.BigEndian.Uint16(data[0:2])

	return &ImageWithMeta{
		Headers: data[2:(2 + headLength)],
		Image:   data[(2 + headLength):],
	}
}

//
// 将 http response中和缓存相关的header读取出来
//
func ParseHeadersFromResponse(res *http.Response) []byte {
	buf := new(bytes.Buffer)
	keys := []string{"Last-Modified", "Etag"}
	for _, key := range keys {
		key := http.CanonicalHeaderKey(key)
		if value, ok := res.Header[key]; ok {
			fmt.Fprintf(buf, "%s: %s\n", key, value[0])
		}
	}

	fmt.Fprintf(buf, "Cache-Control: max-age=%d\n", 2592000) // 1个月的有效期
	return buf.Bytes()
}

//
// 输出图片数据
//
func ImageDataToHttpResponse(imageWithMeta *ImageWithMeta, contentType string, req *http.Request) (*http.Response, error) {

	// http.TimeFormat: "Mon, 02 Jan 2006 15:04:05 GMT"
	// time.RFC1123:    "Mon, 02 Jan 2006 15:04:05 MST"
	buf := new(bytes.Buffer)
	fmt.Fprintf(buf, "%s %s OK\n", "HTTP/1.0", "200")
	fmt.Fprintf(buf, "Content-Type: %s\n", contentType)
	fmt.Fprintf(buf, "Date: %s\n", time.Now().Format(http.TimeFormat ))
	fmt.Fprintf(buf, "Expires: %s\n", time.Now().AddDate(0, 1, 0).Format(http.TimeFormat)) // 1个月的有效期
	buf.Write(imageWithMeta.Headers)
	fmt.Fprintf(buf, "Content-Length: %d\n", len(imageWithMeta.Image))
	fmt.Fprintf(buf, "Vary: Accept\n")

	// Http协议头结束
	fmt.Fprintf(buf, HTTP_HEADERS_BODY_SEP)

	// log.Printf("Headers: %s", string(buf.Bytes()))

	buf.Write(imageWithMeta.Image)
	return http.ReadResponse(bufio.NewReader(buf), req)
}

//
// 以JSON格式返回 v 中的数据
//
func JSONDataToHttpResponse(v interface{}, req *http.Request) (*http.Response, error) {

	jsonBuffer := new(bytes.Buffer)
	fmt.Fprintf(jsonBuffer, "%s %s OK\n", "HTTP/1.0", "200")
	fmt.Fprintf(jsonBuffer, "Content-Type:application/json\n")
	fmt.Fprintf(jsonBuffer, "Date:%s\n", time.Now().Format(http.TimeFormat))
	fmt.Fprintf(jsonBuffer, "Cache-Control:no-cache, no-store, must-revalidate\n")

	// Http协议头结束
	fmt.Fprintf(jsonBuffer, HTTP_HEADERS_BODY_SEP)

	resultJson, _ := json.Marshal(v)
	jsonBuffer.Write(resultJson)
	return http.ReadResponse(bufio.NewReader(jsonBuffer), req)
}

//
// 以JSON格式返回 v 中的数据
//
func Http404Response(req *http.Request) (*http.Response, error) {

	buf := new(bytes.Buffer)
	fmt.Fprintf(buf, "%s %s OK\n", "HTTP/1.0", "404")
	fmt.Fprintf(buf, "Date:%s\n", time.Now().Format(http.TimeFormat))
	fmt.Fprintf(buf, "Expires: %s\n", time.Now().Add(time.Hour).Format(http.TimeFormat)) // 1小时的有效期
	fmt.Fprintf(buf, "Cache-Control: max-age=%d\n", 3600)
	return http.ReadResponse(bufio.NewReader(buf), req)
}
