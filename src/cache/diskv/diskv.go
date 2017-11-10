package diskv

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/ioutil"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const (
	defaultBasePath = "diskv"
	defaultFilePerm os.FileMode = 0666
	defaultPathPerm os.FileMode = 0777
)

var (
	defaultTransform = func(s string) []string {
		return []string{}
	}
	errCanceled = errors.New("canceled")
	errEmptyKey = errors.New("empty key")
	errBadKey = errors.New("bad key")
)

// TransformFunction transforms a key into a slice of strings, with each
// element in the slice representing a directory in the file path where the
// key's entry will eventually be stored.
//
// For example, if TransformFunc transforms "abcdef" to ["ab", "cde", "f"],
// the final location of the data file will be <basedir>/ab/cde/f/abcdef
type TransformFunction func(s string) []string

type Options struct {
	BasePath     string
	Transform    TransformFunction
	CacheSizeMax uint64 // bytes, 内存中的Cache Size
	PathPerm     os.FileMode
	FilePerm     os.FileMode
}

// Diskv implements the Diskv interface. You shouldn't construct Diskv
// structures directly; instead, use the New constructor.
type Diskv struct {
	Options
	mu        sync.RWMutex // 读写锁
	cache     map[string][]byte
	cacheSize uint64
}

// New returns an initialized Diskv structure, ready to use.
// If the path identified by baseDir already contains data,
// it will be accessible, but not yet cached.
func New(o Options) *Diskv {
	if o.BasePath == "" {
		o.BasePath = defaultBasePath
	}
	// 默认的Transform函数？
	if o.Transform == nil {
		o.Transform = defaultTransform
	}
	if o.PathPerm == 0 {
		o.PathPerm = defaultPathPerm
	}
	if o.FilePerm == 0 {
		o.FilePerm = defaultFilePerm
	}

	d := &Diskv{
		Options:   o,
		cache:     map[string][]byte{},
		cacheSize: 0,
	}

	return d
}

// Write synchronously writes the key-value pair to disk, making it immediately
// available for reads. Write relies on the filesystem to perform an eventual
// sync to physical media. If you need stronger guarantees, see WriteStream.
func (d *Diskv) Write(key string, val []byte) error {
	return d.WriteStream(key, bytes.NewBuffer(val), false)
}

// WriteStream writes the data represented by the io.Reader to the disk, under
// the provided key. If sync is true, WriteStream performs an explicit sync on
// the file as soon as it's written.
//
// bytes.Buffer provides io.Reader semantics for basic data types.
func (d *Diskv) WriteStream(key string, r io.Reader, sync bool) error {
	if len(key) <= 0 {
		return errEmptyKey
	}

	// 图片的读写是经过lock控制的
	d.mu.Lock()
	defer d.mu.Unlock()

	return d.writeStreamWithLock(key, r, sync)
}

func (d *Diskv) writeStreamWithLock(key string, r io.Reader, sync bool) error {

	if err := d.ensurePathWithLock(key); err != nil {
		return fmt.Errorf("ensure path: %s", err)
	}

	mode := os.O_WRONLY | os.O_CREATE | os.O_TRUNC // overwrite if exists

	// 获取文件句柄
	f, err := os.OpenFile(d.completeFilename(key), mode, d.FilePerm)
	if err != nil {
		return fmt.Errorf("open file: %s", err)
	}

	wc := io.WriteCloser(&nopWriteCloser{f})

	// 写文件
	if _, err := io.Copy(wc, r); err != nil {
		f.Close() // error deliberately ignored
		return fmt.Errorf("i/o copy: %s", err)
	}

	if err := wc.Close(); err != nil {
		f.Close() // error deliberately ignored
		return fmt.Errorf("compression close: %s", err)
	}

	// 同步到磁盘上
	if sync {
		if err := f.Sync(); err != nil {
			f.Close() // error deliberately ignored
			return fmt.Errorf("file sync: %s", err)
		}
	}

	if err := f.Close(); err != nil {
		return fmt.Errorf("file close: %s", err)
	}

	// 删除对应的key，表示之前的数据无效
	// 缓存只由Read操作来更新
	d.bustCacheWithLock(key) // cache only on read

	return nil
}

// Read reads the key and returns the value.
// If the key is available in the cache, Read won't touch the disk.
// If the key is not in the cache, Read will have the side-effect of
// lazily caching the value.
func (d *Diskv) Read(key string) ([]byte, error) {
	rc, err := d.ReadStream(key)
	if err != nil {
		return []byte{}, err
	}
	defer rc.Close()
	return ioutil.ReadAll(rc)
}

