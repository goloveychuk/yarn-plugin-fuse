// Copyright 2016 the Go-FUSE Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// This program is the analogon of libfuse's hello.c, a a program that
// exposes a single file "file.txt" in the root directory.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net/http"
	_ "net/http/pprof"
	"os"
	"os/exec"
	"os/signal"
	"runtime"
	"strings"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/hanwen/go-fuse/v2/fs"
	"github.com/hanwen/go-fuse/v2/fuse"
)

var zg *zipGetter

const INO_STEP = uint64(1_000_000_000)

var last_ino = uint64(0)

type DependencyRoot struct {
	fs.Inode
	LinkType string                     //SOFT HARD
	Children map[string]*DependencyRoot //mb should be ref
	Target   string
	inoStart uint64
}

var _ = (fs.NodeGetattrer)((*DependencyRoot)(nil))

// var _ = (fs.NodeOnAdder)((*DependencyRoot)(nil))
var _ = (fs.NodeLookuper)((*DependencyRoot)(nil))
var _ = (fs.NodeReaddirer)((*DependencyRoot)(nil))

// func addChildren(ctx context.Context, r *fs.Inode, children map[string]*DependencyRoot) {
// 	for name, dep := range children {
// 		if dep.LinkType == "SOFT" {
// 			if dep.Target == "" {
// 				log.Fatalf("Target is empty for %s", name)
// 			}
// 			ch := r.NewPersistentInode(ctx, &fs.MemSymlink{
// 				Data: []byte(dep.Target),
// 			}, fs.StableAttr{Mode: syscall.S_IFLNK})
// 			r.AddChild(name, ch, false)
// 		} else {
// 			if dep.Target == "" {
// 				ch := r.NewPersistentInode(ctx, dep, fs.StableAttr{Mode: fuse.S_IFDIR})
// 				r.AddChild(name, ch, false)
// 			} else {
// 				parts := strings.SplitN(dep.Target, ".zip/", 2)

// 				root, err := NewZipTree(parts[0]+".zip", parts[1])
// 				if err != nil {
// 					log.Fatal(err)
// 				}
// 				ch := r.NewPersistentInode(ctx, root,
// 					fs.StableAttr{Mode: fuse.S_IFDIR})
// 				r.AddChild(name, ch, false)
// 				addChildren(ctx, ch, dep.Children)
// 				for name := range dep.Children {
// 					root.AddStaticChildren(name)
// 				}
// 			}
// 		}
// 	}
// }

func (r *DependencyRoot) Readdir(ctx context.Context) (fs.DirStream, syscall.Errno) {
	lst := make([]fuse.DirEntry, 0, len(r.Children))
	for name, e := range r.Children {

		lst = append(lst, fuse.DirEntry{
			Mode: getMode(e),
			Name: name,
			Ino:  e.StableAttr().Ino, //0 if not inited, probably
		})
	}
	return fs.NewListDirStream(lst), 0
}

func getMode(dep *DependencyRoot) uint32 {
	if dep.LinkType == "SOFT" {
		return syscall.S_IFLNK
	} else {
		return fuse.S_IFDIR
	}
}

func (r *DependencyRoot) getInoStart() uint64 {
	if r.inoStart == 0 {
		newInoStart := atomic.AddUint64(&last_ino, INO_STEP)
		r.inoStart = newInoStart
	}

	return r.inoStart
}

func (r *DependencyRoot) Lookup(ctx context.Context, name string, out *fuse.EntryOut) (*fs.Inode, syscall.Errno) {
	dep, ok := r.Children[name]
	if !ok {
		return nil, syscall.ENOENT
	}
	mode := getMode(dep)
	ino := dep.getInoStart()
	if dep.LinkType == "SOFT" {
		if dep.Target == "" {
			log.Fatalf("Target is empty for %s", name)
		}
		ch := r.NewInode(ctx, &fs.MemSymlink{
			Data: []byte(dep.Target),
		}, fs.StableAttr{Mode: mode})
		return ch, 0
	} else {
		if dep.Target == "" {
			ch := r.NewInode(ctx, dep, fs.StableAttr{Mode: mode, Ino: ino}) //could be inited multiple times (if ops.embed().bridge != nil {return ops.embed() })
			return ch, 0
		} else {
			parts := strings.SplitN(dep.Target, ".zip/", 2)

			root, err := NewZipTree(zg, parts[0]+".zip", parts[1], ino+1) //carefull
			if err != nil {
				log.Fatal(err)
			}

			ch := r.NewInode(ctx, root,
				fs.StableAttr{Mode: mode, Ino: ino})
			// r.AddChild(name, ch, false)
			// addChildren(ctx, ch, dep.Children) //todo
			// for name := range dep.Children {
			// 	root.AddStaticChildren(name)
			// }
			return ch, 0
		}
	}

}

