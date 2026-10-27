package main

// errors: the error-site inventory of a Go module. It parses and
// type-checks the module's packages (go/ast, go/types; imports outside the
// module are stand-ins, so only the module's own identifiers resolve) and
// lists every place an error a user can see is produced: errors.New,
// fmt.Errorf, sentinels (package-level error vars) and their uses, Failed(x)
// results, Status/Notify texts that read like an error, and panics. Each
// site carries its enclosing function, the noun.verb operations whose
// handler it is in (directly, or reached through calls), the enclosing
// conditions and a heuristic class. Output is sorted, so two runs over the
// same tree are byte-identical.

import (
	"bytes"
	"encoding/json"
	"fmt"
	"go/ast"
	"go/constant"
	"go/parser"
	"go/printer"
	"go/token"
	"go/types"
	"io/fs"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// emSite is one error site (a JSON Lines record).
type emSite struct {
	Module       string   `json:"module"`
	Package      string   `json:"package"`
	File         string   `json:"file"`
	Line         int      `json:"line"`
	Col          int      `json:"col"`
	Kind         string   `json:"kind"`
	Class        string   `json:"class"`
	Message      string   `json:"message"`
	Func         string   `json:"func"`
	Operations   []string `json:"operations,omitempty"`
	ReachedFrom  []string `json:"reachedFrom,omitempty"`
	ReachedCount int      `json:"reachedCount,omitempty"`
	Condition    string   `json:"condition,omitempty"`
	Conditions   []string `json:"conditions,omitempty"`
	Wrapped      bool     `json:"wrapped,omitempty"`
	Wraps        []string `json:"wraps,omitempty"`
	Failed       bool     `json:"failed,omitempty"`
	Relay        bool     `json:"relay,omitempty"`
	Sentinel     string   `json:"sentinel,omitempty"`
	Uses         []emUse  `json:"uses,omitempty"`
	Sentinels    []string `json:"sentinels,omitempty"`
	Test         bool     `json:"test,omitempty"`

	node  ast.Node
	fnPos token.Pos
}

// emUse is one reference to a sentinel.
type emUse struct {
	File       string   `json:"file"`
	Line       int      `json:"line"`
	Role       string   `json:"role"` // return, wrap, compare, other
	Func       string   `json:"func"`
	Operations []string `json:"operations,omitempty"`
	Via        []string `json:"via,omitempty"`
}

// emHandler is a verb registration whose handler did not resolve.
type emHandler struct {
	Op, Handler, File string
	Line              int
}

// emReport is a scan's result.
type emReport struct {
	Module     string
	Root       string
	Rev        string
	Patterns   []string
	Sites      []*emSite
	Unresolved []emHandler
	OpCount    int
}

const (
	emReachDepth = 3  // call hops from a handler counted as reaching
	emReachShown = 12 // reachedFrom entries kept per site
)

type emPkg struct {
	dir   string // relative to the root, "." for the root
	path  string
	files []*ast.File
	names []string // relative file names, parallel to files
	info  *types.Info
	tpkg  *types.Package
}

type emScan struct {
	fset     *token.FileSet
	root     string
	module   string
	pkgs     []*emPkg
	byPath   map[string]*emPkg
	fileName map[*ast.File]string
	filePkg  map[*ast.File]*emPkg

	decls    map[token.Pos]*ast.FuncDecl   // name pos -> decl
	funcName map[token.Pos]string          // func node pos (decl or lit) -> name
	parent   map[token.Pos]token.Pos       // func lit -> enclosing func node
	builders map[types.Object]string       // b := r.Define("noun")
	sentObjs map[types.Object]*emSite      // sentinel vars
	direct   map[token.Pos]map[string]bool // handler func node -> ops
	calls    map[token.Pos]map[token.Pos]bool
	ops      map[string]bool
	consumed map[ast.Node]string // constructor calls inside panic/Failed
	sites    []*emSite
	unres    []emHandler
	reach    map[token.Pos]map[string]int // func node -> op -> distance
	pending  []emPendingUse

	localFuncs    map[types.Object]token.Pos // x := func(...) {...}
	registrars    map[types.Object]*emRegistrar
	regHandlersOf map[types.Object][]ast.Expr
}

// ScanErrors scans the module at root, limited to the package patterns
// ("./..." by default; "." is the root package, "./x/..." a tree).
func ScanErrors(root string, patterns []string) (*emReport, error) {
	root, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	gomod, err := os.ReadFile(filepath.Join(root, "go.mod"))
	if err != nil {
		return nil, fmt.Errorf("errors: %s is not a module root: %w", root, err)
	}
	module := ""
	for _, line := range strings.Split(string(gomod), "\n") {
		if rest, ok := strings.CutPrefix(strings.TrimSpace(line), "module "); ok {
			module = strings.Trim(strings.TrimSpace(rest), `"`)
		}
	}
	if module == "" {
		return nil, fmt.Errorf("errors: %s/go.mod names no module", root)
	}
	if len(patterns) == 0 {
		patterns = []string{"./..."}
	}
	s := &emScan{
		fset: token.NewFileSet(), root: root, module: module,
		byPath: map[string]*emPkg{}, fileName: map[*ast.File]string{}, filePkg: map[*ast.File]*emPkg{},
		decls: map[token.Pos]*ast.FuncDecl{}, funcName: map[token.Pos]string{}, parent: map[token.Pos]token.Pos{},
		builders: map[types.Object]string{}, sentObjs: map[types.Object]*emSite{},
		direct: map[token.Pos]map[string]bool{}, calls: map[token.Pos]map[token.Pos]bool{},
		ops: map[string]bool{}, consumed: map[ast.Node]string{}, reach: map[token.Pos]map[string]int{},
		localFuncs: map[types.Object]token.Pos{}, registrars: map[types.Object]*emRegistrar{}, regHandlersOf: map[types.Object][]ast.Expr{},
	}
	var testSites []*emSite
	dirs, err := emDirs(root, patterns)
	if err != nil {
		return nil, err
	}
	for _, dir := range dirs {
		p, tests, err := s.parseDir(dir)
		if err != nil {
			return nil, err
		}
		testSites = append(testSites, tests...)
		if p != nil {
			s.pkgs = append(s.pkgs, p)
			s.byPath[p.path] = p
		}
	}
	s.check()
	for _, p := range s.pkgs {
		for _, f := range p.files {
			s.passBuilders(p, f)
		}
	}
	for _, p := range s.pkgs {
		for _, f := range p.files {
			s.passFuncs(p, f)
		}
	}
	for _, p := range s.pkgs {
		for _, f := range p.files {
			s.passRegistrarCalls(p, f)
		}
	}
	for _, p := range s.pkgs {
		for _, f := range p.files {
			s.passSites(p, f)
		}
	}
	s.propagate()
	s.finish()
	s.sites = append(s.sites, testSites...)
	sort.SliceStable(s.sites, func(i, j int) bool {
		a, b := s.sites[i], s.sites[j]
		if a.Test != b.Test {
			return !a.Test
		}
		if a.File != b.File {
			return a.File < b.File
		}
		if a.Line != b.Line {
			return a.Line < b.Line
		}
		if a.Col != b.Col {
			return a.Col < b.Col
		}
		return a.Kind < b.Kind
	})
	sort.Slice(s.unres, func(i, j int) bool {
		if s.unres[i].File != s.unres[j].File {
			return s.unres[i].File < s.unres[j].File
		}
		return s.unres[i].Line < s.unres[j].Line
	})
	rep := &emReport{Module: module, Root: root, Patterns: patterns, Sites: s.sites, Unresolved: s.unres, OpCount: len(s.ops)}
	if out, err := exec.Command("git", "-C", root, "rev-parse", "--short", "HEAD").Output(); err == nil {
		rep.Rev = strings.TrimSpace(string(out))
	}
	return rep, nil
}

// emDirs lists the package directories (relative, slash-separated) the
// patterns name, sorted; testdata, vendor and dot or underscore directories
// are skipped.
func emDirs(root string, patterns []string) ([]string, error) {
	seen := map[string]bool{}
	for _, pat := range patterns {
		pat = filepath.ToSlash(pat)
		recursive := false
		if rest, ok := strings.CutSuffix(pat, "/..."); ok {
			pat, recursive = rest, true
		} else if pat == "..." {
			pat, recursive = ".", true
		}
		pat = path.Clean(strings.TrimPrefix(pat, "./"))
		base := filepath.Join(root, filepath.FromSlash(pat))
		info, err := os.Stat(base)
		if err != nil || !info.IsDir() {
			return nil, fmt.Errorf("errors: pattern %q names no directory under %s", pat, root)
		}
		if !recursive {
			seen[pat] = true
			continue
		}
		err = filepath.WalkDir(base, func(p string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if !d.IsDir() {
				return nil
			}
			name := d.Name()
			if p != base && (name == "testdata" || name == "vendor" || strings.HasPrefix(name, ".") || strings.HasPrefix(name, "_")) {
				return filepath.SkipDir
			}
			if p != base {
				if _, err := os.Stat(filepath.Join(p, "go.mod")); err == nil {
					return filepath.SkipDir // a nested module
				}
			}
			rel, _ := filepath.Rel(root, p)
			seen[filepath.ToSlash(rel)] = true
			return nil
		})
		if err != nil {
			return nil, err
		}
	}
	out := make([]string, 0, len(seen))
	for d := range seen {
		out = append(out, d)
	}
	sort.Strings(out)
	return out, nil
}

// parseDir parses the non-test files of dir as one package, and returns the
// sentinels of its test files as test sites.
func (s *emScan) parseDir(dir string) (*emPkg, []*emSite, error) {
	abs := filepath.Join(s.root, filepath.FromSlash(dir))
	entries, err := os.ReadDir(abs)
	if err != nil {
		return nil, nil, err
	}
	p := &emPkg{dir: dir, path: s.module}
	if dir != "." {
		p.path = s.module + "/" + dir
	}
	var tests []*emSite
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasPrefix(name, ".") || strings.HasPrefix(name, "_") {
			continue
		}
		rel := name
		if dir != "." {
			rel = dir + "/" + name
		}
		f, err := parser.ParseFile(s.fset, filepath.Join(abs, name), nil, parser.ParseComments|parser.SkipObjectResolution)
		if err != nil {
			return nil, nil, fmt.Errorf("errors: %w", err)
		}
		if strings.HasSuffix(name, "_test.go") {
			tests = append(tests, s.testSentinels(p, rel, f)...)
			continue
		}
		if len(p.files) > 0 && f.Name.Name != p.files[0].Name.Name {
			continue // a second package in the directory (a generator's main)
		}
		p.files = append(p.files, f)
		p.names = append(p.names, rel)
		s.fileName[f] = rel
		s.filePkg[f] = p
	}
	if len(p.files) == 0 {
		return nil, tests, nil
	}
	return p, tests, nil
}

