package main

import (
	"bytes"
	"fmt"
	"go/ast"
	"go/printer"
	"go/token"
	"go/types"
	"strings"

	"golang.org/x/tools/go/ast/astutil"
	"golang.org/x/tools/go/packages"
)

func findTypeSpec(decl *ast.GenDecl, pos token.Pos) *ast.TypeSpec {
	for _, spec := range decl.Specs {
		typeSpec := spec.(*ast.TypeSpec)
		if typeSpec.Pos() == pos {
			return typeSpec
		}
	}
	return nil
}

func findVarSpec(decl *ast.GenDecl, pos token.Pos) *ast.ValueSpec {
	for _, spec := range decl.Specs {
		varSpec := spec.(*ast.ValueSpec)
		for _, ident := range varSpec.Names {
			if ident.Pos() == pos {
				return varSpec
			}
		}
	}
	return nil
}

func formatNode(n ast.Node, obj types.Object, prog *packages.Package) string {
	//fmt.Printf("formatting %T node\n", n)
	var nc ast.Node
	// Render a copy of the node with no documentation.
	// We emit the documentation ourself.
	switch n := n.(type) {
	case *ast.FuncDecl:
		cp := *n
		cp.Doc = nil
		// Don't print the whole function body
		cp.Body = nil
		nc = &cp
	case *ast.Field:
		// Not supported by go/printer

		// TODO(dominikh): Methods in interfaces are syntactically
		// represented as fields. Using types.Object.String for those
		// causes them to look different from real functions.
		// go/printer doesn't include the import paths in names, while
		// Object.String does. Fix that.

		return obj.String()
	case *ast.TypeSpec:
		specCp := *n
		if !*showUnexportedFields {
			trimUnexportedElems(&specCp)
		}
		specCp.Doc = nil
		typeSpec := ast.GenDecl{
			Tok:   token.TYPE,
			Specs: []ast.Spec{&specCp},
		}
		nc = &typeSpec
	case *ast.GenDecl:
		cp := *n
		cp.Doc = nil
		if len(n.Specs) > 0 {
			// Only print this one type, not all the types in the gendecl
			switch n.Specs[0].(type) {
			case *ast.TypeSpec:
				spec := findTypeSpec(n, obj.Pos())
				if spec != nil {
					specCp := *spec
					if !*showUnexportedFields {
						trimUnexportedElems(&specCp)
					}
					specCp.Doc = nil
					cp.Specs = []ast.Spec{&specCp}
				}
				cp.Lparen = 0
				cp.Rparen = 0
			case *ast.ValueSpec:
				spec := findVarSpec(n, obj.Pos())
				if spec != nil {
					specCp := *spec
					specCp.Doc = nil
					cp.Specs = []ast.Spec{&specCp}
				}
				cp.Lparen = 0
				cp.Rparen = 0
			}
		}
		nc = &cp

	default:
		return obj.String()
	}

	buf := &bytes.Buffer{}
	cfg := printer.Config{Mode: printer.UseSpaces | printer.TabIndent, Tabwidth: 8}
	err := cfg.Fprint(buf, prog.Fset, nc)
	if err != nil {
		return obj.String()
	}
	return buf.String()
}

