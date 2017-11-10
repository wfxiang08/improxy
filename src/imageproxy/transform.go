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
	"image"
	// 注册: gif, jpeg, png, webp等格式
	"media_utils"
	"fmt"
	"github.com/chai2010/webp"
	"github.com/disintegration/imaging"
	"github.com/wfxiang08/cyutils/utils/errors"
	log "github.com/wfxiang08/cyutils/utils/rolling_log"
	_ "golang.org/x/image/webp"
	_ "image/gif"
	"image/jpeg"
	"image/png"
	"math"
)

// default compression quality of resized jpegs
const defaultQuality = 80

// resample filter used when resizing images
var resampleFilter = imaging.Lanczos

func DetectFormat(img []byte, opt Options) ([]byte, string, error) {
	m, format, err := image.Decode(bytes.NewReader(img))

	if err != nil {
		return nil, "", err
	}

	// 如果opt中没有指定format, 或者format和预期相同，或者format为gif, 则直接返回
	if opt.Format == "" || format == opt.Format || format == media_utils.ImageFormatGif {
		return nil, format, nil
	}

	format = opt.Format

	buf := new(bytes.Buffer)
	switch format {
	case media_utils.ImageFormatGif:
		fn := func(img image.Image) image.Image {
			return img
		}
		err = GifProcess(buf, bytes.NewReader(img), fn)
		if err != nil {
			return nil, "", err
		}

	case media_utils.ImageFormatWebp:
		// webp格式的数据就暂时以jpg格式保存
		quality := opt.Quality
		if quality == 0 {
			quality = defaultQuality
		}
		err = webp.Encode(buf, m, &webp.Options{Lossless: false, Quality: float32(quality)})
		if err != nil {
			return nil, "", err
		}

	case media_utils.ImageFormatJpg:
		// 标准化文件format: jpg --> jpeg
		format = media_utils.ImageFormatJpeg
		fallthrough
	case media_utils.ImageFormatJpeg:
		quality := opt.Quality
		if quality == 0 {
			quality = defaultQuality
		}
		err = jpeg.Encode(buf, m, &jpeg.Options{Quality: quality})
		if err != nil {
			return nil, "", err
		}
	case media_utils.ImageFormatPng:
		err = png.Encode(buf, m)
		if err != nil {
			return nil, "", err
		}
	default:
		// 默认的情况，直接报错
		return nil, "", errors.New(fmt.Sprintf("image format %s not supported", format))
	}

	return buf.Bytes(), format, nil

}

// Transform the provided image.  img should contain the raw bytes of an
// encoded image in one of the supported formats (gif, jpeg, or png).  The
// bytes of a similarly encoded image is returned.
func Transform(img []byte, opt Options) ([]byte, string, error) {
	// log.Printf("Options: %s, Should Transform: %v", opt.String(), opt.transform())

	// decode image
	m, format, err := image.Decode(bytes.NewReader(img))
	if err != nil {
		log.ErrorErrorf(err, "Image Data Decode Error")
		return nil, "", err
	}

	// 如果用户没有指定Format,
	//      或Format和现有图片一致，
	//      或现有图片为Gif, 则不做格式转换
	if !opt.transform() && (opt.Format == "" || opt.Format == format || media_utils.ImageFormatGif == format) {
		log.Printf("No transform is needed and format is ok")
		return img, format, nil
	}

	// 以用户指定的format为准
	// opt.Format的合法性在imageproxy.go#allow中已经做了检查
	// gif动画的格式不能改变
	//
	if len(opt.Format) > 0 && format != media_utils.ImageFormatGif {
		format = opt.Format
	}

	buf := new(bytes.Buffer)
	switch format {
	case media_utils.ImageFormatGif:
		fn := func(img image.Image) image.Image {
			if opt.transform() {
				return transformImage(img, opt)
			} else {
				return img
			}
		}
		err = GifProcess(buf, bytes.NewReader(img), fn)
		if err != nil {
			return nil, "", err
		}

	case media_utils.ImageFormatWebp:
		// webp格式的数据就暂时以jpg格式保存
		quality := opt.Quality
		if quality == 0 {
			quality = defaultQuality
		}
		if opt.transform() {
			m = transformImage(m, opt)
		}
		err = webp.Encode(buf, m, &webp.Options{Lossless: false, Quality: float32(quality)})
		if err != nil {
			log.ErrorErrorf(err, "webp encode error")
			return nil, "", err
		}
	case media_utils.ImageFormatJpg:
		format = media_utils.ImageFormatJpeg
		fallthrough
	case media_utils.ImageFormatJpeg:
		quality := opt.Quality
		if quality == 0 {
			quality = defaultQuality
		}
		if opt.transform() {
			m = transformImage(m, opt)
			// log.Printf("Transform image ends, m size: %s", m.Bounds().String())
		}
		err = jpeg.Encode(buf, m, &jpeg.Options{Quality: quality})
		if err != nil {
			log.ErrorErrorf(err, "jpeg encode error")
			return nil, "", err
		}
	case media_utils.ImageFormatPng:
		if opt.transform() {
			m = transformImage(m, opt)
		}
		err = png.Encode(buf, m)
		if err != nil {
			log.ErrorErrorf(err, "png encode error")
			return nil, "", err
		}
	default:
		// 默认的情况，直接报错
		return nil, "", errors.New(fmt.Sprintf("image format %s not supported", format))
	}

	return buf.Bytes(), format, nil
}

