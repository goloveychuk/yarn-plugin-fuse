package main

import (
	"archive/zip"
	"encoding/json"
	"fmt"
	"testing"
)

func TestEscapedMountOption(t *testing.T) {
	path := "/Users/vadymh/.yarn/berry/cache/yup-npm-0.29.3-759cc2e2b8-10c0.zip"
	subp := "node_modules/yup"

	zip, err := zip.OpenReader(path)
	if err != nil {
		t.Fatal(err)
	}
	processed := processZip(zip, subp)
	b, err := json.MarshalIndent(processed.chMap, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	fmt.Println(string(b))
}
