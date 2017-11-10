package media_utils

import (
	"fmt"
	"net/url"
	"testing"
	"time"
)

// go test media_utils -v -run "TestSign"
func TestSign(t *testing.T) {
	now := time.Now().Unix()
	var tests = []struct {
		Key      string
		Ts       string
		Time     int64
		Verified bool
	}{
		{
			"production/uploading/recordings/6755399443954614/cover_image.png",
			"",
			now - 10,
			false, // 验证时间过期
		},
		{
			"production/uploading/recordings/6755399443954614/cover_image.png",
			"121212",
			now + 10,
			true,
		},
	}

	for _, tt := range tests {

		// 如何签名?
		relativeUrl := SimpleSignUrlWithTime(tt.Key, tt.Ts, tt.Time)
		fmt.Printf("Url: %s, Time: %d\n", relativeUrl, tt.Time)

		parsedUrl, err := url.Parse(relativeUrl)
		if err != nil {
			t.Errorf("Invalid relativeUrl")
		}

		params := parsedUrl.Query()
		token := params.Get("tk")

		// 如何验证有效
		verified := SimpleVerify(tt.Key, tt.Ts, token, true)
		fmt.Printf("Verfied: %v\n", verified)
		if verified != tt.Verified {
			t.Errorf("Verified not match")
		}

		relativeUrl = SimpleSignUrl(tt.Key, tt.Ts, 3600*24*7)
		fmt.Printf("relativeUrl: %s\n", relativeUrl)
		parsedUrl, err = url.Parse(relativeUrl)
		if err != nil {
			t.Errorf("Invalid relativeUrl")
		}
		params = parsedUrl.Query()
		ts := params.Get(ParamVersionTs)
		token = params.Get(ParamToken)

		verified = SimpleVerify(tt.Key, ts, token, true)
		if !verified {
			t.Errorf("Verified failed")
		}
	}

}
