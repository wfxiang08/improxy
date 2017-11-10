package media_utils

import (
	"bytes"
	"fmt"
	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/aws/aws-sdk-go/service/s3"
	log "github.com/wfxiang08/cyutils/utils/rolling_log"
	"io/ioutil"
	"time"

	"config"
	"github.com/aws/aws-sdk-go/aws/credentials"
	"github.com/wfxiang08/cyutils/utils"
)

const (
	ContentTypeJPEG = "image/jpeg"
	ContentTypePNG  = "image/png"
	ContentTypeGIF  = "image/gif"
	ContentTypeWebp = "image/webp"

	ImageFormatPng  = "png"
	ImageFormatWebp = "webp"
	ImageFormatJpeg = "jpeg"
	ImageFormatJpg  = "jpg"
	ImageFormatGif  = "gif"
)

func GetS3Session() *session.Session {
	awsCreditial := credentials.NewStaticCredentials(config.AwsAccessKeyId, config.AwsSecretAccessKey, "")
	cfg := aws.NewConfig().WithRegion(config.AwsRegion).WithCredentials(awsCreditial)
	return session.New(cfg)
}

//
// 将aws s3上的对象的Meta信息，转换成为Http Response中的Cache相关的Headers
//
func S3Meta2Headers(meta *s3.GetObjectOutput) []byte {
	buf := new(bytes.Buffer)

	fmt.Fprintf(buf, "Cache-Control: max-age=%d\n", 2592000) // 1个月的有效期
	if meta.ETag != nil {
		fmt.Fprintf(buf, "ETag: %s\n", *meta.ETag)
	}

	if meta.LastModified != nil {
		fmt.Fprintf(buf, "Last-Modified: %s\n", meta.LastModified.Format(time.RFC1123))
	}

	return buf.Bytes()
}

//
// 从AWS S3上下载图片，并且返回Headers
//
func GetContentFromAWSWithMeta(session *session.Session, bucket, key string) (content []byte, headers []byte, err error) {
	start := time.Now()
	s3Client := s3.New(session)

	result, err := s3Client.GetObject(&s3.GetObjectInput{
		Bucket: aws.String(bucket),
		Key:    aws.String(key),
	})

	if err != nil {
		return nil, nil, err
	}

	defer result.Body.Close()

	// result --> headers
	headers = S3Meta2Headers(result)
	content, err = ioutil.ReadAll(result.Body)

	log.Printf("Elapsed: %.1fms, S3 download, key: %s", utils.ElapsedMillSeconds(start, time.Now()), key)
	return content, headers, err
}
