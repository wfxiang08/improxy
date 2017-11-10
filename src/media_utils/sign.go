package media_utils

import (
	"config"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"fmt"
	"hash/crc32"
	"strings"
	"time"
)

var (
	ParamVersionTs = "ts"
	ParamToken     = "tk"
)

/**
 * 这部分只是用于数据的读取,不用于预签名
 * 为了提高"客户端缓存"的命中率, URL需要保持稳定; 大家在一个约定的时间点过期;
 * t0       t1       t2      t3
 * time == t0       --> t1过期
 * t0 < time <= t1  --> t2过期, 也即是(t0, t1]内的URL应该是保持一致
 * 为了防止在所有的资源在同一个时间点过期, $fixed_expire 又增加了一个和key相关的offset
 */
func GenerateAlignedExpire(key string, expiresSeconds int64) int64 {

	if expiresSeconds < 3600*24 {
		expiresSeconds = 3600 * 24
	}

	offset := int64(crc32.ChecksumIEEE([]byte(key))) % expiresSeconds
	now := time.Now().Unix()

	// fmt.Printf("NOW: %d\n", now)
	// 上对齐 + offset
	alignedExpire := ((now+expiresSeconds-1)/expiresSeconds)*expiresSeconds + offset

	// echo "Now: {$now}, Align: {$aligned_expire}, Offset: {$offset}\n";

	// 保证缓存至少在一个 $expires_seconds 周期内有效
	if alignedExpire-now < expiresSeconds {
		alignedExpire = alignedExpire + expiresSeconds
		// echo "Now: {$now}, Align: {$aligned_expire}, Offset: {$offset}\n";
	}
	return alignedExpire
}

func SimpleVerify(path, ts string, token string, checkExpire bool) bool {

	if strings.HasPrefix(path, "/") {
		path = path[1:]
	}

	var err error
	var tokenBytes []byte
	tokenBytes, err = base64.RawURLEncoding.DecodeString(token)
	// tokenBytes
	// 后四位为过期时间, 前面为signature
	if err != nil || len(tokenBytes) < 5 {
		return false
	}

	// 1. 解码过期时间
	expireTime := binary.BigEndian.Uint32(tokenBytes[len(tokenBytes)-4:]) ^ uint32(config.MagicNum)
	// fmt.Printf("ExpireTimeVerify: %d\n", expireTime)
	// 比较过期
	if checkExpire && (time.Now().Unix() > int64(expireTime)) {
		return false
	}

	oe := SimpleTimeByteToStr(tokenBytes[len(tokenBytes)-4:])

	// 2. 计算token
	want := SimpleToken(path, ts, oe)

	// 3. 验证token是否正确
	return hmac.Equal(tokenBytes[0:len(tokenBytes)-4], want)
}

func SimpleToken(path, ts, oe string) []byte {
	// fmt.Printf("path: %s, ts: %s, oe: %s\n", path, ts, oe)
	mac := hmac.New(sha256.New, config.SimpleKey)
	mac.Write([]byte(path))
	if len(ts) > 0 {
		mac.Write([]byte("?ts=" + ts))
		mac.Write([]byte("&oe=" + oe))
	} else {
		mac.Write([]byte("?oe=" + oe))
	}
	return mac.Sum(nil)
}

func SimpleSignUrl(path, ts string, relativeExpire int64) string {
	if strings.HasPrefix(path, "/") {
		path = path[1:]
	}

	expire := GenerateAlignedExpire(path, relativeExpire)
	return SimpleSignUrlWithTime(path, ts, expire)
}

func SimpleTimeToStr(time int64) (string, []byte) {
	expires := make([]byte, 4)
	binary.BigEndian.PutUint32(expires, uint32(time^config.MagicNum))
	return base64.RawURLEncoding.EncodeToString(expires), expires
}

func SimpleTimeByteToStr(time []byte) string {
	return base64.RawURLEncoding.EncodeToString(time)
}

func SimpleSignUrlWithTime(path, ts string, time int64) string {

	if strings.HasPrefix(path, "/") {
		path = path[1:]
	}

	oe, oeBytes := SimpleTimeToStr(time)

	want := SimpleToken(path, ts, oe)
	want = append(want, oeBytes...)

	token := base64.RawURLEncoding.EncodeToString(want)

	if len(ts) > 0 {
		return fmt.Sprintf("%s?ts=%s&tk=%s", path, ts, token)
	} else {
		return fmt.Sprintf("%s?tk=%s", path, token)
	}
}