// func (r *DependencyRoot) OnAdd(ctx context.Context) {
// 	addChildren(ctx, &r.Inode, r.Children)
// }

func (r *DependencyRoot) Getattr(ctx context.Context, fh fs.FileHandle, out *fuse.AttrOut) syscall.Errno {
	// out.Mode = 0755
	// out.Owner = fuse.Owner{Uid: uint32(os.Getuid()), Gid: uint32(os.Getgid())} // memo
	return 0
}

type ToMount struct {
	path string
	root fs.InodeEmbedder
}

func isExists(path string) bool {
	_, err := os.Stat(path)
	if err == nil {
		return true
	}
	if os.IsNotExist(err) {
		return false
	}
	return false
}

type FuseData struct {
	Roots map[string]*DependencyRoot
}

func runGCInterval(interval time.Duration) {
	ticker := time.NewTicker(interval)
	go func() {
		for range ticker.C {

			runtime.GC()
			var m runtime.MemStats
			runtime.ReadMemStats(&m)
			fmt.Printf("Alloc = %v MiB\n", m.Alloc/1024/1024)
		}
	}()
}

func main() {

	debug := flag.Bool("debug", false, "print debug data")
	prof := flag.Bool("prof", false, "open profile server")

	flag.Parse()
	if len(flag.Args()) < 1 {
		log.Fatal("Usage:\n  hello MOUNTPOINT")
	}
	if *prof {
		// defer profile.Start(profile.CPUProfile, profile.ProfilePath(".")).Stop()
		go func() {
			http.ListenAndServe(":8080", nil)
		}()
	}
	configPath := flag.Arg(0)
	data, err := os.ReadFile(configPath)
	if err != nil {
		log.Fatal(err)
	}
	fuseData := &FuseData{}

	if err := json.Unmarshal(data, fuseData); err != nil {
		log.Fatal(err)
	}
	servers := make([]*fuse.Server, 0)

	toMount := make([]ToMount, 0)
	for mount, root := range fuseData.Roots {
		toMount = append(toMount, ToMount{mount, root})
	}
	// toMount = append(toMount, ToMount{"/tmp/dep", &ControlWrap{}})
	close := make(chan os.Signal, 10)

	zg = createZipGetter()
	for _, mount := range toMount {
		println("Mounting", mount.path)
		if isExists(mount.path) {
			println("Unmounting", mount.path)
			cmd := exec.Command("umount", mount.path)
			err := cmd.Run()
			if err != nil {
				// log.Fatal(err)
			}
		}
		opts := &fs.Options{UID: uint32(os.Getuid()), GID: uint32(os.Getgid())}

		timeout := 10 * time.Second
		timeout2 := 10 * time.Second
		// attrTimeout := 10 * time.Second
		// opts.AttrTimeout = &attrTimeout
		opts.EntryTimeout = &timeout
		opts.NegativeTimeout = &timeout2

		opts.MaxBackground = 30
		opts.Debug = *debug
		opts.Options = []string{"vm.vfs_cache_pressure=10"}

		if err != nil {
			log.Fatalf("Unmarshal fail: %v\n", err)
		}

		server, err := fs.Mount(mount.path, mount.root, opts)
		if err != nil {
			log.Fatalf("Mount fail: %v\n", err)
		}
		println("Mounted!", mount.path)
		servers = append(servers, server)

		// go func() {
		// 	err := server.WaitMount()
		// 	if err != nil {
		// 		log.Println(err)
		// 	}
		// 	println("here", mount.path, err)
		// 	close <- syscall.SIGTERM
		// }()
	}

	cleanup := func() {
		for _, server := range servers {
			println("unmounting\n", server)
			server.Unmount()
		}

		os.Exit(1)
	}

	go func() {
		<-close
		cleanup()
	}()

	signal.Notify(close, os.Interrupt, syscall.SIGTERM, syscall.SIGINT)
	runGCInterval(5 * time.Second)
	select {}
}