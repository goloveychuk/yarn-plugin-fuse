package zip

import (
	"archive/zip"
	"io"
	"io/fs"
	"log"
	"os"
	pathMod "path"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/hanwen/go-fuse/v2/fuse"
	"github.com/hashicorp/golang-lru/v2/expirable"
)

type fListedData struct {
	index    uint64
	fileData *zipFileData
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
	chMap       map[string]map[string]*fListedData
	mu          sync.Mutex
	processed   bool
	zipPath     *string
	stripPrefix string
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

func (this *proccessedZip) processZip() (*proccessedZip, error) {
	this.mu.Lock()
	defer this.mu.Unlock()
	if this.processed {
		return this, nil
	}
	zipPath := this.zipPath
	stripPrefix := this.stripPrefix

	zr, err := zip.OpenReader(*zipPath)
	if err != nil {
		return nil, err
	}
	defer zr.Close()

	for fileIndex, f := range zr.File {

		cleaned := filepath.Clean(f.Name)
		if !strings.HasPrefix(cleaned, stripPrefix) {
			continue
		}

		path := strings.TrimPrefix(cleaned, stripPrefix)
		if path == "" {
			continue
		}

		mode := f.FileInfo().Mode()

		parts := strings.Split(path, string(os.PathSeparator))

		prev := ""
		for ind, part := range parts {
			if _, ok := this.chMap[prev]; !ok {
				this.chMap[prev] = make(map[string]*fListedData)
			}
			fullPath := pathMod.Join(prev, part)
			if ind == len(parts)-1 {
				var fileData *zipFileData = nil

				if mode.IsDir() {

				} else if mode.IsRegular() {
					dataOffset, _ := f.DataOffset()
					// if err //todo
					fileData = &zipFileData{
						attr:               getZFAttrs(f),
						dataOffset:         dataOffset,
						compressedSize64:   f.CompressedSize64,
						uncompressedSize64: f.UncompressedSize64,
						method:             f.Method,
						zipPath:            zipPath,
					}
				} else if mode&fs.ModeSymlink != 0 {
					log.Fatalf("Not impl for for symlinks %s, %s, %s", mode, f.Name, *zipPath)
				} else {
					log.Fatalf("Unknown file mode %s, %s, %s", mode, f.Name, *zipPath)
				}
				d := &fListedData{
					index:    uint64(fileIndex),
					fileData: fileData,
				}
				this.chMap[prev][part] = d
			}
			prev = fullPath
		}
	}
	this.processed = true
	return this, nil
}

func newProcessedZip(path *string, stripPrefix string) *proccessedZip {
	return &proccessedZip{chMap: make(map[string]map[string]*fListedData), zipPath: path, stripPrefix: stripPrefix}
}

type zipGetter struct {
	cache *expirable.LRU[string, *proccessedZip]
	mu    sync.Mutex
}

type IZipGetter interface {
	GetZip(path string, stripPrefix string) (*proccessedZip, error)
}

func (zg *zipGetter) GetZip(path string, stripPrefix string) (*proccessedZip, error) {
	zg.mu.Lock()
	if zr, ok := zg.cache.Get(path); ok {
		zg.mu.Unlock()
		return zr.processZip()
	}
	zr := newProcessedZip(&path, stripPrefix)
	zg.cache.Add(path, zr)
	zg.mu.Unlock()
	return zr.processZip()
}

func CreateZipGetter() IZipGetter {
	lru := expirable.NewLRU[string, *proccessedZip](30, func(k string, v *proccessedZip) {

	}, time.Second*20)

	zg := &zipGetter{cache: lru}

	return zg
}
