// Copyright 2019 the Go-FUSE Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"syscall"

	"github.com/hanwen/go-fuse/v2/fs"
	"github.com/hanwen/go-fuse/v2/fuse"
)

// Control is a filesystem node that holds a read-only data
// slice in memory.
type Control struct {
	fs.Inode
}

type Data struct {
	Pid int
}

func (f *controlFh) Read(ctx context.Context, dest []byte, off int64) (fuse.ReadResult, syscall.Errno) {
	data := f.data
	end := int(off) + len(dest)
	if end > len(data) {
		end = len(data)
	}
	return fuse.ReadResultData(data[off:end]), fs.OK
}

var _ = (fs.FileReader)((*controlFh)(nil))
var _ = (fs.FileGetattrer)((*controlFh)(nil))
var _ = (fs.FileFlusher)((*controlFh)(nil))

var _ = (fs.NodeOpener)((*Control)(nil))
var _ = (fs.NodeGetattrer)((*Control)(nil))

type controlFh struct {
	data []byte
}

func (f *controlFh) Getattr(ctx context.Context, out *fuse.AttrOut) syscall.Errno {
	out.Size = uint64(len(f.data))
	return fs.OK
}

func (f *controlFh) Flush(ctx context.Context) syscall.Errno {
	fmt.Println("flush")
	return 0
}

func (f *Control) Open(ctx context.Context, flags uint32) (fh fs.FileHandle, fuseFlags uint32, errno syscall.Errno) {
	if flags == syscall.O_RDONLY {
		d := Data{Pid: os.Getpid()}
		data, err := json.Marshal(d)
		if err != nil {
			log.Fatalf("json.Marshal: %v", err)
		}
		fh := &controlFh{data: data}
		return fh, fuse.FOPEN_DIRECT_IO, fs.OK
	}
	return nil, fuse.FOPEN_DIRECT_IO, fs.OK
}

func (f *Control) Getattr(ctx context.Context, fh fs.FileHandle, out *fuse.AttrOut) syscall.Errno {
	return fs.OK
}

type ControlWrap struct {
	fs.Inode
}

func (f *ControlWrap) Getattr(ctx context.Context, fh fs.FileHandle, out *fuse.AttrOut) syscall.Errno {
	return fs.OK
}

var _ = (fs.NodeOnAdder)((*ControlWrap)(nil))

var _ = (fs.NodeGetattrer)((*ControlWrap)(nil))

func (r *ControlWrap) OnAdd(ctx context.Context) {
	ch := r.NewPersistentInode(ctx, &Control{}, fs.StableAttr{})
	r.AddChild("control", ch, false)
}
