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
	"goloveychuk/yarn-fuse/zip"
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

var ZIP_GETTER zip.IZipGetter

const INO_STEP = uint64(1_000_000_000)

var last_ino = uint64(0)

var inoCache *inoCacheStr

const UNMOUNT_FILE = ".00unmount"

type inoGen struct {
	ino uint64
	gen uint64
}
type inoCacheStr struct {
	m  map[string]inoGen
	mu sync.Mutex
}

type dependencyRoot struct {
	LinkType  string                     //SOFT HARD
	Children2 map[string]*dependencyRoot `json:"Children"`
	Target    string
	inoGen    inoGen
}

type dependencyRootNode struct {
	close chan os.Signal
	dependencyNode
}

func (this *dependencyRootNode) Rmdir(ctx context.Context, name string) syscall.Errno {
	if name == UNMOUNT_FILE {
		this.close <- syscall.SIGTERM
		return fs.OK
	}
	return syscall.ENOTSUP
}

var _ = (fs.NodeRmdirer)((*dependencyRootNode)(nil))

type dependencyNode struct {
	dependencyRoot
	fs.Inode
}

type zipNode struct {
	dependencyRoot
	zip.ZipDir
}

type lookbackNode struct {
	dependencyRoot
	fs.LoopbackNode
}

func (this *lookbackNode) Lookup(ctx context.Context, name string, out *fuse.EntryOut) (*fs.Inode, syscall.Errno) {
	if node, err := this.dependencyRoot.GetChild(ctx, &this.Inode, name, out); err != syscall.ENOENT {
		return node, err
	}
	return this.LoopbackNode.Lookup(ctx, name, out)
}

func (this *lookbackNode) Readdir(ctx context.Context) (fs.DirStream, syscall.Errno) {
	list, err := this.LoopbackNode.Readdir(ctx)
	if err != 0 {
		return list, err
	}
	list2, err := this.dependencyRoot.Readdir(ctx)
	if err != 0 {
		return list2, err
	}
	return NewMultiDirStream(list, list2), 0
}

var _ = (fs.NodeGetattrer)((*dependencyNode)(nil))
var _ = (fs.NodeLookuper)((*dependencyNode)(nil))
var _ = (fs.NodeReaddirer)((*dependencyNode)(nil))
var _ = (fs.NodeLookuper)((*zipNode)(nil))
var _ = (fs.NodeReaddirer)((*zipNode)(nil))

func (this *zipNode) Lookup(ctx context.Context, name string, out *fuse.EntryOut) (*fs.Inode, syscall.Errno) {
	if node, err := this.dependencyRoot.GetChild(ctx, &this.Inode, name, out); err != syscall.ENOENT {
		return node, err
	}
	return this.ZipDir.Lookup(ctx, name, out)
}

func (this *zipNode) Readdir(ctx context.Context) (fs.DirStream, syscall.Errno) {
	list, err := this.ZipDir.Readdir(ctx)
	if err != 0 {
		return list, err
	}
	list2, err := this.dependencyRoot.Readdir(ctx)

	if err != 0 {
		return list2, err
	}
	return NewMultiDirStream(list, list2), 0
}

func (this *dependencyRoot) init(depth int) {
	if depth == 0 {
		this.Children2[UNMOUNT_FILE] = &dependencyRoot{LinkType: "HARD", Target: "", Children2: map[string]*dependencyRoot{}}
	}
	this.inoGen = getInoStart(this.LinkType, this.Target)
	for _, dep := range this.Children2 {
		dep.init(depth + 1)
	}
}

