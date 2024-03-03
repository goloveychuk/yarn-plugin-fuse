package main

import (
	"archive/zip"
	"os"
	pathMod "path"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/hanwen/go-fuse/v2/fuse"
	"github.com/hashicorp/golang-lru/v2/expirable"
)

const (
	DIR  = uint8(1)
	FILE = uint8(2)
)

type fListedData struct {
	ino uint64
	typ uint8
}

type zipFileData struct {
	attr fuse.Attr
	mu   *sync.Mutex //todo impl
	file *zip.File
}
type proccessedZip struct {
	chMap     map[string]map[string]*fListedData
	filesData map[string]*zipFileData
	zip       *zip.ReadCloser
}

func getZFAttrs(f *zip.File) fuse.Attr {
	t := uint64(f.ModTime().Unix())
	const bs = 512 //why?
	return fuse.Attr{
		Mode:    uint32(f.Mode()) & 07777,
		Size:    f.UncompressedSize64,
		Mtime:   t,
		Atime:   t,
		Ctime:   t,
		Blksize: bs,
		Blocks:  (f.UncompressedSize64 + bs - 1) / bs,
	}
}

func processZip(zr *zip.ReadCloser, stripPrefix string, inoStart uint64) *proccessedZip {
	chMap := make(map[string]map[string]*fListedData)
	filesData := make(map[string]*zipFileData)
	curIno := inoStart

	for _, f := range zr.File {

		cleaned := filepath.Clean(f.Name)
		if !strings.HasPrefix(cleaned, stripPrefix) {
			continue
		}

		path := strings.TrimPrefix(cleaned, stripPrefix)
		if path == "" {
			continue
		}

		isFile := !f.FileInfo().IsDir()

		parts := strings.Split(path, string(os.PathSeparator))

		prev := ""
		for ind, part := range parts {
			if _, ok := chMap[prev]; !ok {
				chMap[prev] = make(map[string]*fListedData)
			}
			fullPath := pathMod.Join(prev, part)
			if ind == len(parts)-1 {
				var typ uint8
				if isFile {
					typ = FILE
					filesData[fullPath] = &zipFileData{
						attr: getZFAttrs(f),
						file: f,
					}

				} else {
					typ = DIR
				}
				d := &fListedData{
					ino: curIno,
					typ: typ,
				}
				curIno += 1
				chMap[prev][part] = d
			}
			prev = fullPath
		}

	}
	return &proccessedZip{chMap: chMap, filesData: filesData, zip: zr}

}

type zipGetter struct {
	cache *expirable.LRU[string, *proccessedZip]
}

func (zg *zipGetter) GetZip(path string, stripPrefix string, inoStart uint64) (*proccessedZip, error) {
	zr, ok := zg.cache.Get(path)
	if ok {
		return zr, nil
	}

	f, err := zip.OpenReader(path)
	if err != nil {
		return nil, err
	}
	zr = processZip(f, stripPrefix, inoStart)
	zg.cache.Add(path, zr)
	// f.Close() //don't affect memory
	return zr, nil
}

func createZipGetter() *zipGetter {
	lru := expirable.NewLRU[string, *proccessedZip](100, func(k string, v *proccessedZip) {
		v.zip.Close()
	}, time.Second*20)

	zg := &zipGetter{cache: lru}

	return zg
}