// testSentinels are the package-level errors.New vars of a test file.
func (s *emScan) testSentinels(p *emPkg, rel string, f *ast.File) []*emSite {
	var out []*emSite
	for _, d := range f.Decls {
		gd, ok := d.(*ast.GenDecl)
		if !ok || gd.Tok != token.VAR {
			continue
		}
		for _, spec := range gd.Specs {
			vs := spec.(*ast.ValueSpec)
			for i, v := range vs.Values {
				call, ok := v.(*ast.CallExpr)
				if !ok || i >= len(vs.Names) || !emIsPkgCall(call, "errors", "New") && !emIsPkgCall(call, "fmt", "Errorf") {
					continue
				}
				pos := s.fset.Position(call.Pos())
				msg := ""
				if len(call.Args) > 0 {
					msg = s.message(nil, call.Args[0])
				}
				out = append(out, &emSite{Module: s.module, Package: p.dir, File: rel, Line: pos.Line, Col: pos.Column,
					Kind: "sentinel", Class: "test", Message: msg, Func: "(package)", Sentinel: vs.Names[i].Name, Test: true})
			}
		}
	}
	return out
}

// check type-checks the packages in import order; an import outside the
// module is an empty stand-in, and every type error is ignored.
func (s *emScan) check() {
	done := map[string]*types.Package{}
	var visit func(p *emPkg, stack map[string]bool)
	imp := &emImporter{done: done}
	visit = func(p *emPkg, stack map[string]bool) {
		if done[p.path] != nil || stack[p.path] {
			return
		}
		stack[p.path] = true
		for _, f := range p.files {
			for _, is := range f.Imports {
				ip, _ := strconv.Unquote(is.Path.Value)
				if q := s.byPath[ip]; q != nil {
					visit(q, stack)
				}
			}
		}
		p.info = &types.Info{
			Types: map[ast.Expr]types.TypeAndValue{}, Defs: map[*ast.Ident]types.Object{},
			Uses: map[*ast.Ident]types.Object{}, Selections: map[*ast.SelectorExpr]*types.Selection{},
		}
		conf := types.Config{Importer: imp, Error: func(error) {}, FakeImportC: true}
		p.tpkg, _ = conf.Check(p.path, s.fset, p.files, p.info)
		done[p.path] = p.tpkg
	}
	for _, p := range s.pkgs {
		visit(p, map[string]bool{})
	}
}

type emImporter struct{ done map[string]*types.Package }

func (im *emImporter) Import(path string) (*types.Package, error) {
	if p := im.done[path]; p != nil {
		return p, nil
	}
	name := path[strings.LastIndex(path, "/")+1:]
	if len(name) >= 2 && name[0] == 'v' && strings.Trim(name[1:], "0123456789") == "" {
		trim := strings.TrimSuffix(path, "/"+name)
		name = trim[strings.LastIndex(trim, "/")+1:]
	}
	name = strings.TrimPrefix(strings.TrimPrefix(name, "go-"), "go")
	name = strings.Map(func(r rune) rune {
		if r == '-' || r == '.' {
			return -1
		}
		return r
	}, name)
	if name == "" {
		name = "x"
	}
	p := types.NewPackage(path, name)
	p.MarkComplete()
	im.done[path] = p
	return p, nil
}

// emIsPkgCall reports whether call is pkg.name(...) by spelling.
func emIsPkgCall(call *ast.CallExpr, pkg, name string) bool {
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok || sel.Sel.Name != name {
		return false
	}
	id, ok := sel.X.(*ast.Ident)
	return ok && id.Name == pkg
}

// pkgCall reports whether call is a call of the standard package path's
// function name, by its import (or by spelling when types are missing).
func (s *emScan) pkgCall(p *emPkg, call *ast.CallExpr, pkgPath, name string) bool {
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok || sel.Sel.Name != name {
		return false
	}
	id, ok := sel.X.(*ast.Ident)
	if !ok {
		return false
	}
	if p != nil && p.info != nil {
		if pn, ok := p.info.Uses[id].(*types.PkgName); ok {
			return pn.Imported().Path() == pkgPath
		}
	}
	return id.Name == pkgPath
}

func (s *emScan) text(n ast.Node) string {
	var b bytes.Buffer
	if err := printer.Fprint(&b, s.fset, n); err != nil {
		return "?"
	}
	out := strings.Join(strings.Fields(b.String()), " ")
	if len([]rune(out)) > 160 {
		out = string([]rune(out)[:157]) + "..."
	}
	return out
}

// strOf is a constant string expression's value.
func (s *emScan) strOf(p *emPkg, e ast.Expr) (string, bool) {
	if p != nil && p.info != nil {
		if tv, ok := p.info.Types[e]; ok && tv.Value != nil && tv.Value.Kind() == constant.String {
			return constant.StringVal(tv.Value), true
		}
	}
	if lit, ok := e.(*ast.BasicLit); ok && lit.Kind == token.STRING {
		if v, err := strconv.Unquote(lit.Value); err == nil {
			return v, true
		}
	}
	return "", false
}

// message is the text an expression makes: constants as they are, a
// Sprintf/Errorf its format, a concatenation piecewise, anything else
// {expression}.
func (s *emScan) message(p *emPkg, e ast.Expr) string {
	if v, ok := s.strOf(p, e); ok {
		return v
	}
	switch x := e.(type) {
	case *ast.ParenExpr:
		return s.message(p, x.X)
	case *ast.BinaryExpr:
		if x.Op == token.ADD {
			return s.message(p, x.X) + s.message(p, x.Y)
		}
	case *ast.CallExpr:
		if (s.pkgCall(p, x, "fmt", "Sprintf") || s.pkgCall(p, x, "fmt", "Errorf") || s.pkgCall(p, x, "errors", "New")) && len(x.Args) > 0 {
			return s.message(p, x.Args[0])
		}
	}
	return "{" + s.text(e) + "}"
}

