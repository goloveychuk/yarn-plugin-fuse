package zip

import (
	"context"
	"path"
	"syscall"

	"github.com/hanwen/go-fuse/v2/fs"
	"github.com/hanwen/go-fuse/v2/fuse"
)

var last_gen = uint64(0)

type ZipDir struct {
	// mutableNode
	fs.Inode
	root *ZipRoot
	path string
}

var _ = (fs.NodeGetattrer)((*ZipDir)(nil))
var _ = (fs.NodeLookuper)((*ZipDir)(nil))
var _ = (fs.NodeReaddirer)((*ZipDir)(nil))

func (zr *ZipDir) Lookup(ctx context.Context, name string, out *fuse.EntryOut) (*fs.Inode, syscall.Errno) {

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
		// newGen := atomic.AddUint64(&last_gen, 1)
		ino := zr.root.inoStart + d.index
		out.Ino = ino

		if d.fileData == nil {
			zipDir := NewZipDir(zr.root, fullPath)
			ch := zr.NewInode(ctx, &zipDir, fs.StableAttr{
				Mode: fuse.S_IFDIR,
				Ino:  ino,
				// Gen:  newGen,
			})
			return ch, 0
		}
		out.Attr = d.fileData.attr
		ch := zr.NewInode(ctx, &zipFile{fileData: d.fileData}, fs.StableAttr{
			Mode: fuse.S_IFREG,
			Ino:  ino,
			// Gen:  newGen,
		})
		return ch, 0
	}

	return nil, syscall.ENOENT

}

func (r *ZipDir) Readdir(ctx context.Context) (fs.DirStream, syscall.Errno) {
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
				Ino:  d.index + r.root.inoStart,
			}
			ind += 1
		}
		return fs.NewListDirStream(lst), 0
	}
	return nil, syscall.ENOENT

}

func (r *ZipDir) Getattr(ctx context.Context, fh fs.FileHandle, out *fuse.AttrOut) syscall.Errno {
	// out.Mode = 0755
	// out.Owner = fuse.Owner{Uid: uint32(os.Getuid()), Gid: uint32(os.Getgid())} // memo
	return 0
}

func NewZipDir(root *ZipRoot, path string) ZipDir {
	return ZipDir{
		root: root,
		path: path,
	}
}
