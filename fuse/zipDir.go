package main

import (
	"context"
	"path"
	"syscall"

	"github.com/hanwen/go-fuse/v2/fs"
	"github.com/hanwen/go-fuse/v2/fuse"
)

type zipDir struct {
	mutableNode
	root *ZipRoot
	path string
}

var _ = (fs.NodeGetattrer)((*zipDir)(nil))
var _ = (fs.NodeLookuper)((*zipDir)(nil))
var _ = (fs.NodeReaddirer)((*zipDir)(nil))

func (zr *zipDir) Lookup(ctx context.Context, name string, out *fuse.EntryOut) (*fs.Inode, syscall.Errno) {

	zip, err := zr.root.GetZip()
	if err != nil {
		return nil, syscall.ENOENT
	}

	if ch, ok := zip.chMap[zr.path]; ok {
		d, ok := ch[name]
		if !ok {
			return nil, syscall.ENOENT
		}
		fullPath := path.Join(zr.path, name)
		if d.fileData == nil {
			ch := zr.NewInode(ctx, newZipDir(zr.root, fullPath), fs.StableAttr{Mode: fuse.S_IFDIR, Ino: d.ino})
			return ch, 0
		}
		ch := zr.NewInode(ctx, &zipFile{fileData: d.fileData}, fs.StableAttr{Mode: fuse.S_IFREG, Ino: d.ino})
		return ch, 0
	}

	return nil, syscall.ENOENT

}

func (r *zipDir) Readdir(ctx context.Context) (fs.DirStream, syscall.Errno) {
	zip, err := r.root.GetZip()
	if err != nil {
		return nil, syscall.ENOENT
	}
	if ch, ok := zip.chMap[r.path]; ok {
		lst := make([]fuse.DirEntry, len(ch))
		ind := 0
		for name, d := range ch {
			var mode uint32
			if d.fileData == nil {
				mode = fuse.S_IFDIR
			} else {
				mode = fuse.S_IFREG
			}
			lst[ind] = fuse.DirEntry{
				Mode: mode,
				Name: name,
				Ino:  d.ino,
			}
			ind += 1
		}
		return fs.NewListDirStream(lst), 0
	}
	return nil, syscall.ENOENT

}

func (r *zipDir) Getattr(ctx context.Context, fh fs.FileHandle, out *fuse.AttrOut) syscall.Errno {
	// out.Mode = 0755
	// out.Owner = fuse.Owner{Uid: uint32(os.Getuid()), Gid: uint32(os.Getgid())} // memo
	return 0
}

func newZipDir(root *ZipRoot, path string) *zipDir {
	return &zipDir{
		root: root,
		path: path,
	}
}