// passBuilders records b := r.Define("noun") variables and the sentinels.
func (s *emScan) passBuilders(p *emPkg, f *ast.File) {
	for _, d := range f.Decls {
		if fd, ok := d.(*ast.FuncDecl); ok {
			s.decls[fd.Name.Pos()] = fd
			s.funcName[fd.Pos()] = emRecvName(fd, s)
		}
	}
	for _, d := range f.Decls {
		gd, ok := d.(*ast.GenDecl)
		if !ok || gd.Tok != token.VAR {
			continue
		}
		for _, spec := range gd.Specs {
			vs := spec.(*ast.ValueSpec)
			for i, v := range vs.Values {
				call, ok := v.(*ast.CallExpr)
				if !ok || i >= len(vs.Names) {
					continue
				}
				if !s.pkgCall(p, call, "errors", "New") && !s.pkgCall(p, call, "fmt", "Errorf") {
					continue
				}
				site := s.newSite(p, f, call, "sentinel", "(package)")
				site.Sentinel = vs.Names[i].Name
				if len(call.Args) > 0 {
					site.Message = s.message(p, call.Args[0])
				}
				if obj := p.info.Defs[vs.Names[i]]; obj != nil {
					s.sentObjs[obj] = site
				}
				s.consumed[call] = "sentinel"
				s.sites = append(s.sites, site)
			}
		}
	}
	ast.Inspect(f, func(n ast.Node) bool {
		switch x := n.(type) {
		case *ast.AssignStmt:
			for i, r := range x.Rhs {
				if i >= len(x.Lhs) {
					break
				}
				id, ok := x.Lhs[i].(*ast.Ident)
				if !ok {
					continue
				}
				if lit, ok := r.(*ast.FuncLit); ok {
					if obj := p.info.Defs[id]; obj != nil {
						s.localFuncs[obj] = lit.Pos()
					} else if obj := p.info.Uses[id]; obj != nil {
						s.localFuncs[obj] = lit.Pos()
					}
				}
				if noun, ok := s.nounOfChain(p, r); ok {
					obj := p.info.Defs[id]
					if obj == nil {
						obj = p.info.Uses[id]
					}
					if obj != nil {
						s.builders[obj] = noun
					}
				}
			}
		case *ast.ValueSpec:
			for i, r := range x.Values {
				if i < len(x.Names) {
					if noun, ok := s.nounOfChain(p, r); ok {
						if obj := p.info.Defs[x.Names[i]]; obj != nil {
							s.builders[obj] = noun
						}
					}
				}
			}
		}
		return true
	})
}

// chainNoun is the noun of a Define chain as the Define argument, or as
// the noun a builder variable holds.
func (s *emScan) chainNoun(p *emPkg, e ast.Expr) (ast.Expr, string, bool) {
	switch x := e.(type) {
	case *ast.ParenExpr:
		return s.chainNoun(p, x.X)
	case *ast.Ident:
		if obj := p.info.Uses[x]; obj != nil {
			if noun, ok := s.builders[obj]; ok {
				return nil, noun, true
			}
		}
	case *ast.CallExpr:
		sel, ok := x.Fun.(*ast.SelectorExpr)
		if !ok {
			return nil, "", false
		}
		if sel.Sel.Name == "Define" && len(x.Args) == 1 {
			return x.Args[0], "", true
		}
		return s.chainNoun(p, sel.X)
	}
	return nil, "", false
}

// nounOfChain is the noun of a Define builder chain: r.Define("x"),
// r.Define("x").Columns(...).Verb(...), or a builder variable.
func (s *emScan) nounOfChain(p *emPkg, e ast.Expr) (string, bool) {
	switch x := e.(type) {
	case *ast.ParenExpr:
		return s.nounOfChain(p, x.X)
	case *ast.Ident:
		if obj := p.info.Uses[x]; obj != nil {
			if noun, ok := s.builders[obj]; ok {
				return noun, true
			}
		}
	case *ast.CallExpr:
		sel, ok := x.Fun.(*ast.SelectorExpr)
		if !ok {
			return "", false
		}
		if sel.Sel.Name == "Define" && len(x.Args) == 1 {
			if v, ok := s.strOf(p, x.Args[0]); ok {
				return v, true
			}
			return "{" + s.text(x.Args[0]) + "}", true
		}
		return s.nounOfChain(p, sel.X)
	}
	return "", false
}

// funcNodes walks f with a stack and calls visit for each node.
func emWalk(f *ast.File, visit func(n ast.Node, stack []ast.Node)) {
	var stack []ast.Node
	ast.Inspect(f, func(n ast.Node) bool {
		if n == nil {
			stack = stack[:len(stack)-1]
			return true
		}
		visit(n, stack)
		stack = append(stack, n)
		return true
	})
}

// innerFunc is the innermost function node on the stack.
func emInnerFunc(stack []ast.Node) ast.Node {
	for i := len(stack) - 1; i >= 0; i-- {
		switch stack[i].(type) {
		case *ast.FuncDecl, *ast.FuncLit:
			return stack[i]
		}
	}
	return nil
}

func emRecvName(fd *ast.FuncDecl, s *emScan) string {
	if fd.Recv == nil || len(fd.Recv.List) == 0 {
		return fd.Name.Name
	}
	t := s.text(fd.Recv.List[0].Type)
	if i := strings.Index(t, "["); i >= 0 {
		t = t[:i]
	}
	return "(" + t + ")." + fd.Name.Name
}

// passFuncs names every function literal and records the verb
// registrations and the registrar functions (a function whose parameters
// become an Operation's Noun, Verb or Run).
func (s *emScan) passFuncs(p *emPkg, f *ast.File) {
	lits := map[ast.Node]int{}
	emWalk(f, func(n ast.Node, stack []ast.Node) {
		switch x := n.(type) {
		case *ast.FuncLit:
			outer := emInnerFunc(stack)
			top := ast.Node(nil)
			for _, st := range stack {
				if fd, ok := st.(*ast.FuncDecl); ok {
					top = fd
				}
			}
			name := "func"
			if top != nil {
				lits[top]++
				name = s.funcName[top.Pos()] + ".func" + strconv.Itoa(lits[top])
			} else {
				lits[nil]++
				name = "(package).func" + strconv.Itoa(lits[nil])
			}
			s.funcName[x.Pos()] = name
			if outer != nil {
				s.parent[x.Pos()] = outer.Pos()
			}
		case *ast.CallExpr:
			sel, ok := x.Fun.(*ast.SelectorExpr)
			if ok && sel.Sel.Name == "Verb" && len(x.Args) >= 2 {
				if nounE, noun, ok := s.chainNoun(p, sel.X); ok {
					s.regOp(p, f, stack, nounE, noun, x.Args[0], []ast.Expr{x.Args[1]})
				}
			}
		case *ast.CompositeLit:
			if !emIsOperationType(x.Type) {
				return
			}
			var nounE, verbE ast.Expr
			var handlers []ast.Expr
			for _, el := range x.Elts {
				kv, ok := el.(*ast.KeyValueExpr)
				if !ok {
					continue
				}
				key, _ := kv.Key.(*ast.Ident)
				if key == nil {
					continue
				}
				switch key.Name {
				case "Noun":
					nounE = kv.Value
				case "Verb":
					verbE = kv.Value
				case "Run", "App":
					handlers = append(handlers, kv.Value)
				}
			}
			if nounE == nil || verbE == nil {
				return
			}
			s.regOp(p, f, stack, nounE, "", verbE, handlers)
		}
	})
}

