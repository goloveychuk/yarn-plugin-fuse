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
	"sync"
	"syscall"

	"github.com/hanwen/go-fuse/v2/fs"
	"github.com/hanwen/go-fuse/v2/fuse"
)

// Control is a filesystem node that holds a read-only data
// slice in memory.
type Control struct {
	fs.Inode

	mu sync.Mutex
	// Data []byte
	// Attr fuse.Attr
}

var _ = (fs.NodeOpener)((*Control)(nil))
var _ = (fs.NodeReader)((*Control)(nil))
var _ = (fs.NodeWriter)((*Control)(nil))
var _ = (fs.NodeFlusher)((*Control)(nil))
var _ = (fs.NodeGetattrer)((*Control)(nil))
var _ = (fs.NodeSetattrer)((*Control)(nil))

func (f *Control) Open(ctx context.Context, flags uint32) (fh fs.FileHandle, fuseFlags uint32, errno syscall.Errno) {
	fmt.Println("open")
	return nil, fuse.FOPEN_DIRECT_IO, fs.OK
}

func (f *Control) Write(ctx context.Context, fh fs.FileHandle, data []byte, off int64) (uint32, syscall.Errno) {
	fmt.Println("write", data)
	f.mu.Lock()
	defer f.mu.Unlock()
	// end := int64(len(data)) + off
	// if int64(len(f.Data)) < end {
	// 	n := make([]byte, end)
	// 	copy(n, f.Data)
	// 	f.Data = n
	// }

	// copy(f.Data[off:off+int64(len(data))], data)

	return uint32(len(data)), 0
}

func (f *Control) Setattr(ctx context.Context, fh fs.FileHandle, in *fuse.SetAttrIn, out *fuse.AttrOut) syscall.Errno {
	fmt.Println("setattr")
	return 0
}

func (f *Control) Getattr(ctx context.Context, fh fs.FileHandle, out *fuse.AttrOut) syscall.Errno {
	f.mu.Lock()
	defer f.mu.Unlock()
	// out.Mode = 0755
	out.Owner = fuse.Owner{Uid: uint32(os.Getuid()), Gid: uint32(os.Getgid())}
	out.Size = 100
	return fs.OK
}

func (f *Control) Flush(ctx context.Context, fh fs.FileHandle) syscall.Errno {
	fmt.Println("flush")
	return 0
}

type Data struct {
	Pid int
}

func (f *Control) Read(ctx context.Context, fh fs.FileHandle, dest []byte, off int64) (fuse.ReadResult, syscall.Errno) {
	log.Println("called")
	f.mu.Lock()
	defer f.mu.Unlock()
	d := Data{Pid: os.Getpid()}
	data, err := json.Marshal(d)
	if err != nil {
		log.Fatalf("json.Marshal: %v", err)
	}
	end := int(off) + len(dest)
	if end > len(data) {
		end = len(data)
	}
	return fuse.ReadResultData(data[off:end]), fs.OK
}

type ControlWrap struct {
	fs.Inode

	// Data []byte
	// Attr fuse.Attr
}

func (f *ControlWrap) Getattr(ctx context.Context, fh fs.FileHandle, out *fuse.AttrOut) syscall.Errno {
	// f.mu.Lock()
	// defer f.mu.Unlock()
	// out.Mode = 0755
	// out.Owner = fuse.Owner{Uid: uint32(os.Getuid()), Gid: uint32(os.Getgid())}
	out.Size = 100
	return fs.OK
}

var _ = (fs.NodeOnAdder)((*ControlWrap)(nil))

var _ = (fs.NodeGetattrer)((*ControlWrap)(nil))

func (r *ControlWrap) OnAdd(ctx context.Context) {
	ch := r.NewPersistentInode(ctx, &Control{}, fs.StableAttr{})
	r.AddChild("control", ch, false)
}