// ReadStream reads the key and returns the value (data) as an io.ReadCloser.
// If the value is cached from a previous read, and direct is false,
// ReadStream will use the cached value. Otherwise, it will return a handle to
// the file on disk, and cache the data on read.
//
// If compression is enabled, ReadStream taps into the io.Reader stream prior
// to decompression, and caches the compressed data.
func (d *Diskv) ReadStream(key string) (io.ReadCloser, error) {
	d.mu.RLock()
	defer d.mu.RUnlock()

	// 判断是否在cache中，不是则直接返回
	if val, ok := d.cache[key]; ok {
		// 将 []byte 转换成为 Buffer
		buf := bytes.NewBuffer(val)
		return ioutil.NopCloser(buf), nil
	} else {
		return d.readWithRLock(key)
	}
}

// read ignores the cache, and returns an io.ReadCloser representing the
// decompressed data for the given key, streamed from the disk. Clients should
// acquire a read lock on the Diskv and check the cache themselves before
// calling read.
func (d *Diskv) readWithRLock(key string) (io.ReadCloser, error) {
	filename := d.completeFilename(key)

	fi, err := os.Stat(filename)
	if err != nil {
		return nil, err
	}
	if fi.IsDir() {
		return nil, os.ErrNotExist
	}

	f, err := os.Open(filename)
	if err != nil {
		return nil, err
	}

	// 如何处理CacheSize呢?
	var r io.Reader
	if d.CacheSizeMax > 0 {
		now := time.Now()
		os.Chtimes(filename, now, now)
		r = newSiphon(f, d, key)
	} else {
		r = &closingReader{f}
	}

	var rc = io.ReadCloser(ioutil.NopCloser(r))
	return rc, nil
}

// closingReader provides a Reader that automatically closes the
// embedded ReadCloser when it reaches EOF
type closingReader struct {
	rc io.ReadCloser
}

func (cr closingReader) Read(p []byte) (int, error) {
	n, err := cr.rc.Read(p)
	if err == io.EOF {
		if closeErr := cr.rc.Close(); closeErr != nil {
			return n, closeErr // close must succeed for Read to succeed
		}
	}
	return n, err
}

// siphon is like a TeeReader: it copies all data read through it to an
// internal buffer, and moves that buffer to the cache at EOF.
type siphon struct {
	f   *os.File
	d   *Diskv
	key string
	buf *bytes.Buffer
}

// newSiphon constructs a siphoning reader that represents the passed file.
// When a successful series of reads ends in an EOF, the siphon will write
// the buffered data to Diskv's cache under the given key.
func newSiphon(f *os.File, d *Diskv, key string) io.Reader {
	return &siphon{
		f:   f,
		d:   d,
		key: key,
		buf: &bytes.Buffer{},
	}
}

// Read implements the io.Reader interface for siphon.
func (s *siphon) Read(p []byte) (int, error) {
	n, err := s.f.Read(p)

	if err == nil {
		return s.buf.Write(p[0:n]) // Write must succeed for Read to succeed
	}

	if err == io.EOF {
		s.d.cacheWithoutLock(s.key, s.buf.Bytes()) // cache may fail
		if closeErr := s.f.Close(); closeErr != nil {
			return n, closeErr // close must succeed for Read to succeed
		}
		return n, err
	}

	return n, err
}

// Erase synchronously erases the given key from the disk and the cache.
// 同时删除cache和文件
//
func (d *Diskv) Erase(key string) error {
	d.mu.Lock()
	defer d.mu.Unlock()

	// 1. 从内存中删除 key
	d.bustCacheWithLock(key)

	// erase from disk
	// 2. 从磁盘上删除文件
	filename := d.completeFilename(key)
	if s, err := os.Stat(filename); err == nil {
		if s.IsDir() {
			return errBadKey
		}
		if err = os.Remove(filename); err != nil {
			return err
		}
	} else {
		// Return err as-is so caller can do os.IsNotExist(err).
		return err
	}

	// 删除空的目录
	d.pruneDirsWithLock(key)
	return nil
}

//
// 删除所有的数据, 注意设置d.BasePath
//
func (d *Diskv) EraseAll() error {
	d.mu.Lock()
	defer d.mu.Unlock()

	if len(d.BasePath) <= 0 {
		panic("Invalid Base Path")
	}

	// 直接清空cache 和删除 根目录
	d.cache = make(map[string][]byte)
	d.cacheSize = 0
	return os.RemoveAll(d.BasePath)
}

// Has returns true if the given key exists.
func (d *Diskv) Has(key string) bool {
	d.mu.Lock()
	defer d.mu.Unlock()

	// 首先看内存是否有数据
	if _, ok := d.cache[key]; ok {
		return true
	}

	// 如果内存中没有数据，则看文件是否存在
	filename := d.completeFilename(key)
	s, err := os.Stat(filename)
	if err != nil {
		return false
	}
	if s.IsDir() {
		return false
	}

	return true
}

// Keys returns a channel that will yield every key accessible by the store,
// in undefined order. If a cancel channel is provided, closing it will
// terminate and close the keys channel.
func (d *Diskv) Keys(cancel <-chan struct{}) <-chan string {
	return d.KeysPrefix("", cancel)
}

