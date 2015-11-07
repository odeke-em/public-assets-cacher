package main

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"time"

	expirableCache "github.com/odeke-em/cache"
	watnot "github.com/odeke-em/watnot-dev/src"
)

const (
	Byte  = 1
	KByte = 1024 * Byte
	MByte = 1024 * KByte
)

var MaxThresholdCacheSize = int64(80 * MByte)

var globalDataCache = expirableCache.New()

func relToPublicPath(p string) string {
	return path.Join(".", "public", p)
}

func newExpirableValue30MinuteOffset(data interface{}) *expirableCache.ExpirableValue {
	return expirableCache.NewExpirableValueWithOffset(data, uint64(time.Hour))
}

func retrievePublicResource(subPath string) (data string, err error) {
	key := relToPublicPath(subPath)
	retr, ok := globalDataCache.Get(key)
	if ok {
		value := retr.Value()
		data, ok = value.(string)
		if ok {
			fmt.Printf("cache hit for %v\n", key)
			return
		}
		// TODO: Decide if a failed cast should
		// fall through and graduate to a cache miss
	}
	return memoizePublicResource(subPath)
}

func memoizePublicResource(subPath string) (data string, err error) {
	key := relToPublicPath(subPath)

	fInfo, statErr := os.Stat(key)
	if statErr != nil {
		err = statErr
		return
	}

	if fInfo == nil {
		err = fmt.Errorf("nil fInfo")
		return
	}

	if fInfo.IsDir() {
		err = fmt.Errorf("dir cannot be read from")
		return
	}

	fileSize := fInfo.Size()
	if fileSize >= MaxThresholdCacheSize {
		err = fmt.Errorf("entity too large, cannot fit in cache")
		return
	}

	fh, fErr := os.Open(key)
	if fErr != nil {
		err = fErr
		return
	}

	inMemoryBuffer := make([]byte, fileSize)
	io.ReadAtLeast(fh, inMemoryBuffer, 1)

	fh.Close()

	data = string(inMemoryBuffer)
	value := newExpirableValue30MinuteOffset(data)
	if inserted := globalDataCache.Put(key, value); inserted {
		fmt.Printf("cached: %v\n", key)
	}

	return
}

func main() {
	http.HandleFunc("/", rootHandler)

	go func() {
		cancellation := make(chan bool)
		publicDirCacheEvicter(cancellation)
	}()

	err := http.ListenAndServe(":8000", nil)
	if err != nil {
		fmt.Fprintf(os.Stderr, "running server encountered err: %v\n", err)
	}
}

func publicDirCacheEvicter(done chan bool) {
	go func() {
		publicRawPath, err := filepath.Abs("./public")
		fmt.Println(publicRawPath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "publicDirCacheEvicter: absPath err: %v\n", err)
			return
		}

		changedFilesChan := watnot.NewWatcher(publicRawPath)
		for absPath := range changedFilesChan {
			// Check to see if we've been cancelled
			select {
			case sig := <-done:
				if sig {
					return
				}
			default:
			}

			relPath := filepath.Base(absPath)
			if err != nil {
				fmt.Fprintf(os.Stderr, "publicDirCacheEvicter: %q %q err: %v\n", absPath, relPath, err)
				continue
			}

			toPublicPath := relToPublicPath(relPath)
			// Invalidate the cache
			if _, existed := globalDataCache.Remove(toPublicPath); existed {
				fmt.Println("evicted", toPublicPath)
			}
		}
	}()
}

func rootHandler(res http.ResponseWriter, req *http.Request) {
	url := req.URL.String()
	if url == "" || url == "/" {
		url = "index.html"
	}
	data, err := retrievePublicResource(url)
	if err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		http.Error(res, "error encountered please try again", 500)
		return
	}

	fmt.Fprintf(res, "%s", data)
}