// resizeParams determines if the image needs to be resized, and if so, the
// dimensions to resize to.
func resizeParams(m image.Image, opt Options) (w, h int, resize bool) {
	// convert percentage width and height values to absolute values
	imgW := m.Bounds().Max.X - m.Bounds().Min.X
	imgH := m.Bounds().Max.Y - m.Bounds().Min.Y

	// 如何进行resize呢?
	// 1. 按照比例来做
	if 0 < opt.Width && opt.Width < 1 {
		w = int(float64(imgW) * opt.Width)
	} else if opt.Width < 0 {
		w = 0
	} else {
		w = int(opt.Width)
	}
	if 0 < opt.Height && opt.Height < 1 {
		h = int(float64(imgH) * opt.Height)
	} else if opt.Height < 0 {
		h = 0
	} else {
		h = int(opt.Height)
	}

	if w > 0 && h > 0 && !opt.Fit && (w > imgW || h > imgH) {
		// 给定的w, h太大，导致图片得不到缩放；现在采取的策略是图片的w, h同时做一个scale，
		// 保证(w, h)*scale之后能得到一个满足比例要求的图片
		// w * scale <= imgW
		// h * scale <= imgH
		// scale <= Min(imgW / w, imgH / h)
		scaleX := float64(imgW) / float64(w) // scaleX, Y 应该 >= 1,
		scaleY := float64(imgH) / float64(h)
		minScale := math.Min(scaleX, scaleY)

		// 如果 scaleX < 1,
		w = int(float64(w) * minScale)
		h = int(float64(h) * minScale)

	} else {
		if w > imgW {
			w = imgW
		}
		if h > imgH {
			h = imgH
		}
	}

	// if requested width and height match the original, skip resizing
	if (w == imgW || w == 0) && (h == imgH || h == 0) {
		return 0, 0, false
	}

	return w, h, true
}

// transformImage modifies the image m based on the transformations specified
// in opt.
func transformImage(m image.Image, opt Options) image.Image {
	// resize if needed
	if w, h, resize := resizeParams(m, opt); resize {
		// log.Printf("resize w: %d, h: %d", w, h)
		if opt.Fit {
			// log.Printf("resize fit")
			m = imaging.Fit(m, w, h, resampleFilter)
		} else {
			if w == 0 || h == 0 {
				// log.Printf("resize one size zero")
				m = imaging.Resize(m, w, h, resampleFilter)
			} else {
				// log.Printf("resize no fit, size: %s", m.Bounds().String())
				m = imaging.Thumbnail(m, w, h, resampleFilter)
				// log.Printf("resize no fit end, size: %s", m.Bounds().String())

			}
		}
	}

	// flip
	if opt.FlipVertical {
		m = imaging.FlipV(m)
	}
	if opt.FlipHorizontal {
		m = imaging.FlipH(m)
	}

	// rotate
	switch opt.Rotate {
	case 90:
		m = imaging.Rotate90(m)
	case 180:
		m = imaging.Rotate180(m)
	case 270:
		m = imaging.Rotate270(m)
	}
	// log.Printf("processed")
	return m
}
