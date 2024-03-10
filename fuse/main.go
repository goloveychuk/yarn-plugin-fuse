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
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/hanwen/go-fuse/v2/fs"
	"github.com/hanwen/go-fuse/v2/fuse"
	"github.com/pkg/profile"
)

var ZIP_GETTER *zipGetter

const INO_STEP = uint64(1_000_000_000)

var last_ino = uint64(0)
var last_gen = uint64(0)

var inoCache *inoCacheStr

type inoGen struct {
	ino uint64
	gen uint64
}
type inoCacheStr struct {
	m  map[string]inoGen
	mu sync.Mutex
}

type depJson struct {
	LinkType  string                    //SOFT HARD
	Children2 map[string]dependencyRoot `json:"Children"`
	Target    string
}

type dependencyRoot struct {
	depJson
	inoGen inoGen
}

type dependencyNode struct {
	dependencyRoot
	zipDir
}

func copyDepRoot(orig dependencyRoot) dependencyRoot {
	copy := orig
	copy.Children2 = make(map[string]dependencyRoot)
	for k, v := range orig.Children2 {
		copy.Children2[k] = v
	}
	return copy
}

var _ = (fs.NodeGetattrer)((*dependencyNode)(nil))

// var _ = (fs.NodeOnAdder)((*dependencyNode)(nil))
var _ = (fs.NodeLookuper)((*dependencyNode)(nil))
var _ = (fs.NodeReaddirer)((*dependencyNode)(nil))

func (this *dependencyRoot) UnmarshalJSON(b []byte) error {
	res := &depJson{}
	if err := json.Unmarshal(b, res); err != nil {
		return err
	}
	this.LinkType = res.LinkType
	this.Children2 = res.Children2
	this.Target = res.Target
	this.inoGen = getInoStart(res.LinkType, res.Target)
	// fmt.Println("inoStart", this.inoStart, this.Target)
	return nil
}

// func addChildren(ctx context.Context, r *fs.Inode, children map[string]*dependencyNode) {
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

func (r *dependencyNode) Readdir(ctx context.Context) (fs.DirStream, syscall.Errno) {
	lst := make([]fuse.DirEntry, 0, len(r.Children2))
	for name, ch := range r.Children2 {
		lst = append(lst, fuse.DirEntry{
			Mode: getMode(&ch),
			Name: name,
			Ino:  ch.inoGen.ino,
		})
	}
	if r.Target != "" && r.LinkType == "HARD" {
		zipList, err := r.zipDir.Readdir(ctx)
		if err != 0 {
			return zipList, err
		}
		return NewMultiDirStream(fs.NewListDirStream(lst), zipList), 0
	}
	return fs.NewListDirStream(lst), 0
}

func getMode(dep *dependencyRoot) uint32 {
	if dep.LinkType == "SOFT" {
		return syscall.S_IFLNK
	} else {
		return fuse.S_IFDIR
	}
}

func getInoStart(linkType string, target string) inoGen {

	// breaks find node_modules -type f | wc -l
	if linkType == "HARD" && target != "" {
		inoCache.mu.Lock()
		defer inoCache.mu.Unlock()
		if ino, ok := inoCache.m[target]; ok {
			ino.gen++
			inoCache.m[target] = ino
			// fmt.Print("cached", target, ino, "\n")
			return ino
			// fmt.Print("cached", r.Target, ino, "\n")
		} else {
			newInoStart := atomic.AddUint64(&last_ino, INO_STEP)
			inoGen := inoGen{ino: newInoStart, gen: 1}
			inoCache.m[target] = inoGen
			return inoGen
		}
	} else {
		newInoStart := atomic.AddUint64(&last_ino, INO_STEP)
		return inoGen{ino: newInoStart, gen: 1}

	}
	// r.inoStart = newInoStart
	// }

}
func (r *dependencyNode) Lookup(ctx context.Context, name string, out *fuse.EntryOut) (*fs.Inode, syscall.Errno) {
	ch, attr := r.dependencyRoot.GetChild(ctx, name, out)
	if ch == nil {
		if r.Target != "" && r.LinkType == "HARD" {
			return r.zipDir.Lookup(ctx, name, out)
		}
		return nil, syscall.ENOENT
	}
	// out.Ino = attr.Ino
	return r.NewInode(ctx, ch, attr), 0
}

