package zip

import (
	"context"
	"fmt"
	"syscall"

	"github.com/hanwen/go-fuse/v2/fs"
	"github.com/hanwen/go-fuse/v2/fuse"
)

type mutableNode struct {
	fs.Inode
}

var _ = (writable)((*mutableNode)(nil))

type writable interface {
	fs.NodeRenamer
	fs.NodeRmdirer
	fs.NodeMkdirer
	fs.NodeUnlinker
	fs.NodeSetattrer
	fs.NodeCreater
}

func (this *mutableNode) onMutate() {

}

func (this *mutableNode) Setattr(ctx context.Context, file fs.FileHandle, in *fuse.SetAttrIn, out *fuse.AttrOut) syscall.Errno {
	this.onMutate()
	return 0
}

func (this *mutableNode) Rename(ctx context.Context, name string, newParent fs.InodeEmbedder, newName string, flags uint32) syscall.Errno {
	this.onMutate()
	return 0
}

func (this *mutableNode) Unlink(ctx context.Context, name string) syscall.Errno {
	this.onMutate()
	return 0
}

func (this *mutableNode) Create(ctx context.Context, name string, flags uint32, mode uint32, out *fuse.EntryOut) (node *fs.Inode, fh fs.FileHandle, fuseFlags uint32, errno syscall.Errno) {
	this.onMutate()
	fmt.Println("create")
	file := this.NewPersistentInode(ctx, &fs.MemRegularFile{}, fs.StableAttr{}) //todo owner
	return file, nil, 0, 0                                                      //check flags
}

func (this *mutableNode) Mkdir(ctx context.Context, name string, mode uint32, out *fuse.EntryOut) (*fs.Inode, syscall.Errno) {
	this.onMutate()
	fmt.Println("mkdir")
	return this.NewPersistentInode(ctx, &mutableNode{}, fs.StableAttr{Mode: fuse.S_IFDIR}), 0
}

func (this *mutableNode) Rmdir(ctx context.Context, name string) syscall.Errno {
	this.onMutate()
	fmt.Println("rm dir")
	return 0
}