// IdentDoc attempts to get the documentation for a *ast.Ident.
func IdentDoc(id *ast.Ident, info *types.Info, pkg *packages.Package) (*Doc, error) {
	// get definition of identifier
	obj := info.ObjectOf(id)

	// for anonymous fields, we want the type definition, not the field
	if v, ok := obj.(*types.Var); ok && v.Anonymous() {
		obj = info.Uses[id]
	}

	var pos string
	if p := obj.Pos(); p.IsValid() {
		pos = pkg.Fset.Position(p).String()
	}

	pkgPath, pkgName := "", ""
	if op := obj.Pkg(); op != nil {
		pkgPath = op.Path()
		pkgName = op.Name()
	}

	// handle packages imported under a different name
	if p, ok := obj.(*types.PkgName); ok {
		return PackageDoc(pkg, p.Imported().Path())
	}

	_, nodes := pathEnclosingInterval(pkg, obj.Pos(), obj.Pos())
	if len(nodes) == 0 {
		// special case - builtins
		doc, decl := findInBuiltin(obj.Name(), obj, pkg)
		if doc != "" {
			return &Doc{
				Import: "builtin",
				Pkg:    "builtin",
				Name:   obj.Name(),
				Doc:    doc,
				Decl:   decl,
				Pos:    pos,
			}, nil
		}
		return nil, fmt.Errorf("no documentation found for %s", obj.Name())
	}
	var doc *Doc
	for _, node := range nodes {
		switch node.(type) {
		case *ast.Ident:
			// continue ascending AST (searching for parent node of the identifier)
			continue
		case *ast.FuncDecl, *ast.GenDecl, *ast.Field, *ast.TypeSpec, *ast.ValueSpec:
			// found the parent node
		default:
			break
		}
		doc = &Doc{
			Import: stripVendorFromImportPath(pkgPath),
			Pkg:    pkgName,
			Name:   obj.Name(),
			Decl:   formatNode(node, obj, pkg),
			Pos:    pos,
		}
		break
	}
	if doc == nil {
		// This shouldn't happen
		return nil, fmt.Errorf("no documentation found for %s", obj.Name())
	}

	for _, node := range nodes {
		//fmt.Printf("for %s: found %T\n%#v\n", id.Name, node, node)
		switch n := node.(type) {
		case *ast.Ident:
			continue
		case *ast.FuncDecl:
			doc.Doc = n.Doc.Text()
			return doc, nil
		case *ast.Field:
			if n.Doc != nil {
				doc.Doc = n.Doc.Text()
			} else if n.Comment != nil {
				doc.Doc = n.Comment.Text()
			}
			return doc, nil
		case *ast.TypeSpec:
			if n.Doc != nil {
				doc.Doc = n.Doc.Text()
				return doc, nil
			}
			if n.Comment != nil {
				doc.Doc = n.Comment.Text()
				return doc, nil
			}
		case *ast.ValueSpec:
			if n.Doc != nil {
				doc.Doc = n.Doc.Text()
				return doc, nil
			}
			if n.Comment != nil {
				doc.Doc = n.Comment.Text()
				return doc, nil
			}
		case *ast.GenDecl:
			constValue := ""
			if c, ok := obj.(*types.Const); ok {
				constValue = c.Val().ExactString()
			}
			if doc.Doc == "" && n.Doc != nil {
				doc.Doc = n.Doc.Text()
			}
			if constValue != "" {
				doc.Doc += fmt.Sprintf("\nConstant Value: %s", constValue)
			}
			return doc, nil
		default:
			return doc, nil
		}
	}
	return doc, nil
}

// pathEnclosingInterval returns the types.Info of the package and ast.Node that
// contain source interval [start, end), and all the node's ancestors
// up to the AST root.  It searches the ast.Files of initPkg and the packages it imports.
//
// Modified from golang.org/x/tools/go/loader.
func pathEnclosingInterval(initPkg *packages.Package, start, end token.Pos) (*types.Info, []ast.Node) {
	pkgs := []*packages.Package{initPkg}
	for _, pkg := range initPkg.Imports {
		pkgs = append(pkgs, pkg)
	}

	for _, pkg := range pkgs {
		for _, f := range pkg.Syntax {
			if f.Pos() == token.NoPos {
				// This can happen if the parser saw
				// too many errors and bailed out.
				// (Use parser.AllErrors to prevent that.)
				continue
			}
			if !tokenFileContainsPos(pkg.Fset.File(f.Pos()), start) {
				continue
			}
			if path, _ := astutil.PathEnclosingInterval(f, start, end); path != nil {
				return pkg.TypesInfo, path
			}
		}
	}
	return nil, nil
}

func tokenFileContainsPos(f *token.File, pos token.Pos) bool {
	p := int(pos)
	base := f.Base()
	return base <= p && p < base+f.Size()
}

func stripVendorFromImportPath(ip string) string {
	vendor := "/vendor/"
	l := len(vendor)
	if i := strings.LastIndex(ip, vendor); i != -1 {
		return ip[i+l:]
	}
	return ip
}