// passRegistrarCalls registers the verbs a call of a registrar function
// makes: add("undo", "...", params, fn).
func (s *emScan) passRegistrarCalls(p *emPkg, f *ast.File) {
	emWalk(f, func(n ast.Node, stack []ast.Node) {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return
		}
		id := emCalleeIdent(call.Fun)
		if id == nil {
			return
		}
		reg := s.registrars[p.info.Uses[id]]
		if reg == nil {
			return
		}
		arg := func(a emArg) (ast.Expr, string) {
			if a.param >= 0 {
				if a.param < len(call.Args) {
					return call.Args[a.param], ""
				}
				return nil, "{?}"
			}
			if a.expr != nil {
				return a.expr, ""
			}
			return nil, a.lit
		}
		nounE, noun := arg(reg.noun)
		verbE, verb := arg(reg.verb)
		var handlers []ast.Expr
		if reg.handler >= 0 && reg.handler < len(call.Args) {
			handlers = append(handlers, call.Args[reg.handler])
		} else if reg.handler < 0 {
			handlers = s.regHandlersOf[p.info.Uses[id]]
		}
		s.regOpResolved(p, f, stack, nounE, noun, verbE, verb, handlers)
	})
}

// emArg is a registrar's noun or verb: a call argument (param >= 0), an
// expression of its own, or a literal.
type emArg struct {
	param int
	expr  ast.Expr
	lit   string
}

type emRegistrar struct {
	noun, verb emArg
	handler    int
}

// emAlt is one value a noun or verb expression takes; elt is the element
// of the ranged composite literal it came from.
type emAlt struct {
	val string
	elt ast.Expr
	rng *ast.RangeStmt
}

// paramIndex is the index of obj among fn's parameters, -1 when it is none.
func (s *emScan) paramIndex(p *emPkg, fn ast.Node, e ast.Expr) int {
	id, ok := emUnparen(e).(*ast.Ident)
	if !ok || fn == nil {
		return -1
	}
	obj := p.info.Uses[id]
	if obj == nil {
		return -1
	}
	var ft *ast.FuncType
	switch x := fn.(type) {
	case *ast.FuncDecl:
		ft = x.Type
	case *ast.FuncLit:
		ft = x.Type
	}
	i := 0
	for _, field := range ft.Params.List {
		if len(field.Names) == 0 {
			i++
			continue
		}
		for _, name := range field.Names {
			if p.info.Defs[name] == obj {
				return i
			}
			i++
		}
	}
	return -1
}

// registrarObj is the object a registrar function is called through: a
// declared function, or the variable a literal is assigned to.
func (s *emScan) registrarObj(p *emPkg, fn ast.Node, stack []ast.Node) types.Object {
	switch x := fn.(type) {
	case *ast.FuncDecl:
		return p.info.Defs[x.Name]
	case *ast.FuncLit:
		for i := len(stack) - 1; i > 0; i-- {
			if stack[i] != fn {
				continue
			}
			if as, ok := stack[i-1].(*ast.AssignStmt); ok {
				for j, r := range as.Rhs {
					if r == fn && j < len(as.Lhs) {
						if id, ok := as.Lhs[j].(*ast.Ident); ok {
							if obj := p.info.Defs[id]; obj != nil {
								return obj
							}
							return p.info.Uses[id]
						}
					}
				}
			}
			return nil
		}
	}
	return nil
}

// regOp registers noun.verb for handlers, or records a registrar when the
// noun, verb or handler is a parameter of the enclosing function.
func (s *emScan) regOp(p *emPkg, f *ast.File, stack []ast.Node, nounE ast.Expr, noun string, verbE ast.Expr, handlers []ast.Expr) {
	fn := emInnerFunc(stack)
	if fd, ok := fn.(*ast.FuncDecl); ok && fd.Recv != nil && fd.Name.Name == "Verb" {
		return // the builder's own Verb: its calls are read as Define chains
	}
	np, vp, hp := -1, s.paramIndex(p, fn, verbE), -1
	if nounE != nil {
		np = s.paramIndex(p, fn, nounE)
	}
	if len(handlers) == 1 {
		hp = s.paramIndex(p, fn, handlers[0])
	}
	if np >= 0 || vp >= 0 || hp >= 0 {
		if obj := s.registrarObj(p, fn, stack); obj != nil {
			reg := &emRegistrar{noun: emArg{param: np, lit: noun}, verb: emArg{param: vp}, handler: hp}
			if np < 0 && nounE != nil {
				if v, ok := s.strOf(p, nounE); ok {
					reg.noun.lit = v
				} else {
					reg.noun.lit = "{" + s.text(nounE) + "}"
				}
			}
			if vp < 0 {
				if v, ok := s.strOf(p, verbE); ok {
					reg.verb.lit = v
				} else {
					reg.verb.lit = "{" + s.text(verbE) + "}"
				}
			}
			if hp < 0 {
				reg.handler = -1
				// the registrar's own handler expression is registered per call
				// through the literal handlers below, with the call's ops
				s.regHandlersOf[obj] = handlers
			}
			s.registrars[obj] = reg
			return
		}
	}
	s.regOpResolved(p, f, stack, nounE, noun, verbE, "", handlers)
}

// regOpResolved registers every noun.verb the expressions can take.
func (s *emScan) regOpResolved(p *emPkg, f *ast.File, stack []ast.Node, nounE ast.Expr, noun string, verbE ast.Expr, verb string, handlers []ast.Expr) {
	nouns := []emAlt{{val: noun}}
	if nounE != nil {
		nouns = s.alts(p, nounE, stack)
	}
	verbs := []emAlt{{val: verb}}
	if verbE != nil {
		verbs = s.alts(p, verbE, stack)
	}
	type pair struct {
		key string
		elt ast.Expr
	}
	var pairs []pair
	if nouns[0].rng != nil && nouns[0].rng == verbs[0].rng && len(nouns) == len(verbs) {
		for i := range nouns {
			pairs = append(pairs, pair{nouns[i].val + "." + verbs[i].val, verbs[i].elt})
		}
	} else {
		for _, n := range nouns {
			for _, v := range verbs {
				elt := v.elt
				if elt == nil {
					elt = n.elt
				}
				pairs = append(pairs, pair{n.val + "." + v.val, elt})
			}
		}
	}
	for _, pr := range pairs {
		if len(handlers) == 0 {
			s.ops[pr.key] = true
		}
		for _, h := range handlers {
			s.register(p, f, pr.key, h)
		}
		// the functions an element of a ranged table carries are its verb's
		if cl, ok := pr.elt.(*ast.CompositeLit); ok {
			for _, el := range cl.Elts {
				v := el
				if kv, ok := el.(*ast.KeyValueExpr); ok {
					v = kv.Value
				}
				for _, pos := range s.handlerNodes(p, v, false) {
					s.addDirect(pos, pr.key)
				}
			}
		}
	}
}

// alts are the values a noun or verb expression takes: a constant, a range
// variable over a composite literal of strings, or a field of a range
// variable over a literal table of structs; anything else is one {text}.
func (s *emScan) alts(p *emPkg, e ast.Expr, stack []ast.Node) []emAlt {
	if v, ok := s.strOf(p, e); ok {
		return []emAlt{{val: v}}
	}
	dynamic := []emAlt{{val: "{" + s.text(e) + "}"}}
	var id *ast.Ident
	field := ""
	switch x := emUnparen(e).(type) {
	case *ast.Ident:
		id = x
	case *ast.SelectorExpr:
		if xid, ok := x.X.(*ast.Ident); ok {
			id, field = xid, x.Sel.Name
		}
	}
	if id == nil {
		return dynamic
	}
	obj := p.info.Uses[id]
	if obj == nil {
		return dynamic
	}
	for i := len(stack) - 1; i >= 0; i-- {
		rs, ok := stack[i].(*ast.RangeStmt)
		if !ok {
			continue
		}
		vid, _ := rs.Value.(*ast.Ident)
		kid, _ := rs.Key.(*ast.Ident)
		isValue := vid != nil && p.info.Defs[vid] == obj
		isKey := kid != nil && p.info.Defs[kid] == obj
		if !isValue && !isKey {
			continue
		}
		cl, ok := emUnparen(rs.X).(*ast.CompositeLit)
		if !ok {
			return dynamic
		}
		var out []emAlt
		for _, el := range cl.Elts {
			val := el
			if kv, ok := el.(*ast.KeyValueExpr); ok {
				if isKey {
					val = kv.Key
				} else {
					val = kv.Value
				}
			} else if isKey {
				return dynamic // a slice index
			}
			if field != "" {
				val = s.fieldOf(cl, val, field)
				if val == nil {
					return dynamic
				}
			}
			v, ok := s.strOf(p, val)
			if !ok {
				return dynamic
			}
			out = append(out, emAlt{val: v, elt: el, rng: rs})
		}
		if len(out) == 0 {
			return dynamic
		}
		return out
	}
	return dynamic
}

