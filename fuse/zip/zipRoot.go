// Copyright 2016 the Go-FUSE Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package zip

import (
	"path/filepath"
)

type ZipRoot struct {
	stripPrefix string
	zipPath     string
	inoStart    uint64
	zipGetter   IZipGetter
}

func (this *ZipRoot) GetZip() (*proccessedZip, error) {
	return this.zipGetter.GetZip(this.zipPath, this.stripPrefix)
	// if this.zip == nil {
	// 	zip, err := this._zipGetter.GetZip(this.zipPath, this.stripPrefix, this.inoStart)
	// 	if err != nil {
	// 		return nil, syscall.ENOENT
	// 	}
	// 	go func() {
	// 		tick := time.Tick(time.Second * 10)
	// 		for {
	// 			<-tick
	// 			if true {
	// 				this.zip.zip.Close()
	// 				this.zip = nil
	// 				break
	// 			}
	// 		}
	// 	}()

	// 	this.zip = zip
	// }
	// return this.zip, nil
}

// func (zr *ZipRoot) AddStaticChildren(name string) { //mb rewrite
// 	zr.staticChildren[name] = true
// }

// func (zr *ZipRoot) Readdir(ctx context.Context) (fs.DirStream, syscall.Errno) {
// 	zr.openZip(ctx)

// 	if strings.Contains(zr.zipPath, "/yup") {
// 		fmt.Println(zr.Children())
// 	}
// 	lst := zr.Children()
// 	r := make([]fuse.DirEntry, 0, len(lst))
// 	for name, e := range lst {
// 		r = append(r, fuse.DirEntry{Mode: e.Mode(),
// 			Name: name,
// 			Ino:  e.StableAttr().Ino})
// 	}
// 	go func() {
// 		<-time.After(time.Second * 10)
// 		zr.RmAllChildren()
// 	}()
// 	return fs.NewListDirStream(r), 0
// }

// func (zr *ZipRoot) openZip(ctx context.Context) {
// 	if zr.zipIsOpened {
// 		return
// 	}

// 	zr.zipIsOpened = true
// }

// NewZipTree creates a new file-system for the zip file named name.
func NewZipTree(zipGetter IZipGetter, name string, stripPrefix string, inoStart uint64) (*ZipRoot, error) {
	stripPrefix = filepath.Clean(stripPrefix)
	root := &ZipRoot{zipPath: name, stripPrefix: stripPrefix, inoStart: inoStart, zipGetter: zipGetter}
	return root, nil
}