func (r *dependencyRoot) Readdir(ctx context.Context) (fs.DirStream, syscall.Errno) {
	lst := make([]fuse.DirEntry, 0, len(r.Children2))
	for name, ch := range r.Children2 {
		lst = append(lst, fuse.DirEntry{
			Mode: getMode(ch),
			Name: name,
			Ino:  ch.inoGen.ino,
		})
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
	return r.dependencyRoot.GetChild(ctx, &r.Inode, name, out)
}

func (r *dependencyRoot) GetChild(ctx context.Context, parent *fs.Inode, name string, out *fuse.EntryOut) (*fs.Inode, syscall.Errno) {
	dep, ok := r.Children2[name]
	if !ok {
		// fmt.Println("GetChild", name, ok, r.Children2, r.Target, r.inoStart)
		return nil, syscall.ENOENT
	}
	ino := dep.inoGen.ino
	attr := fs.StableAttr{Mode: getMode(dep), Ino: ino, Gen: dep.inoGen.gen}

	if dep.LinkType == "SOFT" {
		if dep.Target == "" {
			log.Fatalf("Target is empty for %s", name)
		}
		ch := &fs.MemSymlink{
			Data: []byte(dep.Target),
		}
		return parent.NewInode(ctx, ch, attr), 0
	} else {
		if dep.Target == "" {
			return parent.NewInode(ctx, &dependencyNode{dependencyRoot: *dep}, attr), 0
		} else {
			if strings.Contains(dep.Target, ".zip") {
				parts := strings.SplitN(dep.Target, ".zip/", 2)
				root, err := zip.NewZipTree(ZIP_GETTER, parts[0]+".zip", parts[1], ino+1)
				if err != nil {
					log.Fatal(err)
				}

				// fmt.Println(dep.Target, ino+1, "ino")
				rootNode := &zipNode{dependencyRoot: *dep, ZipDir: zip.NewZipDir(root, "")}

				return parent.NewInode(ctx, rootNode, attr), 0
			} else {
				p := strings.TrimRight(dep.Target, "/")
				var st syscall.Stat_t
				err := syscall.Stat(p, &st)
				if err != nil {
					log.Fatal(err)
				}
				loopackRoot := &fs.LoopbackRoot{
					Path: p,
					Dev:  uint64(st.Dev),
					NewNode: func(rootData *fs.LoopbackRoot, parent *fs.Inode, name string, st *syscall.Stat_t) fs.InodeEmbedder {
						return &lookbackNode{dependencyRoot: *dep,
							LoopbackNode: fs.LoopbackNode{
								RootData: rootData,
							},
						}
					},
				}
				rootInode := parent.NewInode(ctx, loopackRoot.NewNode(loopackRoot, nil, "", nil), attr)
				loopackRoot.RootNode = rootInode
				return rootInode, 0
			}
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

func (this *FuseData) init() {
	for _, root := range this.Roots {
		root.init(0)
	}
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

type ApiServer struct {
}

func (this *ApiServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	d := Data{Pid: os.Getpid()}
	data, err := json.Marshal(d)
	if err != nil {
		log.Fatalf("json.Marshal: %v", err)
	}
	w.Header().Set("Content-Type", "application/json")
	w.Write(data)
}

func main() {

	debug := flag.Bool("debug", false, "print debug data")
	prof := flag.Bool("prof", false, "open profile server")

	flag.Parse()
	if len(flag.Args()) < 1 {
		log.Fatal("Usage:\n  hello MOUNTPOINT")
	}
	if *prof {
		defer profile.Start(profile.CPUProfile, profile.ProfilePath(".")). // profile.MemProfile
											Stop()
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
	close := make(chan os.Signal, 10)

	fuseData.init()
	// fmt.Println(fuseData.Roots)
	servers := make(map[string]*fuse.Server, 0)

	toMount := make([]ToMount, 0)
	for mount, root := range fuseData.Roots {
		node := &dependencyRootNode{close: close, dependencyNode: dependencyNode{dependencyRoot: root}}
		toMount = append(toMount, ToMount{mount, node})
	}
	// controlPath := configPath + ".control"
	// toMount = append(toMount, ToMount{controlPath, &ControlWrap{}})
	ZIP_GETTER = zip.CreateZipGetter()
	for _, mount := range toMount {
		println("Mounting", mount.path)
		if isExists(mount.path) {
			fmt.Println("Unmounting", mount.path)
			cmd := exec.Command("umount", mount.path)
			err := cmd.Run()
			if err != nil {
				fmt.Println("unmounting err", err)
			}
		} else {
			os.Mkdir(mount.path, 0755)
		}
		opts := &fs.Options{UID: uint32(os.Getuid()), GID: uint32(os.Getgid())}

		timeout := time.Second
		opts.AttrTimeout = &timeout
		opts.NegativeTimeout = &timeout
		opts.EntryTimeout = &timeout
		opts.DirectMount = true

		// opts.MaxBackground = 30
		opts.Debug = *debug
		opts.Options = []string{
			// "vm.vfs_cache_pressure=10",
			"auto_unmount",
		}

		if err != nil {
			log.Fatalf("Unmarshal fail: %v\n", err)
		}

		server, err := fs.Mount(mount.path, mount.root, opts)
		if err != nil {
			log.Fatalf("Mount fail: %v\n", err)
		}
		println("Mounted!", mount.path)
		servers[mount.path] = server

		// go func() {
		// 	err := server.WaitMount()
		// 	if err != nil {
		// 		log.Println(err)
		// 	}
		// 	println("here", mount.path, err)
		// 	close <- syscall.SIGTERM
		// }()
	}
	// handler := &ApiServer{}
	// listener, err2 := net.Listen("unix", controlPath)
	// if err2 != nil {
	// 	log.Fatal(err2)
	// }
	// go http.Serve(listener, handler)

	cleanup := func() {

		<-time.After(1 * time.Second)
		// listener.Close()
		for name, server := range servers {
			println("unmounting\n", name)
			err := server.Unmount()
			if err != nil {
				fmt.Println("unmounting err 1", err)
			}
			// <-time.After(1 * time.Second)
			// cmd := exec.Command("umount", name)
			// err = cmd.Run()
			// if err != nil {
			// 	fmt.Println("unmounting err 2", err)
			// }

		}
		fmt.Println("Finished cleanup")
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