// fieldOf is a struct literal's field by name, keyed or by position in the
// table's element type.
func (s *emScan) fieldOf(table *ast.CompositeLit, el ast.Expr, name string) ast.Expr {
	if kv, ok := el.(*ast.KeyValueExpr); ok {
		el = kv.Value
	}
	if u, ok := el.(*ast.UnaryExpr); ok && u.Op == token.AND {
		el = u.X
	}
	cl, ok := el.(*ast.CompositeLit)
	if !ok {
		return nil
	}
	for _, e := range cl.Elts {
		if kv, ok := e.(*ast.KeyValueExpr); ok {
			if k, ok := kv.Key.(*ast.Ident); ok && k.Name == name {
				return kv.Value
			}
		}
	}
	var st *ast.StructType
	switch t := table.Type.(type) {
	case *ast.ArrayType:
		st, _ = t.Elt.(*ast.StructType)
	case *ast.MapType:
		st, _ = t.Value.(*ast.StructType)
	}
	if st == nil {
		return nil
	}
	i := 0
	for _, fl := range st.Fields.List {
		for _, n := range fl.Names {
			if n.Name == name {
				if i < len(cl.Elts) {
					if _, keyed := cl.Elts[i].(*ast.KeyValueExpr); !keyed {
						return cl.Elts[i]
					}
				}
				return nil
			}
			i++
		}
	}
	return nil
}

func emIsOperationType(t ast.Expr) bool {
	switch x := t.(type) {
	case *ast.Ident:
		return x.Name == "Operation"
	case *ast.SelectorExpr:
		return x.Sel.Name == "Operation"
	}
	return false
}

func (s *emScan) addDirect(pos token.Pos, op string) {
	if s.direct[pos] == nil {
		s.direct[pos] = map[string]bool{}
	}
	s.direct[pos][op] = true
}

// register attributes op to the functions a handler expression names.
func (s *emScan) register(p *emPkg, f *ast.File, op string, h ast.Expr) {
	s.ops[op] = true
	if poss := s.handlerNodes(p, h, true); len(poss) > 0 {
		for _, pos := range poss {
			s.addDirect(pos, op)
		}
		return
	}
	if id, ok := h.(*ast.Ident); ok && id.Name == "nil" {
		return
	}
	at := s.fset.Position(h.Pos())
	s.unres = append(s.unres, emHandler{Op: op, Handler: s.text(h), File: s.fileName[f], Line: at.Line})
}

// handlerNodes are the function nodes of a handler: a literal, a function,
// method or local closure value, and for a call both the factory it names
// and the functions passed to it (input(func...), appHandler(a.verb)).
func (s *emScan) handlerNodes(p *emPkg, h ast.Expr, calls bool) []token.Pos {
	switch x := h.(type) {
	case *ast.ParenExpr:
		return s.handlerNodes(p, x.X, calls)
	case *ast.FuncLit:
		return []token.Pos{x.Pos()}
	case *ast.CallExpr:
		if !calls {
			return nil
		}
		var out []token.Pos
		if pos := s.funcRef(p, x.Fun, true); pos.IsValid() {
			out = append(out, pos)
		}
		for _, a := range x.Args {
			out = append(out, s.handlerNodes(p, a, false)...)
		}
		return out
	case *ast.Ident, *ast.SelectorExpr:
		if pos := s.funcRef(p, x, true); pos.IsValid() {
			return []token.Pos{pos}
		}
	}
	return nil
}

// funcRef is the declaration node of a function or method expression in
// the module; byName falls back to a method or function of that name
// declared once in the package when types did not resolve it.
func (s *emScan) funcRef(p *emPkg, e ast.Expr, byName bool) token.Pos {
	var id *ast.Ident
	switch x := e.(type) {
	case *ast.Ident:
		id = x
	case *ast.SelectorExpr:
		id = x.Sel
	case *ast.IndexExpr:
		return s.funcRef(p, x.X, byName)
	default:
		return token.NoPos
	}
	if v, ok := p.info.Uses[id].(*types.Var); ok {
		if pos, ok := s.localFuncs[v]; ok {
			return pos
		}
		return token.NoPos
	}
	if fn, ok := p.info.Uses[id].(*types.Func); ok {
		if fd := s.decls[fn.Pos()]; fd != nil {
			return fd.Pos()
		}
		if fn.Origin() != nil {
			if fd := s.decls[fn.Origin().Pos()]; fd != nil {
				return fd.Pos()
			}
		}
		return token.NoPos
	}
	if !byName || p.info.Uses[id] != nil {
		return token.NoPos
	}
	_, isSel := e.(*ast.SelectorExpr)
	var found *ast.FuncDecl
	n := 0
	for _, f := range p.files {
		for _, d := range f.Decls {
			fd, ok := d.(*ast.FuncDecl)
			if !ok || fd.Name.Name != id.Name || (fd.Recv != nil) != isSel {
				continue
			}
			found = fd
			n++
		}
	}
	if n == 1 {
		return found.Pos()
	}
	return token.NoPos
}

func (s *emScan) newSite(p *emPkg, f *ast.File, n ast.Node, kind, fn string) *emSite {
	pos := s.fset.Position(n.Pos())
	return &emSite{Module: s.module, Package: p.dir, File: s.fileName[f], Line: pos.Line, Col: pos.Column, Kind: kind, Func: fn, node: n}
}

var emErrorLike = regexp.MustCompile(`(?i)\b(errors?|fail(ed|s|ure)?|cannot|can't|could not|couldn't|unable|invalid|unknown|missing|refused|refuses|denied|not|no|nothing|already|warning|stale|gone|expired|timeout|timed out|unavailable|unsupported)\b|!`)

// passSites records the sites, the call edges and the sentinel uses.
func (s *emScan) passSites(p *emPkg, f *ast.File) {
	// constructors a panic or Failed consumes, first, so the walk sees them
	ast.Inspect(f, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok || len(call.Args) == 0 {
			return true
		}
		tag := ""
		if id, ok := call.Fun.(*ast.Ident); ok && id.Name == "panic" {
			tag = "panic"
		} else if emCalls(call, "Failed") {
			tag = "failed"
		}
		if tag == "" {
			return true
		}
		if inner, ok := emUnparen(call.Args[0]).(*ast.CallExpr); ok && (s.pkgCall(p, inner, "errors", "New") || s.pkgCall(p, inner, "fmt", "Errorf") || s.pkgCall(p, inner, "fmt", "Sprintf")) {
			if _, done := s.consumed[inner]; !done {
				s.consumed[inner] = tag
			}
		}
		return true
	})
	emWalk(f, func(n ast.Node, stack []ast.Node) {
		fnNode := emInnerFunc(stack)
		fnPos := token.NoPos
		fnName := "(package)"
		if fnNode != nil {
			fnPos = fnNode.Pos()
			fnName = s.funcName[fnPos]
		}
		switch x := n.(type) {
		case *ast.Ident:
			obj := p.info.Uses[x]
			if lit, ok := s.localFuncs[obj]; ok && fnPos.IsValid() && obj != nil {
				s.edge(fnPos, lit)
			}
			if fn, ok := obj.(*types.Func); ok && fnPos.IsValid() {
				if fd := s.decls[fn.Pos()]; fd != nil {
					s.edge(fnPos, fd.Pos())
				} else if fn.Origin() != nil {
					if fd := s.decls[fn.Origin().Pos()]; fd != nil {
						s.edge(fnPos, fd.Pos())
					}
				}
			}
			if site := s.sentObjs[obj]; site != nil && obj != nil {
				pos := s.fset.Position(x.Pos())
				site.Uses = append(site.Uses, emUse{File: s.fileName[f], Line: pos.Line, Role: s.useRole(p, x, stack), Func: fnName})
				s.pending = append(s.pending, emPendingUse{site: site, idx: len(site.Uses) - 1, fn: fnPos})
			}
		case *ast.CallExpr:
			if fnPos.IsValid() && p.info.Uses[emCalleeIdent(x.Fun)] == nil {
				if to := s.funcRef(p, x.Fun, true); to.IsValid() {
					s.edge(fnPos, to)
				}
			}
			s.callSite(p, f, x, stack, fnPos, fnName)
		}
	})
}

