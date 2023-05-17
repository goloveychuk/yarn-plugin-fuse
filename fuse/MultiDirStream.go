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
	newStreams := make([]fs.DirStream, 0, len(this.streams))
	for _, stream := range this.streams {
		if stream.HasNext() {
			newStreams = append(newStreams, stream)
		}
	}
	this.streams = newStreams
	return len(newStreams) > 0
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
