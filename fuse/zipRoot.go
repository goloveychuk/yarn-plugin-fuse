// Copyright 2016 the Go-FUSE Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"archive/zip"
	"context"
	"fmt"
	"log"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/hanwen/go-fuse/v2/fs"
	"github.com/hanwen/go-fuse/v2/fuse"
)

type ZipRoot struct {
	zipDir
	stripPrefix string
	// zr             *zip.ReadCloser
	zipPath string
	// staticChildren map[string]bool //arr?
	zipIsOpened bool
}

// var _ = (fs.NodeLookuper)((*ZipRoot)(nil))
var _ = (fs.NodeReaddirer)((*ZipRoot)(nil))

// func (zr *ZipRoot) AddStaticChildren(name string) { //mb rewrite
// 	zr.staticChildren[name] = true
// }

func (zr *ZipRoot) Readdir(ctx context.Context) (fs.DirStream, syscall.Errno) {
	zr.openZip(ctx)

	if strings.Contains(zr.zipPath, "/yup") {
		fmt.Println(zr.Children())
	}
	lst := zr.Children()
	r := make([]fuse.DirEntry, 0, len(lst))
	for name, e := range lst {
		r = append(r, fuse.DirEntry{Mode: e.Mode(),
			Name: name,
			Ino:  e.StableAttr().Ino})
	}
	go func() {
		<-time.After(time.Second * 10)
		zr.RmAllChildren()
	}()
	return fs.NewListDirStream(r), 0
}

func (zr *ZipRoot) openZip(ctx context.Context) {
	if zr.zipIsOpened {
		return
	}
	// ticker := time.NewTicker(time.Second * 5)
	// go func() {
	// 	for range ticker.C {
	// 		if zr.Forgotten() {
	// 			fmt.Println("forgotten", zr.zipPath)
	// 		}
	// 		// runtime.GC()
	// 	}
	// }()
	// fmt.Println("openZip")
	r, err := zip.OpenReader(zr.zipPath)
	if err != nil {
		log.Fatalf("zip.Open(%q) failed: %v", zr.zipPath, err) //todo
	}

	// zr.zr = r
	for _, f := range r.File {

		cleaned := filepath.Clean(f.Name)
		if !strings.HasPrefix(cleaned, zr.stripPrefix) {
			continue
		}

		var dir string
		var base string

		if f.FileInfo().IsDir() {
			dir = cleaned
		} else {
			dir, base = filepath.Split(cleaned)
		}
		dir = strings.TrimPrefix(dir, zr.stripPrefix)

		p := &zr.Inode
		for _, component := range strings.Split(dir, "/") {
			if len(component) == 0 {
				continue
			}
			ch := p.GetChild(component)
			if ch == nil {
				ch = p.NewInode(ctx, NewZipDir(),
					fs.StableAttr{Mode: fuse.S_IFDIR})
				p.AddChild(component, ch, true)
			}
			p = ch
		}
		if base != "" {
			zf := &zipFile{attr: getZFAttrs(f)}
			ch := p.NewInode(ctx, zf, fs.StableAttr{})

			p.AddChild(base, ch, true)
		}
	}
	r.Close()
	// go func() {
	// 	timer := time.NewTimer(time.Second * 5)

	// 	for range timer.C {
	// 		for name, ch := range zr.Children() {
	// 			if ch.Forgotten() {
	// 				fmt.Println("forgotten", zr.zipPath, name)
	// 				return
	// 			}
	// 		}
	// 		timer.Reset(time.Second * 5)
	// 	}
	// }()
	zr.zipIsOpened = true
}

// NewZipTree creates a new file-system for the zip file named name.
func NewZipTree(name string, stripPrefix string) (*ZipRoot, error) {
	stripPrefix = filepath.Clean(stripPrefix)
	return &ZipRoot{zipPath: name, stripPrefix: stripPrefix}, nil
}
