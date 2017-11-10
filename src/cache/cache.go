package cache

//
// 定义Cache的接口
//
type Cache interface {
	Get(key string) (data []byte, ok bool)
	Set(key string, data []byte)
	Delete(key string)
	Exists(key string) bool
}

//
// NopCache的实现，Operation
//
var NopCache = new(nopCache)

type nopCache struct{}

func (c nopCache) Get(string) ([]byte, bool) {
	return nil, false
}

func (c nopCache) Set(string, []byte) {}
func (c nopCache) Delete(string) {}
func (c nopCache) Exists(string) bool {
	return true
}
