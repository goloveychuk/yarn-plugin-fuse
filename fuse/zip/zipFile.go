package zip

import (
	"context"
	"syscall"

	"github.com/hanwen/go-fuse/v2/fs"
	"github.com/hanwen/go-fuse/v2/fuse"
)

type zipFile struct {
	fs.Inode
	fileData *zipFileData
}

var _ = (fs.NodeOpener)((*zipFile)(nil))
var _ = (fs.NodeGetattrer)((*zipFile)(nil))
var _ = (fs.NodeReader)((*zipFile)(nil))

type readFileHandle struct {
	data []byte
}

func (zf *zipFile) Getattr(ctx context.Context, f fs.FileHandle, out *fuse.AttrOut) syscall.Errno {
	out.Attr = zf.fileData.attr
	return 0
}

// Open lazily unpacks zip data
func (zf *zipFile) Open(ctx context.Context, flags uint32) (fs.FileHandle, uint32, syscall.Errno) {
	if flags == syscall.O_WRONLY {
		return nil, 0, 0
	}

	data, err := zf.fileData.ReadFile()
	if err != 0 {
		return nil, 0, err
	}
	return &readFileHandle{
		data: data,
	}, fuse.FOPEN_KEEP_CACHE, 0
}

// Read simply returns the data that was already unpacked in the Open call
func (zf *zipFile) Read(ctx context.Context, _fh fs.FileHandle, dest []byte, off int64) (fuse.ReadResult, syscall.Errno) {
	end := int(off) + len(dest)
	fh := _fh.(*readFileHandle)
	if end > len(fh.data) {
		end = len(fh.data)
	}
	return fuse.ReadResultData(fh.data[off:end]), 0
}
