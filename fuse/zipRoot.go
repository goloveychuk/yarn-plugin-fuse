// Copyright 2016 the Go-FUSE Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"path/filepath"
)

type ZipRoot struct {
	zipDir
	zipGetter   *zipGetter
	stripPrefix string
	// zr             *zip.ReadCloser
	zipPath  string
	inoStart uint64
	// staticChildren map[string]bool //arr?
	// zipIsOpened bool
}

// var _ = (fs.NodeLookuper)((*ZipRoot)(nil))
// var _ = (fs.NodeReaddirer)((*ZipRoot)(nil))

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
func NewZipTree(zipGetter *zipGetter, name string, stripPrefix string, inoStart uint64) (*ZipRoot, error) {
	stripPrefix = filepath.Clean(stripPrefix)
	root := &ZipRoot{zipGetter: zipGetter, zipPath: name, stripPrefix: stripPrefix, inoStart: inoStart}
	root.zipDir = *newZipDir(root, "")
	return root, nil
}