type emPendingUse struct {
	site *emSite
	idx  int
	fn   token.Pos
}

func emCalleeIdent(e ast.Expr) *ast.Ident {
	switch x := e.(type) {
	case *ast.Ident:
		return x
	case *ast.SelectorExpr:
		return x.Sel
	}
	return nil
}

func emUnparen(e ast.Expr) ast.Expr {
	for {
		p, ok := e.(*ast.ParenExpr)
		if !ok {
			return e
		}
		e = p.X
	}
}

// emCalls reports whether call names a function or method name.
func emCalls(call *ast.CallExpr, name string) bool {
	switch f := call.Fun.(type) {
	case *ast.Ident:
		return f.Name == name
	case *ast.SelectorExpr:
		return f.Sel.Name == name
	}
	return false
}

func (s *emScan) edge(from, to token.Pos) {
	if from == to {
		return
	}
	if s.calls[from] == nil {
		s.calls[from] = map[token.Pos]bool{}
	}
	s.calls[from][to] = true
}

// useRole is how a sentinel reference is used.
func (s *emScan) useRole(p *emPkg, id *ast.Ident, stack []ast.Node) string {
	for i := len(stack) - 1; i >= 0; i-- {
		switch x := stack[i].(type) {
		case *ast.ReturnStmt:
			return "return"
		case *ast.BinaryExpr:
			if x.Op == token.EQL || x.Op == token.NEQ {
				return "compare"
			}
		case *ast.CallExpr:
			if s.pkgCall(p, x, "errors", "Is") {
				return "compare"
			}
			if s.pkgCall(p, x, "fmt", "Errorf") {
				return "wrap"
			}
			if emCalls(x, "Failed") {
				return "return"
			}
		case *ast.CaseClause:
			return "compare"
		case *ast.BlockStmt, *ast.FuncLit, *ast.FuncDecl, *ast.AssignStmt:
			return "other"
		}
	}
	return "other"
}

// callSite records a call that is a site.
func (s *emScan) callSite(p *emPkg, f *ast.File, call *ast.CallExpr, stack []ast.Node, fnPos token.Pos, fnName string) {
	var site *emSite
	switch {
	case s.pkgCall(p, call, "errors", "New"), s.pkgCall(p, call, "fmt", "Errorf"):
		tag, consumed := s.consumed[call]
		if consumed && tag != "failed" {
			return // a sentinel, or a panic's argument (the panic is the site)
		}
		kind := "errors.New"
		if s.pkgCall(p, call, "fmt", "Errorf") {
			kind = "fmt.Errorf"
		}
		site = s.newSite(p, f, call, kind, fnName)
		site.Failed = tag == "failed"
		if len(call.Args) > 0 {
			site.Message = s.message(p, call.Args[0])
		}
		if kind == "fmt.Errorf" && len(call.Args) > 0 {
			if format, ok := s.strOf(p, call.Args[0]); ok {
				for _, i := range emWrapArgs(format) {
					site.Wrapped = true
					if 1+i < len(call.Args) {
						a := call.Args[1+i]
						site.Wraps = append(site.Wraps, s.text(a))
						if id := emCalleeIdent(emUnparen(a)); id != nil {
							if sent := s.sentObjs[p.info.Uses[id]]; sent != nil {
								site.Sentinels = append(site.Sentinels, sent.Sentinel)
							}
						}
					}
				}
			}
		}
	case emIsPanic(call):
		site = s.newSite(p, f, call, "panic", fnName)
		if len(call.Args) > 0 {
			site.Message = s.message(p, call.Args[0])
		}
	case emCalls(call, "Failed") && len(call.Args) == 1:
		if _, ok := s.consumed[emUnparen(call.Args[0])]; ok {
			return // the constructor inside is the site
		}
		site = s.newSite(p, f, call, "failed", fnName)
		site.Message = s.message(p, call.Args[0])
	case (emCalls(call, "Status") || emCalls(call, "Notify")) && len(call.Args) >= 1:
		if _, isSel := call.Fun.(*ast.SelectorExpr); !isSel {
			return
		}
		arg := call.Args[len(call.Args)-1]
		msg := s.message(p, arg)
		if !emErrorLike.MatchString(msg) && !strings.Contains(s.text(arg), "err") {
			return
		}
		kind := "status"
		if emCalls(call, "Notify") {
			kind = "notify"
		}
		site = s.newSite(p, f, call, kind, fnName)
		site.Message = msg
	default:
		return
	}
	site.fnPos = fnPos
	site.Conditions = s.conditions(stack)
	if len(site.Conditions) > 0 {
		site.Condition = site.Conditions[0]
	}
	sort.Strings(site.Sentinels)
	site.Sentinels = emUniq(site.Sentinels)
	s.sites = append(s.sites, site)
}

func emIsPanic(call *ast.CallExpr) bool {
	id, ok := call.Fun.(*ast.Ident)
	return ok && id.Name == "panic"
}

// emWrapArgs are the argument indexes of a format's %w verbs.
func emWrapArgs(format string) []int {
	var out []int
	arg := 0
	for i := 0; i < len(format); i++ {
		if format[i] != '%' {
			continue
		}
		i++
		if i < len(format) && format[i] == '%' {
			continue
		}
		for i < len(format) && strings.IndexByte("+-# 0123456789.*[]", format[i]) >= 0 {
			if format[i] == '*' {
				arg++
			}
			i++
		}
		if i < len(format) {
			if format[i] == 'w' {
				out = append(out, arg)
			}
			arg++
		}
	}
	return out
}

// conditions are the enclosing if (and case) conditions, innermost first,
// at most three, inside the innermost function.
func (s *emScan) conditions(stack []ast.Node) []string {
	var out []string
	for i := len(stack) - 1; i > 0 && len(out) < 3; i-- {
		child := stack[i]
		switch x := stack[i-1].(type) {
		case *ast.FuncDecl, *ast.FuncLit:
			return out
		case *ast.IfStmt:
			switch child {
			case x.Body:
				out = append(out, s.text(x.Cond))
			case x.Else:
				out = append(out, "!("+s.text(x.Cond)+")")
			}
		case *ast.CaseClause:
			sw := ""
			if i >= 3 {
				if ss, ok := stack[i-3].(*ast.SwitchStmt); ok && ss.Tag != nil {
					sw = "switch " + s.text(ss.Tag) + " "
				}
				if ts, ok := stack[i-3].(*ast.TypeSwitchStmt); ok {
					sw = "switch " + s.text(ts.Assign) + " "
				}
			}
			if len(x.List) == 0 {
				out = append(out, sw+"default")
			} else {
				var cs []string
				for _, e := range x.List {
					cs = append(cs, s.text(e))
				}
				out = append(out, sw+"case "+strings.Join(cs, ", "))
			}
		}
		if _, ok := stack[i-1].(*ast.FuncLit); ok {
			return out
		}
	}
	return out
}

func emUniq(xs []string) []string {
	var out []string
	for i, x := range xs {
		if i == 0 || x != xs[i-1] {
			out = append(out, x)
		}
	}
	return out
}

