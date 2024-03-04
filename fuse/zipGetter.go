package main

import (
	"archive/zip"
	"io"
	"os"
	pathMod "path"
	"path/filepath"
	"strings"
	"syscall"
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
	attr               fuse.Attr
	dataOffset         int64
	compressedSize64   uint64
	uncompressedSize64 uint64
	method             uint16
	zipPath            *string
}

// type filesFdsCache expirable.LRU[string, *os.File] //also use for zip (NewReader)

func (this *zipFileData) ReadFile() ([]byte, syscall.Errno) {
	file, e := os.Open(*this.zipPath) //todo lru cache
	if e != nil {
		return nil, syscall.EIO
	}
	decomp := decompressor(this.method)

	reader := io.NewSectionReader(file, this.dataOffset, int64(this.compressedSize64))
	res := decomp(reader)
	defer res.Close()

	text := make([]byte, this.uncompressedSize64)

	read, e2 := io.ReadFull(res, text)
	if e2 != nil {
		return nil, syscall.EIO
	}
	if read != int(this.uncompressedSize64) {
		return nil, syscall.EIO
	}
	return text, 0
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

func processZip(zr *zip.ReadCloser, zipPath *string, stripPrefix string, inoStart uint64) *proccessedZip {
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
					dataOffset, _ := f.DataOffset()
					// if err //todo
					filesData[fullPath] = &zipFileData{
						attr:               getZFAttrs(f),
						dataOffset:         dataOffset,
						compressedSize64:   f.CompressedSize64,
						uncompressedSize64: f.UncompressedSize64,
						method:             f.Method,
						zipPath:            zipPath,
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
	defer f.Close() //don't affect memory
	zr = processZip(f, &path, stripPrefix, inoStart)
	zg.cache.Add(path, zr)
	return zr, nil
}

func createZipGetter() *zipGetter {
	lru := expirable.NewLRU[string, *proccessedZip](30, func(k string, v *proccessedZip) {
		v.zip.Close()
	}, time.Second*20)

	zg := &zipGetter{cache: lru}

	return zg
}