// KeysPrefix returns a channel that will yield every key accessible by the
// store with the given prefix, in undefined order. If a cancel channel is
// provided, closing it will terminate and close the keys channel. If the
// provided prefix is the empty string, all keys will be yielded.
func (d *Diskv) KeysPrefix(prefix string, cancel <-chan struct{}) <-chan string {
	var prepath string
	if prefix == "" {
		prepath = d.BasePath
	} else {
		prepath = d.pathFor(prefix)
	}
	c := make(chan string)
	go func() {
		filepath.Walk(prepath, walker(c, prefix, cancel))
		close(c)
	}()
	return c
}

// walker returns a function which satisfies the filepath.WalkFunc interface.
// It sends every non-directory file entry down the channel c.
func walker(c chan <- string, prefix string, cancel <-chan struct{}) filepath.WalkFunc {
	return func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		if info.IsDir() || !strings.HasPrefix(info.Name(), prefix) {
			return nil // "pass"
		}

		select {
		case c <- info.Name():
		case <-cancel:
			return errCanceled
		}

		return nil
	}
}

//
// 如何获取key对应的"目录名"
//
func (d *Diskv) pathFor(key string) string {
	return filepath.Join(d.BasePath, filepath.Join(d.Transform(key)...))
}

// 确保key对应的dir存在
func (d *Diskv) ensurePathWithLock(key string) error {
	return os.MkdirAll(d.pathFor(key), d.PathPerm)
}

// 完整的文件名: key_path + key
func (d *Diskv) completeFilename(key string) string {
	return filepath.Join(d.pathFor(key), key)
}

//
// 将val保存到key指定的文件下, 但是没有写文件
//
func (d *Diskv) cacheWithLock(key string, val []byte) error {
	valueSize := uint64(len(val))

	// 确保有足够的磁盘空间
	// 内存只保留cache的索引信息
	if err := d.ensureCacheSpaceWithLock(valueSize); err != nil {
		return fmt.Errorf("%s; not caching", err)
	}

	// 非常严格地控制磁盘空间的大小
	if (d.cacheSize + valueSize) > d.CacheSizeMax {
		panic(fmt.Sprintf("failed to make room for value (%d/%d)", valueSize, d.CacheSizeMax))
	}

	// 添加新的文件
	d.cache[key] = val
	d.cacheSize += valueSize
	return nil
}

//
// 获取读写锁的 写权限
// 将数据cache起来, 在Read函数中调用，因此文件应该已经存在
//
func (d *Diskv) cacheWithoutLock(key string, val []byte) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.cacheWithLock(key, val)
}

//
// 从cache中删除指定的key, 已经对应的cache size信息
//
func (d *Diskv) bustCacheWithLock(key string) {

	if val, ok := d.cache[key]; ok {
		d.uncacheWithLock(key, uint64(len(val)))
	}
}

// 从HashMap中删除key，不删除磁盘上的文案
func (d *Diskv) uncacheWithLock(key string, sz uint64) {
	d.cacheSize -= sz
	delete(d.cache, key)
}

//
// 删除空的目录
//
func (d *Diskv) pruneDirsWithLock(key string) error {
	pathlist := d.Transform(key)

	for i := range pathlist {
		dir := filepath.Join(d.BasePath, filepath.Join(pathlist[:len(pathlist) - i]...))

		// thanks to Steven Blenkinsop for this snippet
		switch fi, err := os.Stat(dir); true {
		case err != nil:
			return err
		case !fi.IsDir():
			panic(fmt.Sprintf("corrupt dirstate at %s", dir))
		}

		// 删除空的目录
		nlinks, err := filepath.Glob(filepath.Join(dir, "*"))
		if err != nil {
			return err
		} else if len(nlinks) > 0 {
			return nil // has subdirs -- do not prune
		}
		if err = os.Remove(dir); err != nil {
			return err
		}
	}

	return nil
}

//
// 确保存在足够的内存空间
//
func (d *Diskv) ensureCacheSpaceWithLock(valueSize uint64) error {

	// 防止出现过大的文件
	if valueSize > d.CacheSizeMax {
		return fmt.Errorf("value size (%d bytes) too large for cache (%d bytes)", valueSize, d.CacheSizeMax)
	}

	// 确保有足够的空间
	safe := func() bool {
		return (d.cacheSize + valueSize) <= d.CacheSizeMax
	}

	// 遍历cache, 删除其中的key(如果服务重启?)
	for key, val := range d.cache {
		if safe() {
			break
		}

		d.uncacheWithLock(key, uint64(len(val)))
	}

	if !safe() {
		panic(fmt.Sprintf("%d bytes still won't fit in the cache! (max %d bytes)", valueSize, d.CacheSizeMax))
	}

	return nil
}

// nopWriteCloser wraps an io.Writer and provides a no-op Close method to
// satisfy the io.WriteCloser interface.
type nopWriteCloser struct {
	io.Writer
}

func (wc *nopWriteCloser) Write(p []byte) (int, error) {
	return wc.Writer.Write(p)
}
func (wc *nopWriteCloser) Close() error {
	return nil
}