// propagate computes, for every function node, the distance from each
// operation's handler: lexical nesting costs nothing, a call one hop.
func (s *emScan) propagate() {
	children := map[token.Pos][]token.Pos{}
	for lit, outer := range s.parent {
		children[outer] = append(children[outer], lit)
	}
	for k := range children {
		sort.Slice(children[k], func(i, j int) bool { return children[k][i] < children[k][j] })
	}
	byOp := map[string][]token.Pos{}
	for pos, ops := range s.direct {
		for op := range ops {
			byOp[op] = append(byOp[op], pos)
		}
	}
	opNames := make([]string, 0, len(byOp))
	for op := range byOp {
		opNames = append(opNames, op)
	}
	sort.Strings(opNames)
	for _, op := range opNames {
		dist := map[token.Pos]int{}
		var front []token.Pos
		var lexical func(pos token.Pos, d int)
		lexical = func(pos token.Pos, d int) {
			if old, ok := dist[pos]; ok && old <= d {
				return
			}
			dist[pos] = d
			front = append(front, pos)
			for _, c := range children[pos] {
				if _, own := s.direct[c]; own && d == 0 {
					continue // a literal registered as another verb's handler
				}
				lexical(c, d)
			}
		}
		for _, pos := range byOp[op] {
			lexical(pos, 0)
		}
		for d := 0; d < emReachDepth; d++ {
			cur := front
			front = nil
			for _, pos := range cur {
				if dist[pos] != d {
					continue
				}
				for to := range s.calls[pos] {
					lexical(to, d+1)
				}
			}
		}
		for pos, d := range dist {
			if s.reach[pos] == nil {
				s.reach[pos] = map[string]int{}
			}
			s.reach[pos][op] = d
		}
	}
}

func (s *emScan) opsAt(pos token.Pos) (direct, indirect []string) {
	for op, d := range s.reach[pos] {
		if d == 0 {
			direct = append(direct, op)
		} else {
			indirect = append(indirect, op)
		}
	}
	sort.Strings(direct)
	sort.Strings(indirect)
	return direct, indirect
}

// finish fills the operations and classes.
func (s *emScan) finish() {
	for _, site := range s.sites {
		if site.fnPos.IsValid() {
			site.Operations, site.ReachedFrom = s.opsAt(site.fnPos)
			if len(site.ReachedFrom) > emReachShown {
				site.ReachedCount = len(site.ReachedFrom)
				site.ReachedFrom = site.ReachedFrom[:emReachShown]
			}
		}
		site.Relay = (site.Kind == "failed" || site.Kind == "status" || site.Kind == "notify") && emRelayRe.MatchString(site.Message)
		site.Class = emClassify(site)
	}
	// a wrap of a sentinel with no words of its own takes the sentinel's class
	classOf := map[string]string{}
	for _, site := range s.sites {
		if site.Kind == "sentinel" {
			classOf[site.Sentinel] = site.Class
		}
	}
	for _, site := range s.sites {
		if site.Class == "other" && len(site.Sentinels) > 0 {
			site.Class = classOf[site.Sentinels[0]]
		}
	}
	for _, u := range s.pending {
		if u.fn.IsValid() {
			u.site.Uses[u.idx].Operations, u.site.Uses[u.idx].Via = s.opsAt(u.fn)
			if len(u.site.Uses[u.idx].Via) > emReachShown {
				u.site.Uses[u.idx].Via = u.site.Uses[u.idx].Via[:emReachShown]
			}
		}
	}
	for _, site := range s.sites {
		sort.SliceStable(site.Uses, func(i, j int) bool {
			if site.Uses[i].File != site.Uses[j].File {
				return site.Uses[i].File < site.Uses[j].File
			}
			return site.Uses[i].Line < site.Uses[j].Line
		})
	}
}

var (
	emInternalRe = regexp.MustCompile(`(?i)unreachable|impossible|invariant|unexpected|should not|shouldn't|should never|not reached|\bbug\b|corrupt|inconsistent|no such case|programming error|internal error`)
	emStateRe    = regexp.MustCompile(`(?i)focus|modal|\battached|\bdetached|not attached|not open|is open|already|no page|not running|is running|no config|config dir|state dir|pending|busy|in progress|\bclosed\b|no selection|nothing (to|selected|marked)|no (row|item|instance|target|list|input|pane|session|window|terminal|current|active|job|server|daemon|editor|register|macro|history|change|undo|redo)s?\b|outside|not focusable|not available|unavailable|disabled|read-only|readonly|not recording|recording|no longer|still|stopped|not ready|not loaded|not connected|offline|locked|not live|in use|\bgone\b|\bnothing\b|\byet\b|not (sent|mounted|shown|drawn|set up|configured)|are open|(^|: )no `)
	emNoSuchRe   = regexp.MustCompile(`(?i)\bno such\b|\bno match`)
	emRelayRe    = regexp.MustCompile(`^\{[^{}]*\}$`)
	emInputRe    = regexp.MustCompile(`(?i)missing|invalid|unknown|required|requires|\bmust\b|expected|expects|\bempty\b|not an? |\btakes\b|too (many|few|long|short)|argument|parameter|usage|\bbad\b|parse|malformed|out of range|not found|no such|no match|one of|positive|negative|at least|at most|cannot be|needs|\bwant\b|unsupported|not allowed|duplicate|exists`)
	emIORe       = regexp.MustCompile(`(?i)\b(read|reading|write|writing|open|opening|create|creating|mkdir|rename|remove|removing|delete|stat|exec|spawn|connect|dial|socket|daemon|server|http|fetch|db|sql|sqlite|database|file|dir|directory|json|decode|decoding|encode|marshal|unmarshal|load|loading|save|saving|store|pty|process|signal|exit|tuios|git|network|request|response|stream|pipe)\b`)
)

// emClassify is the heuristic class of a site: internal, state, input, io
// or other. Rules, in order: a panic is internal; the message matches the
// internal, state, input or io words; a wrapped error under err != nil is
// io; a switch default is internal; else other.
func emClassify(s *emSite) string {
	msg := s.Message
	if s.Kind == "sentinel" && msg == "" {
		msg = s.Sentinel
	}
	switch {
	case s.Kind == "panic":
		return "internal"
	case s.Relay:
		return "other"
	case emInternalRe.MatchString(msg):
		return "internal"
	case emNoSuchRe.MatchString(msg):
		return "input"
	case emStateRe.MatchString(msg):
		return "state"
	case emInputRe.MatchString(msg):
		return "input"
	case emIORe.MatchString(msg):
		return "io"
	case s.Wrapped && strings.Contains(s.Condition, "err != nil"):
		return "io"
	case strings.HasSuffix(s.Condition, "default"):
		return "internal"
	}
	return "other"
}

// WriteJSONL writes one site per line.
func (r *emReport) WriteJSONL(w *bytes.Buffer) error {
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	for _, s := range r.Sites {
		if err := enc.Encode(s); err != nil {
			return err
		}
	}
	return nil
}

var emClasses = []string{"internal", "state", "input", "io", "other"}

// opGroup is the operation heading a site is listed under.
func emOpGroup(s *emSite) string {
	switch {
	case len(s.Operations) > 0:
		return strings.Join(s.Operations, ", ")
	case len(s.ReachedFrom) == 1:
		return "via " + s.ReachedFrom[0]
	case len(s.ReachedFrom) > 1:
		return "~ helpers reached from several verbs"
	}
	return "~ no operation"
}

func emMD(s string) string {
	s = strings.ReplaceAll(s, "\n", `\n`)
	s = strings.ReplaceAll(s, "`", "'")
	return s
}

