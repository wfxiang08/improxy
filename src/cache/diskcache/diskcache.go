package diskcache

import (
	"bytes"
	"cache/diskv"
	"crypto/md5"
	"encoding/hex"
	"io"
)

//
// 在diskv的基础上，做了一层封装; 改进的地方是实现了key到Filename的映射
//
type Cache struct {
	d *diskv.Diskv
}

// Get returns the response corresponding to key if present
func (c *Cache) Get(key string) (resp []byte, ok bool) {

	key = keyToFilename(key)
	resp, err := c.d.Read(key)
	if err != nil {
		return []byte{}, false
	}
	return resp, true
}

// Set saves a response to the cache as key
func (c *Cache) Set(key string, resp []byte) {
	key = keyToFilename(key)
	c.d.WriteStream(key, bytes.NewReader(resp), true)
}

//
// 删除key
// 删除文件
//
func (c *Cache) Delete(key string) {
	key = keyToFilename(key)
	c.d.Erase(key)
}

func (c *Cache) Exists(key string) bool {
	key = keyToFilename(key)
	hasKey := c.d.Has(key)
	return hasKey
}

//
// 将 key 通过md5 转换成为 hex string
//
func keyToFilename(key string) string {
	h := md5.New()
	io.WriteString(h, key)
	return hex.EncodeToString(h.Sum(nil))
}

func New(basePath string) *Cache {
	return &Cache{
		d: diskv.New(diskv.Options{
			BasePath:     basePath, // 缓存的根目录
			CacheSizeMax: 5 * 1024 * 1024 * 1024, // 内存Cache
		}),
	}
}

func NewWithDiskv(d *diskv.Diskv) *Cache {
	return &Cache{d}
}
