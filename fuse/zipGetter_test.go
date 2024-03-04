package main

import (
	"archive/zip"
	"fmt"
	"io"
	"os"
	"testing"
)

func TestEscapedMountOption(t *testing.T) {
	path := "/Users/vadymh/.yarn/berry/cache/yup-npm-0.29.3-759cc2e2b8-10c0.zip"
	// subp := "node_modules/yup"

	z, err := zip.OpenReader(path)
	if err != nil {
		t.Fatal(err)
	}
	// zip.RegisterDecompressor()
	f := z.File[11]
	offset, _ := f.DataOffset()
	z.Close()

	file, _ := os.Open(path)
	decomp := decompressor(f.Method)

	reader := io.NewSectionReader(file, offset, int64(f.CompressedSize64))
	res := decomp(reader)

	text := make([]byte, f.UncompressedSize64)
	_, e := io.ReadFull(res, text)
	// _, err2 := f.Open()
	// if err2 != nil {
	if e != nil {
		t.Fatal(e)
	}
	fmt.Print(string(text))
	// processed := processZip(zip, subp)
	// b, err := json.MarshalIndent(processed.chMap, "", "  ")
	// if err != nil {
	// 	t.Fatal(err)
	// }
	// fmt.Println(string(b))
}
