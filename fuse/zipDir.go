package main

import (
	"context"
	"syscall"

	"github.com/hanwen/go-fuse/v2/fs"
	"github.com/hanwen/go-fuse/v2/fuse"
)

type zipDir struct {
	fs.Inode
	// children map[string]fs.InodeEmbedder
}

var _ = (fs.NodeGetattrer)((*zipDir)(nil))

// var _ = (fs.NodeLookuper)((*zipDir)(nil))
// var _ = (fs.NodeReaddirer)((*zipDir)(nil))

// var _ = (fs.NodeSetattrer)((*zipDir)(nil))
// var _ = (fs.NodeRenamer)((*zipDir)(nil))

// func (zr *zipDir) Lookup(ctx context.Context, name string, out *fuse.EntryOut) (*fs.Inode, syscall.Errno) {
// 	// if !zr.staticChildren[name] {
// 	// 	zr.openZip(ctx)
// 	// }
// 	ch := zr.GetChild(name)
// 	if ch == nil {
// 		return nil, syscall.ENOENT
// 	}
// 	return ch, 0
// }

// func (r *zipDir) Readdir(ctx context.Context) (fs.DirStream, syscall.Errno) {
// 	lst := make([]fuse.DirEntry, 0, len(r.children))
// 	for name, e := range r.children {
// 		lst = append(lst, fuse.DirEntry{
// 			Mode: e.EmbeddedInode().Mode(),
// 			Name: name,
// 			Ino:  e.EmbeddedInode().StableAttr().Ino,
// 		})
// 	}
// 	return fs.NewListDirStream(lst), 0
// }

func (r *zipDir) Getattr(ctx context.Context, fh fs.FileHandle, out *fuse.AttrOut) syscall.Errno {
	// out.Mode = 0755
	// out.Owner = fuse.Owner{Uid: uint32(os.Getuid()), Gid: uint32(os.Getgid())} // memo
	return 0
}

func NewZipDir() *zipDir {
	return &zipDir{
		// children: make(map[string]fs.InodeEmbedder)
	}
}

// func (f *zipDir) Setattr(ctx context.Context, file fs.FileHandle, in *fuse.SetAttrIn, out *fuse.AttrOut) syscall.Errno {
// 	return 0
// }

// func (f *zipDir) Rename(ctx context.Context, name string, newParent fs.InodeEmbedder, newName string, flags uint32) syscall.Errno {
// 	// zr.openZip(ctx) rename somewhy opens zip
// 	return 0
// }

// func (f *zipDir) Create(ctx context.Context, name string, flags uint32, mode uint32, out *fuse.EntryOut) (node *fs.Inode, fh fs.FileHandle, fuseFlags uint32, errno syscall.Errno) {
// 	// zr.openZip(ctx) todo opens?
// 	fmt.Println("create")
// 	file := f.NewPersistentInode(ctx, &fs.MemRegularFile{}, fs.StableAttr{}) //todo owner
// 	return file, nil, 0, 0                                                   //check flags
// }

// func (f *zipDir) Mkdir(ctx context.Context, name string, mode uint32, out *fuse.EntryOut) (*fs.Inode, syscall.Errno) {
// 	// zr.openZip(ctx) todo also opens
// 	fmt.Println("mkdir")
// 	return f.NewPersistentInode(ctx, &zipDir{}, fs.StableAttr{Mode: fuse.S_IFDIR}), 0
// }
