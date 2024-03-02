package main

import (
	"archive/zip"
	"context"
	"fmt"
	"io/ioutil"
	"sync"
	"syscall"

	"github.com/hanwen/go-fuse/v2/fs"
	"github.com/hanwen/go-fuse/v2/fuse"
)

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
