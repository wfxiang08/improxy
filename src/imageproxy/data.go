// Copyright 2013 Google Inc. All rights reserved.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package imageproxy

import (
	"bytes"
	"fmt"
	"github.com/wfxiang08/cyutils/utils/errors"
	"media_utils"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
)

const (
	optFit = "fit"
	optFlipVertical = "fv"
	optFlipHorizontal = "fh"
	optRotatePrefix = "r"
	optQualityPrefix = "q"
	optFormatPrefix = "f"
	optSizeDelimiter = "x"
	optSizeDelimiter2 = "*"
	kCloudFrontPattern = "tools/im/"
)

// URL错误
type URLError struct {
	Message string
	URL     *url.URL
}

func (e URLError) Error() string {
	return fmt.Sprintf("malformed URL %q: %s", e.URL, e.Message)
}

//
// http://localhost/{options}/url
// 用于指定对url中的部分的缩放等操作
//
type Options struct {
	                      // See ParseOptions for interpretation of Width and Height values
	Width          float64
	Height         float64

	                      // If true, resize the image to fit in the specified dimensions.  Image
	                      // will not be cropped, and aspect ratio will be maintained.
	Fit            bool

	                      // Rotate image the specified degrees counter-clockwise.  Valid values
	                      // are 90, 180, 270.
	Rotate         int
	FlipVertical   bool
	FlipHorizontal bool
	Quality        int    // Quality of output image
	Format         string // 强制定制格式
}

func (o Options) String() string {
	buf := new(bytes.Buffer)
	fmt.Fprintf(buf, "%v%s%v", o.Width, optSizeDelimiter, o.Height)
	if o.Fit {
		fmt.Fprintf(buf, ",%s", optFit)
	}
	if o.Rotate != 0 {
		fmt.Fprintf(buf, ",%s%d", string(optRotatePrefix), o.Rotate)
	}
	if o.FlipVertical {
		fmt.Fprintf(buf, ",%s", optFlipVertical)
	}
	if o.FlipHorizontal {
		fmt.Fprintf(buf, ",%s", optFlipHorizontal)
	}
	if o.Quality != 0 {
		fmt.Fprintf(buf, ",%s%d", string(optQualityPrefix), o.Quality)
	}

	if len(o.Format) > 0 {
		fmt.Fprintf(buf, ",%s%s", optFormatPrefix, o.Format)
	}
	result := buf.String()
	if result == "0x0" {
		return ""
	} else {
		return result
	}
}

// transform returns whether o includes transformation options.  Some fields
// are not transform related at all (like Signature), and others only apply in
// the presence of other fields (like Fit and Quality).
func (o Options) transform() bool {
	return o.Width != 0 || o.Height != 0 || o.Rotate != 0 || o.FlipHorizontal || o.FlipVertical
}

// ParseOptions parses str as a list of comma separated transformation options.
// The following options can be specified in any order:
//
// Size and Cropping
//
// The size option takes the general form "{width}x{height}", where width and
// height are numbers. Integer values greater than 1 are interpreted as exact
// pixel values. Floats between 0 and 1 are interpreted as percentages of the
// original image size. If either value is omitted or set to 0, it will be
// automatically set to preserve the aspect ratio based on the other dimension.
// If a single number is provided (with no "x" separator), it will be used for
// both height and width.
//
// Depending on the size options specified, an image may be cropped to fit the
// requested size. In all cases, the original aspect ratio of the image will be
// preserved; imageproxy will never stretch the original image.
//
// When no explicit crop mode is specified, the following rules are followed:
//
// - If both width and height values are specified, the image will be scaled to
// fill the space, cropping if necessary to fit the exact dimension.
//
// - If only one of the width or height values is specified, the image will be
// resized to fit the specified dimension, scaling the other dimension as
// needed to maintain the aspect ratio.
//
// If the "fit" option is specified together with a width and height value, the
// image will be resized to fit within a containing box of the specified size.
// As always, the original aspect ratio will be preserved. Specifying the "fit"
// option with only one of either width or height does the same thing as if
// "fit" had not been specified.
//
// Rotation and Flips
//
// The "r{degrees}" option will rotate the image the specified number of
// degrees, counter-clockwise. Valid degrees values are 90, 180, and 270.
//
// The "fv" option will flip the image vertically. The "fh" option will flip
// the image horizontally. Images are flipped after being rotated.
//
// Quality
//
// The "q{qualityPercentage}" option can be used to specify the quality of the
// output file (JPEG only)
//
// Examples
//
// 	0x0       - no resizing
// 	200x      - 200 pixels wide, proportional height
// 	0.15x     - 15% original width, proportional height
// 	x100      - 100 pixels tall, proportional width
// 	100x150   - 100 by 150 pixels, cropping as needed
// 	100       - 100 pixels square, cropping as needed
// 	150,fit   - scale to fit 150 pixels square, no cropping
// 	100,r90   - 100 pixels square, rotated 90 degrees
// 	100,fv,fh - 100 pixels square, flipped horizontal and vertical
// 	200x,q80  - 200 pixels wide, proportional height, 80% quality
func ParseOptions(str string, useWebp bool) Options {
	var options Options

	for _, opt := range strings.Split(str, ",") {
		switch {
		case len(opt) == 0:
			break
		case opt == optFit:
			options.Fit = true
		case opt == optFlipVertical:
			options.FlipVertical = true
		case opt == optFlipHorizontal:
			options.FlipHorizontal = true

		case strings.HasPrefix(opt, optRotatePrefix):
			value := strings.TrimPrefix(opt, optRotatePrefix)
			options.Rotate, _ = strconv.Atoi(value)
		case strings.HasPrefix(opt, optQualityPrefix):
			value := strings.TrimPrefix(opt, optQualityPrefix)
			options.Quality, _ = strconv.Atoi(value)

		case strings.HasPrefix(opt, optFormatPrefix):
			options.Format = strings.TrimPrefix(opt, optFormatPrefix)
		case strings.Contains(opt, optSizeDelimiter):
			size := strings.SplitN(opt, optSizeDelimiter, 2)
			if w := size[0]; w != "" {
				options.Width, _ = strconv.ParseFloat(w, 64)
			}
			if h := size[1]; h != "" {
				options.Height, _ = strconv.ParseFloat(h, 64)
			}
		case strings.Contains(opt, optSizeDelimiter2):
			size := strings.SplitN(opt, optSizeDelimiter2, 2)
			if w := size[0]; w != "" {
				options.Width, _ = strconv.ParseFloat(w, 64)
			}
			if h := size[1]; h != "" {
				options.Height, _ = strconv.ParseFloat(h, 64)
			}
		default:
			if size, err := strconv.ParseFloat(opt, 64); err == nil {
				options.Width = size
				options.Height = size
			}
		}
	}

	// 如果支持webp, 并且没有强制指定格式
	// 如果强制指定格式，则以强制指定为准
	if useWebp && len(options.Format) == 0 {
		options.Format = media_utils.ImageFormatWebp
	}

	return options
}

