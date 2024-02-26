// Copyright 2016 the Go-FUSE Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"archive/zip"
	"context"
	"fmt"
	"io/ioutil"
	"path/filepath"
	"strings"
	"sync"
	"syscall"

	"github.com/hanwen/go-fuse/v2/fs"
	"github.com/hanwen/go-fuse/v2/fuse"
)

type ZipRoot struct {
	zipDir
	stripPrefix    string
	zr             *zip.ReadCloser
	staticChildren map[string]bool //arr?
	zipIsOpened    bool
}

var _ = (fs.NodeLookuper)((*ZipRoot)(nil))
var _ = (fs.NodeReaddirer)((*ZipRoot)(nil))

type zipDir struct {
	fs.Inode
}

var _ = (fs.NodeMkdirer)((*zipDir)(nil))
var _ = (fs.NodeCreater)((*zipDir)(nil))
var _ = (fs.NodeGetattrer)((*zipDir)(nil))

// var _ = (fs.NodeSetattrer)((*zipDir)(nil))
var _ = (fs.NodeRenamer)((*zipDir)(nil))

// func (f *zipDir) Setattr(ctx context.Context, file fs.FileHandle, in *fuse.SetAttrIn, out *fuse.AttrOut) syscall.Errno {
// 	return 0
// }

func (f *zipDir) Rename(ctx context.Context, name string, newParent fs.InodeEmbedder, newName string, flags uint32) syscall.Errno {
	// zr.openZip(ctx) rename somewhy opens zip
	return 0
}

func (f *zipDir) Create(ctx context.Context, name string, flags uint32, mode uint32, out *fuse.EntryOut) (node *fs.Inode, fh fs.FileHandle, fuseFlags uint32, errno syscall.Errno) {
	// zr.openZip(ctx) todo opens?
	fmt.Println("create")
	file := f.NewPersistentInode(ctx, &fs.MemRegularFile{}, fs.StableAttr{}) //todo owner
	return file, nil, 0, 0                                                   //check flags
}

func (f *zipDir) Mkdir(ctx context.Context, name string, mode uint32, out *fuse.EntryOut) (*fs.Inode, syscall.Errno) {
	// zr.openZip(ctx) todo also opens
	fmt.Println("mkdir")
	return f.NewPersistentInode(ctx, &zipDir{}, fs.StableAttr{Mode: fuse.S_IFDIR}), 0
}

func (r *zipDir) Getattr(ctx context.Context, fh fs.FileHandle, out *fuse.AttrOut) syscall.Errno {
	// out.Mode = 0755
	// out.Owner = fuse.Owner{Uid: uint32(os.Getuid()), Gid: uint32(os.Getgid())} // memo
	return 0
}

func (zr *ZipRoot) AddStaticChildren(name string) { //mb rewrite
	zr.staticChildren[name] = true
}

func (zr *ZipRoot) Lookup(ctx context.Context, name string, out *fuse.EntryOut) (*fs.Inode, syscall.Errno) {
	if !zr.staticChildren[name] {
		zr.openZip(ctx)
	}
	ch := zr.GetChild(name)
	if ch == nil {
		return nil, syscall.ENOENT
	}
	return ch, 0
}

func (zr *ZipRoot) Readdir(ctx context.Context) (fs.DirStream, syscall.Errno) {
	zr.openZip(ctx)

	lst := zr.Children()
	r := make([]fuse.DirEntry, 0, len(lst))
	for name, e := range lst {
		r = append(r, fuse.DirEntry{Mode: e.Mode(),
			Name: name,
			Ino:  e.StableAttr().Ino})
	}
	return fs.NewListDirStream(r), 0
}

func (zr *ZipRoot) openZip(ctx context.Context) {
	if zr.zipIsOpened {
		return
	}
	fmt.Println("openZip")
	for _, f := range zr.zr.File {

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
				ch = p.NewPersistentInode(ctx, &zipDir{},
					fs.StableAttr{Mode: fuse.S_IFDIR})
				p.AddChild(component, ch, true)
			}

			p = ch
		}
		if base != "" {
			ch := p.NewPersistentInode(ctx, &zipFile{file: f}, fs.StableAttr{})
			p.AddChild(base, ch, true)
		}
	}
	zr.zipIsOpened = true
}

