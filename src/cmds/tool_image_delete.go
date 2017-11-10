package main

import (
	"flag"
	"github.com/wfxiang08/cyutils/utils"
	"github.com/wfxiang08/cyutils/utils/atomic2"
	"github.com/wfxiang08/cyutils/utils/errors"
	log "github.com/wfxiang08/cyutils/utils/rolling_log"
	"media_utils"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

var (
	fileDir   = flag.String("dir", "/data/tmp_improxy/cache", "image cache dir")
	logPrefix = flag.String("log", "", "log file prefix")
	logLevel  = flag.String("level", "", "log level")
)
var deleteFile = flag.Bool("delete", false, "delete old images")

const DEFAULT_TIME_FORMAT = "2006-01-02 15:04:05"

func main() {
	flag.Parse()
	if !strings.HasPrefix(*fileDir, "/data/tmp_improxy/cache") {
		log.Printf("dir must begin with: /data/tmp_improxy/cache")
		return
	}

	// 1. 解析Log相关的配置
	if len(*logPrefix) > 0 {
		f, err := log.NewRollingFile(*logPrefix, 3)
		if err != nil {
			log.PanicErrorf(err, "open rolling log file failed: %s", *logPrefix)
		} else {
			// 不能放在子函数中
			defer f.Close()
			log.StdLog = log.New(f, "")
		}
	}

	// 默认是Debug模式
	log.SetLevel(log.LEVEL_DEBUG)
	log.SetFlags(log.Flags() | log.Lshortfile)

	// set log level
	if len(*logLevel) > 0 {
		log.SetLogLevel(*logLevel)
	}

	sigchan := make(chan os.Signal, 1)
	signal.Notify(sigchan, syscall.SIGINT, syscall.SIGTERM)

	var isRunning atomic2.Bool
	isRunning.Set(true)

	// 异步等待关闭
	go func() {
		// 等待关闭
		sig := <-sigchan
		log.Printf(utils.Red("Caught signal %v: terminating\n"), sig)
		isRunning.Set(false)

	}()

	deleteCount := int64(0)
	totalSize := int64(0)
	toDelete := *deleteFile
	now := time.Now()
	gbScale := 1 / (1024.0 * 1024.0 * 1024.0)
	filepath.Walk(*fileDir, func(path string, info os.FileInfo, err error) error {
		if !isRunning.Get() {
			return errors.New("Process stopped")
		}

		// 不处理目录
		if info.IsDir() {
			return nil
		}

		log.Printf("path: %s\n", path)

		if now.Sub(info.ModTime()) > time.Hour*24*10 {
			deleteCount++
			totalSize += info.Size()
			log.Printf("Num: %06d, Size: %.3fG -- Deleting file: %s, Time: %s", deleteCount, float64(totalSize)*gbScale, path, info.ModTime().Format(DEFAULT_TIME_FORMAT))
			if toDelete {
				os.Remove(path)
			}
			if deleteCount%1000 == 0 {
				time.Sleep(time.Millisecond * 200)
			}

		}
		return nil
	})
	log.Printf(utils.Green("Process Closed\n"))
}
