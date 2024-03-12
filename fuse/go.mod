module goloveychuk/yarn-fuse

go 1.21

require (
	github.com/hanwen/go-fuse/v2 v2.5.0
	github.com/hashicorp/golang-lru/v2 v2.0.7
	github.com/pkg/profile v1.7.0
)

require (
	github.com/felixge/fgprof v0.9.3 // indirect
	github.com/google/pprof v0.0.0-20211214055906-6f57359322fd // indirect
	golang.org/x/sys v0.17.0 // indirect
)

replace github.com/hanwen/go-fuse/v2 => github.com/goloveychuk/go-fuse/v2 v2.0.0-20240312092809-e0b7bf301862
