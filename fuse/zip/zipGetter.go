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
	"syscall"
	"time"

	"github.com/hanwen/go-fuse/v2/fuse"
	"github.com/hashicorp/golang-lru/v2/expirable"
)

type fListedData struct {
	ino      uint64
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
	chMap map[string]map[string]*fListedData
	zip   *zip.ReadCloser
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

		mode := f.FileInfo().Mode()

		parts := strings.Split(path, string(os.PathSeparator))

		prev := ""
		for ind, part := range parts {
			if _, ok := chMap[prev]; !ok {
				chMap[prev] = make(map[string]*fListedData)
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
					ino:      curIno,
					fileData: fileData,
				}
				curIno += 1
				chMap[prev][part] = d
			}
			prev = fullPath
		}

	}
	return &proccessedZip{chMap: chMap, zip: zr}

}

type zipGetter struct {
	cache *expirable.LRU[string, *proccessedZip]
}

type IZipGetter interface {
	GetZip(path string, stripPrefix string, inoStart uint64) (*proccessedZip, error)
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

func CreateZipGetter() IZipGetter {
	lru := expirable.NewLRU[string, *proccessedZip](30, func(k string, v *proccessedZip) {
		v.zip.Close()
	}, time.Second*20)

	zg := &zipGetter{cache: lru}

	return zg
}