// NewZipTree creates a new file-system for the zip file named name.
func NewZipTree(name string, stripPrefix string) (*ZipRoot, error) {
	r, err := zip.OpenReader(name)
	if err != nil {
		return nil, err
	}

	stripPrefix = filepath.Clean(stripPrefix)
	return &ZipRoot{zr: r, stripPrefix: stripPrefix, staticChildren: make(map[string]bool)}, nil
}

// zipFile is a file read from a zip archive.
type zipFile struct {
	fs.Inode
	file *zip.File

	mu   sync.Mutex
	data []byte
}

var _ = (fs.NodeOpener)((*zipFile)(nil))
var _ = (fs.NodeGetattrer)((*zipFile)(nil))

// var _ = (fs.NodeWriter)((*zipFile)(nil))
// var _ = (fs.NodeSetattrer)((*zipFile)(nil))

// Getattr sets the minimum, which is the size. A more full-featured
// FS would also set timestamps and permissions.
func (zf *zipFile) Getattr(ctx context.Context, f fs.FileHandle, out *fuse.AttrOut) syscall.Errno {
	out.Mode = uint32(zf.file.Mode()) & 07777
	// out.Owner = fuse.Owner{Uid: uint32(os.Getuid()), Gid: uint32(os.Getgid())} // memo
	out.Nlink = 1
	out.Mtime = uint64(zf.file.ModTime().Unix())
	out.Atime = out.Mtime
	out.Ctime = out.Mtime
	out.Size = zf.file.UncompressedSize64
	const bs = 512
	out.Blksize = bs
	out.Blocks = (out.Size + bs - 1) / bs
	return 0
}

// Open lazily unpacks zip data
func (zf *zipFile) Open(ctx context.Context, flags uint32) (fs.FileHandle, uint32, syscall.Errno) {
	zf.mu.Lock()
	defer zf.mu.Unlock()
	fmt.Println("open", flags)
	if flags == syscall.O_WRONLY {
		name, parent := zf.Parent()
		memFile := &fs.MemRegularFile{Data: zf.data}
		newFile := zf.NewPersistentInode(ctx, memFile, zf.StableAttr())
		fmt.Println("open created", newFile.StableAttr().Ino)
		parent.AddChild(name, newFile, true) //mb bad idea
		return nil, 0, 0
	}
	if zf.data == nil {
		rc, err := zf.file.Open()
		if err != nil {
			return nil, 0, syscall.EIO
		}
		content, err := ioutil.ReadAll(rc)
		if err != nil {
			return nil, 0, syscall.EIO
		}

		zf.data = content
	} //todo clean

	// We don't return a filehandle since we don't really need
	// one.  The file content is immutable, so hint the kernel to
	// cache the data.
	return nil, fuse.FOPEN_KEEP_CACHE, 0
}

// Read simply returns the data that was already unpacked in the Open call
func (zf *zipFile) Read(ctx context.Context, f fs.FileHandle, dest []byte, off int64) (fuse.ReadResult, syscall.Errno) {
	end := int(off) + len(dest)
	if end > len(zf.data) {
		end = len(zf.data)
	}
	return fuse.ReadResultData(zf.data[off:end]), 0
}

func (f *zipFile) Write(ctx context.Context, fh fs.FileHandle, data []byte, off int64) (uint32, syscall.Errno) {
	f.mu.Lock()
	defer f.mu.Unlock()

	fmt.Print("zipfile write", f.Inode.StableAttr().Ino)
	// f.ForgetPersistent()
	// return memFile.Write(ctx, nil, data, off)
	// fmt.Print("write", data)
	// end := int64(len(data)) + off
	// if int64(len(f.Data)) < end {
	// 	n := make([]byte, end)
	// 	copy(n, f.Data)
	// 	f.Data = n
	// }

	// copy(f.Data[off:off+int64(len(data))], data)

	return uint32(len(data)), 0
}

func (f *zipFile) Setattr(ctx context.Context, file fs.FileHandle, in *fuse.SetAttrIn, out *fuse.AttrOut) syscall.Errno {
	f.mu.Lock()
	defer f.mu.Unlock()

	fmt.Print("should not be caleld", f.Inode.StableAttr().Ino)
	// if sz, ok := in.GetSize(); ok {
	// 	f.Data = f.Data[:sz]
	// }
	// out.Attr = f.Attr
	// out.Size = uint64(len(f.Data))
	return fs.OK
}

// var _ = (fs.NodeOnAdder)((*ZipRoot)(nil))