// WriteMarkdown writes the list grouped by class and operation.
func (r *emReport) WriteMarkdown(w *bytes.Buffer, command string) {
	var main, tests []*emSite
	for _, s := range r.Sites {
		if s.Test {
			tests = append(tests, s)
		} else {
			main = append(main, s)
		}
	}
	fmt.Fprintf(w, "# Error sites: %s\n\n", r.Module)
	rev := r.Rev
	if rev == "" {
		rev = "unknown"
	}
	fmt.Fprintf(w, "Generated from `%s` at `%s` (packages `%s`; `_test.go` and testdata excluded). Regenerate:\n\n```sh\n%s\n```\n\n", r.Module, rev, strings.Join(r.Patterns, " "), command)
	fmt.Fprintf(w, "Classes are heuristic (message words, then `err != nil` wraps as io and a switch `default` as internal). `operation` is the verb whose handler holds the site; `via` means a handler reaches it in at most %d calls. `when` (the key path that gets there) is not known statically.\n\n", emReachDepth)

	kinds := map[string]bool{}
	count := map[string]map[string]int{}
	for _, s := range main {
		kinds[s.Kind] = true
		if count[s.Class] == nil {
			count[s.Class] = map[string]int{}
		}
		count[s.Class][s.Kind]++
	}
	kindList := make([]string, 0, len(kinds))
	for k := range kinds {
		kindList = append(kindList, k)
	}
	sort.Strings(kindList)
	fmt.Fprintf(w, "## Summary\n\n%d sites; %d operations registered; %d handlers unresolved.\n\n| class | %s | total |\n|---|%s---|\n", len(main), r.OpCount, len(r.Unresolved), strings.Join(kindList, " | "), strings.Repeat("---|", len(kindList)))
	total := map[string]int{}
	for _, c := range emClasses {
		row := 0
		fmt.Fprintf(w, "| %s |", c)
		for _, k := range kindList {
			fmt.Fprintf(w, " %d |", count[c][k])
			row += count[c][k]
			total[k] += count[c][k]
		}
		fmt.Fprintf(w, " %d |\n", row)
	}
	fmt.Fprintf(w, "| total |")
	for _, k := range kindList {
		fmt.Fprintf(w, " %d |", total[k])
	}
	fmt.Fprintf(w, " %d |\n\n", len(main))

	attributed := 0
	for _, s := range main {
		if len(s.Operations) > 0 {
			attributed++
		}
	}
	relayCount := 0
	for _, s := range main {
		if s.Relay {
			relayCount++
		}
	}
	fmt.Fprintf(w, "%d sites are relays (listed at the end). %d sites are inside a verb handler, %d more are reached from one.\n\n", relayCount, attributed, func() int {
		n := 0
		for _, s := range main {
			if len(s.Operations) == 0 && len(s.ReachedFrom) > 0 {
				n++
			}
		}
		return n
	}())

	for _, c := range emClasses {
		groups := map[string][]*emSite{}
		for _, s := range main {
			if s.Class == c && !s.Relay {
				g := emOpGroup(s)
				groups[g] = append(groups[g], s)
			}
		}
		if len(groups) == 0 {
			continue
		}
		names := make([]string, 0, len(groups))
		for g := range groups {
			names = append(names, g)
		}
		sort.Strings(names)
		fmt.Fprintf(w, "## %s\n\n", c)
		for _, g := range names {
			fmt.Fprintf(w, "### %s\n\n", g)
			for _, s := range groups[g] {
				fmt.Fprintf(w, "- `%s:%d` %s `%s` in `%s`", s.File, s.Line, s.Kind, emMD(s.Message), s.Func)
				if len(s.Conditions) > 0 {
					var cs []string
					for _, c := range s.Conditions {
						cs = append(cs, "`"+emMD(c)+"`")
					}
					fmt.Fprintf(w, " when %s", strings.Join(cs, " ⟵ "))
				}
				if s.Wrapped {
					fmt.Fprintf(w, "; wraps `%s`", emMD(strings.Join(s.Wraps, ", ")))
				}
				if s.Failed {
					fmt.Fprintf(w, "; Failed")
				}
				if s.Sentinel != "" {
					fmt.Fprintf(w, "; sentinel `%s` (%d uses)", s.Sentinel, len(s.Uses))
				}
				if len(s.Operations) > 0 && len(s.ReachedFrom) > 0 || len(s.ReachedFrom) > 1 {
					more := ""
					if s.ReachedCount > len(s.ReachedFrom) {
						more = fmt.Sprintf(" (+%d)", s.ReachedCount-len(s.ReachedFrom))
					}
					fmt.Fprintf(w, "; via %s%s", strings.Join(s.ReachedFrom, ", "), more)
				}
				fmt.Fprintln(w)
			}
			fmt.Fprintln(w)
		}
	}

	relays := map[string][]string{}
	for _, s := range main {
		if s.Relay {
			g := emOpGroup(s)
			relays[g] = append(relays[g], fmt.Sprintf("`%s:%d` %s", s.File, s.Line, s.Kind))
		}
	}
	if len(relays) > 0 {
		names := make([]string, 0, len(relays))
		for g := range relays {
			names = append(names, g)
		}
		sort.Strings(names)
		fmt.Fprintf(w, "## Relays\n\nA `Failed(err)`, `Status(err.Error())` or `Notify(err.Error())` of an error made elsewhere (class other in the JSON): where the user sees it, not where it is made.\n\n")
		for _, g := range names {
			fmt.Fprintf(w, "- %s: %s\n", g, strings.Join(relays[g], ", "))
		}
		fmt.Fprintln(w)
	}

	fmt.Fprintf(w, "## Sentinels\n\n")
	for _, s := range main {
		if s.Kind != "sentinel" {
			continue
		}
		fmt.Fprintf(w, "- `%s` `%s:%d` `%s` (%s)\n", s.Sentinel, s.File, s.Line, emMD(s.Message), s.Class)
		for _, u := range s.Uses {
			ops := ""
			if len(u.Operations) > 0 {
				ops = " [" + strings.Join(u.Operations, ", ") + "]"
			} else if len(u.Via) > 0 {
				ops = " [via " + strings.Join(u.Via, ", ") + "]"
			}
			fmt.Fprintf(w, "  - %s `%s:%d` in `%s`%s\n", u.Role, u.File, u.Line, u.Func, ops)
		}
	}
	fmt.Fprintln(w)
	if len(tests) > 0 {
		fmt.Fprintf(w, "## Test-only sentinels\n\n")
		for _, s := range tests {
			fmt.Fprintf(w, "- `%s` `%s:%d` `%s`\n", s.Sentinel, s.File, s.Line, emMD(s.Message))
		}
		fmt.Fprintln(w)
	}
	if len(r.Unresolved) > 0 {
		fmt.Fprintf(w, "## Unresolved handlers\n\nVerbs whose handler expression names no function in the module (a variable, a field, a function from another module): their sites are listed without the operation.\n\n")
		for _, u := range r.Unresolved {
			fmt.Fprintf(w, "- `%s` handler `%s` at `%s:%d`\n", u.Op, emMD(u.Handler), u.File, u.Line)
		}
		fmt.Fprintln(w)
	}
}

// ErrorsCommand is `errors [--jsonl PATH] [--md PATH] ROOT [PATTERN...]`.
func (t *Tool) ErrorsCommand(args []string) error {
	f, err := parseFlags("errors", args, nil, []string{"jsonl", "md"})
	if err != nil {
		return err
	}
	if len(f.words) == 0 {
		return fmt.Errorf("errors: name a module root (errors [--jsonl PATH] [--md PATH] ROOT [PATTERN...])")
	}
	root := t.untilde(f.words[0])
	if !filepath.IsAbs(root) {
		root = filepath.Join(t.Cwd, root)
	}
	rep, err := ScanErrors(root, f.words[1:])
	if err != nil {
		return err
	}
	cmd := []string{"o-inventory", "errors"}
	if v := f.values["md"]; v != "" {
		cmd = append(cmd, "--md", t.tilde(t.abs(v)))
	}
	if v := f.values["jsonl"]; v != "" {
		cmd = append(cmd, "--jsonl", t.tilde(t.abs(v)))
	}
	cmd = append(cmd, t.tilde(rep.Root))
	cmd = append(cmd, f.words[1:]...)
	var md bytes.Buffer
	rep.WriteMarkdown(&md, strings.Join(cmd, " "))
	if v := f.values["jsonl"]; v != "" {
		var jl bytes.Buffer
		if err := rep.WriteJSONL(&jl); err != nil {
			return err
		}
		if err := os.WriteFile(t.abs(v), jl.Bytes(), 0o644); err != nil {
			return err
		}
	}
	if v := f.values["md"]; v != "" {
		if err := os.WriteFile(t.abs(v), md.Bytes(), 0o644); err != nil {
			return err
		}
		fmt.Fprintf(t.Stdout, "%s: %d sites\n", t.tilde(t.abs(v)), len(rep.Sites))
		return nil
	}
	if f.values["jsonl"] != "" {
		return nil
	}
	_, err = t.Stdout.Write(md.Bytes())
	return err
}

func (t *Tool) abs(p string) string {
	p = t.untilde(p)
	if !filepath.IsAbs(p) {
		p = filepath.Join(t.Cwd, p)
	}
	return p
}