// Request is an imageproxy request which includes a remote URL of an image to
// proxy, and an optional set of transformations to perform.
type Request struct {
	URL      *url.URL      // URL of the image to proxy
	Options  Options       // Image transformation to perform
	Original *http.Request // The original HTTP request
}

// String returns the request URL as a string, with r.Options encoded in the
// URL fragment.
func (r Request) String() string {
	u := *r.URL

	// 在这里: Fragment被复用起来了
	u.Fragment = r.Options.String()
	return u.String()
}

func NewRequest(r *http.Request, baseURL *url.URL) (*Request, error) {

	var err error
	req := &Request{Original: r}

	path := r.URL.Path[1:]

	//
	// tools/im/{options}/image_url 其中: tools/im/ 是和cloudfront的回源策略对接时约定的pattern
	// 如果 image_url是相对url, 则options必须非空
	//
	if strings.HasPrefix(path, kCloudFrontPattern) {
		path = strings.TrimPrefix(path, kCloudFrontPattern)
	} else {
		// 不存在的文件
		return nil, errors.New("Invalid url")
	}

	// path 格式可能为:
	// /tools/im/150/production/improxy/6a/82e2c962fb727886aa6d7cce7107d7.jpeg
	// /tools/im/150/production/improxy/6a/82e2c962fb727886aa6d7cce7107d7.jpeg/ts10000

	// host之后要么就是没有Options的URL; 要么带有Options

	forceTs := ""
	lastIdx := strings.LastIndex(path, "/")
	if lastIdx != -1 {
		lastComponent := path[lastIdx + 1:]
		reg, _ := regexp.Compile("^ts\\d+$")
		if reg.MatchString(lastComponent) {
			// 提取出 TS, 以及Normalize之后的Path
			forceTs = lastComponent[2:]
			path = path[:lastIdx]
		}
	}

	req.URL, err = parseURL(path)

	useWebp := HasWebpSupport(r)

	if err != nil || !req.URL.IsAbs() {
		// first segment should be options
		parts := strings.SplitN(path, "/", 2)
		if len(parts) != 2 {
			return nil, URLError{"too few path segments", r.URL}
		}

		var err error
		req.URL, err = parseURL(parts[1])
		if err != nil {
			return nil, URLError{fmt.Sprintf("unable to parse remote URL: %v", err), r.URL}
		}

		req.Options = ParseOptions(parts[0], useWebp)
	} else {
		// 如果支持webp, 则特殊考虑
		if useWebp {
			req.Options.Format = media_utils.ImageFormatWebp
		}
	}

	// 使用相对的URL
	if baseURL != nil {
		req.URL = baseURL.ResolveReference(req.URL)
	}

	if !req.URL.IsAbs() {
		return nil, URLError{"must provide absolute remote URL", r.URL}
	}

	// 解析 Schema
	if req.URL.Scheme != "http" && req.URL.Scheme != "https" {
		return nil, URLError{"remote URL must have http or https scheme", r.URL}
	}

	// 这个Query是否有必要再传递呢？
	// ts表示服务器的数据可能更新，需要请求新的版本
	// 其他的只是认证
	values := r.URL.Query()
	ts := values.Get(media_utils.ParamVersionTs)
	if len(forceTs) > 0 {
		req.URL.RawQuery = "ts=" + url.QueryEscape(forceTs)
	} else if len(ts) > 0 {
		req.URL.RawQuery = "ts=" + url.QueryEscape(ts)
	} else {
		req.URL.RawQuery = ""
	}
	return req, nil
}

var reCleanedURL = regexp.MustCompile(`^(https?):/+([^/])`)

// 部分浏览器会将多个"/"缩减为一个"/", 这里需要还原
func parseURL(s string) (*url.URL, error) {
	// 将https:/wwww ---> https://www
	s = reCleanedURL.ReplaceAllString(s, "$1://$2")
	return url.Parse(s)
}
