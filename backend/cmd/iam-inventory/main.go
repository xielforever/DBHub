// iam-inventory inventories source registrations without invoking any endpoint.
// Dynamic registrations stay unresolved; this tool never claims authorization
// coverage merely because a route contains authentication middleware.
package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"go/ast"
	"go/parser"
	"go/printer"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

type registration struct {
	File           string `json:"file"`
	Line           int    `json:"line"`
	Kind           string `json:"kind"`
	Pattern        string `json:"pattern"`
	Dynamic        bool   `json:"dynamic"`
	Bindings       string `json:"bindings"`
	ActionCoverage string `json:"action_coverage"`
}

func main() {
	root := flag.String("root", ".", "backend source directory")
	out := flag.String("out", "", "output JSON file, otherwise stdout")
	flag.Parse()
	fset := token.NewFileSet()
	entries := []registration{}
	err := filepath.WalkDir(filepath.Join(*root, "internal"), func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		file, e := parser.ParseFile(fset, path, nil, 0)
		if e != nil {
			return e
		}
		ast.Inspect(file, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			sel, ok := call.Fun.(*ast.SelectorExpr)
			if !ok || len(call.Args) == 0 {
				return true
			}
			if sel.Sel.Name != "Handle" && sel.Sel.Name != "HandleFunc" && sel.Sel.Name != "Register" {
				return true
			}
			var b bytes.Buffer
			_ = printer.Fprint(&b, fset, call.Args[0])
			pattern := b.String()
			dynamic := true
			if literal, ok := call.Args[0].(*ast.BasicLit); ok && literal.Kind == token.STRING {
				if value, e := strconv.Unquote(literal.Value); e == nil {
					pattern = value
					dynamic = false
				}
			}
			if sel.Sel.Name == "Register" && pattern != "mux" {
				return true
			}
			b.Reset()
			_ = printer.Fprint(&b, fset, call)
			rel, _ := filepath.Rel(*root, path)
			entries = append(entries, registration{File: filepath.ToSlash(rel), Line: fset.Position(call.Pos()).Line, Kind: sel.Sel.Name, Pattern: pattern, Dynamic: dynamic, Bindings: b.String(), ActionCoverage: "UNREVIEWED"})
			return true
		})
		return nil
	})
	if err != nil {
		panic(err)
	}
	sort.Slice(entries, func(i, j int) bool {
		if entries[i].File == entries[j].File {
			return entries[i].Line < entries[j].Line
		}
		return entries[i].File < entries[j].File
	})
	dynamic := 0
	for _, entry := range entries {
		if entry.Dynamic {
			dynamic++
		}
	}
	report := map[string]any{"format": "dbhub-iam-endpoint-inventory-v1", "generated_at": time.Now().UTC().Format(time.RFC3339), "registrations": len(entries), "unresolved_dynamic": dynamic, "action_coverage_verified": false, "entries": entries}
	b, e := json.MarshalIndent(report, "", "  ")
	if e != nil {
		panic(e)
	}
	b = append(b, '\n')
	if *out == "" {
		_, e = os.Stdout.Write(b)
	} else {
		e = os.WriteFile(*out, b, 0600)
	}
	if e != nil {
		panic(e)
	}
}
