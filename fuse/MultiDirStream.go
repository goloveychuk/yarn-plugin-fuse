package main

import (
	"syscall"

	"github.com/hanwen/go-fuse/v2/fs"
	"github.com/hanwen/go-fuse/v2/fuse"
)

type MultiDirStream struct {
	streams []fs.DirStream
}

func (this *MultiDirStream) HasNext() bool {
	for ind, stream := range this.streams {
		if stream.HasNext() {
			return true
		} else {
			this.streams = append(this.streams[:ind], this.streams[ind+1:]...)
			stream.Close()
		}
	}
	return false
}

func (this *MultiDirStream) Next() (fuse.DirEntry, syscall.Errno) {
	return this.streams[0].Next()
}

func (this *MultiDirStream) Close() {

}

var _ = (fs.DirStream)((*MultiDirStream)(nil))

func NewMultiDirStream(streams ...fs.DirStream) fs.DirStream {
	return &MultiDirStream{streams}
}