func (r *dependencyRoot) GetChild(ctx context.Context, name string, out *fuse.EntryOut) (fs.InodeEmbedder, fs.StableAttr) {
	dep, ok := r.Children2[name]
	if !ok {
		// fmt.Println("GetChild", name, ok, r.Children2, r.Target, r.inoStart)
		return nil, fs.StableAttr{}
	}
	ino := dep.inoGen.ino
	attr := fs.StableAttr{Mode: getMode(&dep), Ino: ino, Gen: dep.inoGen.gen}

	if dep.LinkType == "SOFT" {
		if dep.Target == "" {
			log.Fatalf("Target is empty for %s", name)
		}
		ch := &fs.MemSymlink{
			Data: []byte(dep.Target),
		}
		return ch, attr
	} else {
		if dep.Target == "" {
			return &dependencyNode{dependencyRoot: copyDepRoot(dep)}, attr
		} else {
			parts := strings.SplitN(dep.Target, ".zip/", 2)
			root, err := NewZipTree(parts[0]+".zip", parts[1], ino+1)
			if err != nil {
				log.Fatal(err)
			}
			// fmt.Println(dep.Target, ino+1, "ino")
			rootNode := &dependencyNode{dependencyRoot: copyDepRoot(dep), zipDir: zipDir{
				root: root,
				path: "",
			}}

			return rootNode, attr
			// r.AddChild(name, ch, false)
			// addChildren(ctx, ch, dep.Children) //todo
			// for name := range dep.Children {
			// 	root.AddStaticChildren(name)
			// }
		}
	}

}

// func (r *dependencyNode) OnAdd(ctx context.Context) {
// 	addChildren(ctx, &r.Inode, r.Children)
// }

func (r *dependencyNode) Getattr(ctx context.Context, fh fs.FileHandle, out *fuse.AttrOut) syscall.Errno {
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
	Roots map[string]dependencyRoot
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
		defer profile.Start(profile.CPUProfile, profile.ProfilePath(".")).Stop()
		defer profile.Start(profile.MemProfile).Stop()
		go func() {
			http.ListenAndServe(":8080", nil)
		}()
	}
	configPath := flag.Arg(0)
	data, err := os.ReadFile(configPath)
	if err != nil {
		log.Fatal(err)
	}
	inoCache = &inoCacheStr{m: make(map[string]inoGen)}

	fuseData := &FuseData{}

	if err := json.Unmarshal(data, fuseData); err != nil {
		log.Fatal(err)
	}
	// fmt.Println(fuseData.Roots)
	servers := make([]*fuse.Server, 0)

	toMount := make([]ToMount, 0)
	for mount, root := range fuseData.Roots {
		node := &dependencyNode{dependencyRoot: root}

		toMount = append(toMount, ToMount{mount, node})
	}
	// loopback, err := fs.NewLoopbackRoot("/Users/vadymh/work/thunderbolt")
	// toMount = append(toMount, ToMount{"/Users/vadymh/work/thunderbolt2/test", loopback})
	close := make(chan os.Signal, 10)
	ZIP_GETTER = createZipGetter()
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

		timeout := 10 * time.Minute
		// timeout2 := 10 * time.Second
		attrTimeout := 10 * time.Minute
		opts.AttrTimeout = &attrTimeout
		opts.EntryTimeout = &timeout
		opts.DirectMount = true
		// opts.NegativeTimeout = &timeout2

		// opts.MaxBackground = 30
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
