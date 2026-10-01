// Copyright 2025 Google LLC
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//      http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package checker

import (
	"runtime"
	"strings"
	"syscall"
	"testing"

	"cel.dev/cel-go/common"
	"cel.dev/cel-go/common/containers"
	"cel.dev/cel-go/common/decls"
	"cel.dev/cel-go/common/stdlib"
	"cel.dev/cel-go/common/types"
	"cel.dev/cel-go/parser"
)

// sizeTestEnv is an environment of the standard library with the variable x (int) and l (list
// of int), and the size limit of a type set to limit when it is positive.
func sizeTestEnv(t *testing.T, limit int) (*Env, *parser.Parser) {
	t.Helper()
	var opts []Option
	if limit > 0 {
		opts = append(opts, MaxTypeSize(limit))
	}
	env, err := NewEnv(containers.DefaultContainer, newTestRegistry(t), opts...)
	if err != nil {
		t.Fatalf("NewEnv() failed: %v", err)
	}
	if err := env.AddFunctions(stdlib.Functions()...); err != nil {
		t.Fatalf("AddFunctions() failed: %v", err)
	}
	if err := env.AddIdents(
		decls.NewVariable("x", types.IntType),
		decls.NewVariable("l", types.NewListType(types.IntType))); err != nil {
		t.Fatalf("AddIdents() failed: %v", err)
	}
	p, err := parser.NewParser(parser.Macros(parser.AllMacros...))
	if err != nil {
		t.Fatalf("parser.NewParser() failed: %v", err)
	}
	return env, p
}

// checkSizeExpr checks expr and returns its issues and what the check allocated.
func checkSizeExpr(t *testing.T, env *Env, p *parser.Parser, expr string) ([]string, uint64) {
	t.Helper()
	src := common.NewTextSource(expr)
	parsed, errs := p.Parse(src)
	if len(errs.GetErrors()) > 0 {
		t.Fatalf("Parse(%q) failed: %v", expr, errs.ToDisplayString())
	}
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	_, errs = Check(parsed, src, env)
	runtime.ReadMemStats(&after)
	var issues []string
	for _, e := range errs.GetErrors() {
		issues = append(issues, e.Message)
	}
	return issues, after.TotalAlloc - before.TotalAlloc
}

// TestMaxTypeSizeIsNotNegative: the option refuses a negative bound.
func TestMaxTypeSizeIsNotNegative(t *testing.T) {
	if _, err := NewEnv(containers.DefaultContainer, newTestRegistry(t), MaxTypeSize(-1)); err == nil {
		t.Fatal("NewEnv with MaxTypeSize(-1) did not fail")
	}
}

// TestSubstituteTypeSizePoints: every composite a substitution builds - a list, a map, an
// opaque type, a type with a parameter - is refused when it would carry the unfolded size of
// the type over the limit, and built unchanged when it would not.
func TestSubstituteTypeSizePoints(t *testing.T) {
	const limit = 8
	// deep is eight nodes; a composite that holds it twice is seventeen.
	deep := types.NewListType(types.NewListType(types.NewListType(types.NewListType(types.NewListType(types.NewListType(types.NewListType(types.IntType)))))))
	if got := (&mapping{sizeLimit: limit}).typeSize(deep); got != 8 {
		t.Fatalf("the size of a list seven over int is %d, wanted 8", got)
	}
	shallow := types.NewListType(types.IntType)
	for _, tc := range []struct {
		name string
		t    func(p *types.Type) *types.Type // the composite the substitution builds
	}{
		{"a list", func(p *types.Type) *types.Type { return types.NewListType(p) }},
		{"a map", func(p *types.Type) *types.Type { return types.NewMapType(p, p) }},
		{"an opaque type", func(p *types.Type) *types.Type { return types.NewOpaqueType("op", p, p) }},
		{"a type with a parameter", func(p *types.Type) *types.Type { return types.NewTypeTypeWithParam(p) }},
	} {
		for _, bound := range []struct {
			name string
			t    *types.Type
		}{
			{"shallow", shallow},
			{"deep", deep},
		} {
			m := newMapping()
			m.sizeLimit = limit
			m.add(types.NewTypeParamType("T"), bound.t)
			got := substitute(m, tc.t(types.NewTypeParamType("T")), false)
			if bound.name == "shallow" {
				if m.overSize || got == types.ErrorType {
					t.Errorf("%s of %s: refused (%v) though the size stays under %d", tc.name, bound.name, got, limit)
				}
				continue
			}
			// A composite of one deep part goes over the limit when it holds the part twice
			// or wraps it (one more node); a bare wrapper of eight nodes is nine.
			if !m.overSize || got != types.ErrorType {
				t.Errorf("%s of %s: built (%v, overSize %v) though the size goes over %d", tc.name, bound.name, got, m.overSize, limit)
			}
		}
	}
}

// TestMaxTypeSizeRefusesEveryConstruction: the bound holds at the places the checker builds a
// composite outside a substitution too - a list literal and a map literal.
func TestMaxTypeSizeRefusesEveryConstruction(t *testing.T) {
	const limit = 8
	list := strings.Repeat("[", 9) + "1" + strings.Repeat("]", 9)
	mp := strings.Repeat("{1: ", 8) + "1" + strings.Repeat("}", 8)
	env, p := sizeTestEnv(t, limit)
	for _, tc := range []struct {
		name string
		expr string
	}{
		{"a list literal nine deep", list},
		{"a map literal eight deep", mp},
	} {
		t.Run(tc.name, func(t *testing.T) {
			issues, _ := checkSizeExpr(t, env, p, tc.expr)
			found := false
			for _, m := range issues {
				if strings.Contains(m, "larger than the checker reads") {
					found = true
				}
			}
			if !found {
				t.Errorf("issues are %v, wanted the size issue among them", issues)
			}
		})
	}
}

// TestMaxTypeSizeRefusesTheTypeOfAFunction: the type the checker builds for a declared
// function - its result and its arguments - is a composite like any other: a declaration
// whose arguments are deep enough is refused within the bound.
func TestMaxTypeSizeRefusesTheTypeOfAFunction(t *testing.T) {
	const limit = 8
	deep := types.NewListType(types.NewListType(types.NewListType(types.NewListType(types.NewListType(types.NewListType(types.NewListType(types.IntType)))))))
	env, err := NewEnv(containers.DefaultContainer, newTestRegistry(t), MaxTypeSize(limit))
	if err != nil {
		t.Fatalf("NewEnv() failed: %v", err)
	}
	if err := env.AddFunctions(stdlib.Functions()...); err != nil {
		t.Fatalf("AddFunctions() failed: %v", err)
	}
	if err := env.AddFunctions(testFunction(t, "f",
		decls.Overload("f_overload", []*types.Type{deep, deep}, types.BoolType))); err != nil {
		t.Fatalf("AddFunctions(f) failed: %v", err)
	}
	p, err := parser.NewParser(parser.Macros(parser.AllMacros...))
	if err != nil {
		t.Fatalf("parser.NewParser() failed: %v", err)
	}
	args := strings.Repeat("[", 7) + "1" + strings.Repeat("]", 7)
	issues, _ := checkSizeExpr(t, env, p, "f("+args+", "+args+")")
	found := false
	for _, m := range issues {
		if strings.Contains(m, "larger than the checker reads") {
			found = true
		}
	}
	if !found {
		t.Errorf("issues are %v, wanted the size issue among them", issues)
	}
}

// TestMaxTypeSizeRefusesTheOptionalOfADeepType: the optional the checker wraps a field
// selection's result in is a composite like any other: a value deep enough under it is
// refused within the bound.
func TestMaxTypeSizeRefusesTheOptionalOfADeepType(t *testing.T) {
	const limit = 8
	deep := types.NewListType(types.NewListType(types.NewListType(types.NewListType(types.NewListType(types.NewListType(types.NewListType(types.IntType)))))))
	env, err := NewEnv(containers.DefaultContainer, newTestRegistry(t), MaxTypeSize(limit))
	if err != nil {
		t.Fatalf("NewEnv() failed: %v", err)
	}
	if err := env.AddFunctions(stdlib.Functions()...); err != nil {
		t.Fatalf("AddFunctions() failed: %v", err)
	}
	if err := env.AddIdents(decls.NewVariable("m",
		types.NewOptionalType(types.NewMapType(types.StringType, deep)))); err != nil {
		t.Fatalf("AddIdents() failed: %v", err)
	}
	p, err := parser.NewParser(parser.Macros(parser.AllMacros...))
	if err != nil {
		t.Fatalf("parser.NewParser() failed: %v", err)
	}
	issues, _ := checkSizeExpr(t, env, p, "m.k")
	found := false
	for _, msg := range issues {
		if strings.Contains(msg, "larger than the checker reads") {
			found = true
		}
	}
	if !found {
		t.Errorf("issues are %v, wanted the size issue among them", issues)
	}
}

// TestMaxTypeSizeBoundsTheDoubling: the form that doubles the type on every link of a chain -
// the macro variable as the key and the value of a map - is refused within the bound, and a
// short chain of it compiles as without the option.
func TestMaxTypeSizeBoundsTheDoubling(t *testing.T) {
	const limit = 256
	doubling := func(k int) string {
		return "size([1]" + strings.Repeat(".map(x, {x: x})", k) + ") > 0"
	}
	env, p := sizeTestEnv(t, limit)

	issues, alloc := checkSizeExpr(t, env, p, doubling(20))
	var ru syscall.Rusage
	if err := syscall.Getrusage(0, &ru); err != nil {
		t.Fatalf("Getrusage() failed: %v", err)
	}
	t.Logf("twenty links: %d bytes allocated, getrusage maxrss %d", alloc, ru.Maxrss)
	found := false
	for _, m := range issues {
		if strings.Contains(m, "larger than the checker reads (256 nodes)") {
			found = true
		}
	}
	if !found {
		t.Errorf("issues of twenty links are %v, wanted the size issue", issues)
	}
	if alloc > 4<<20 {
		t.Errorf("twenty links allocated %d bytes, wanted no more than 4 MiB", alloc)
	}

	issues, _ = checkSizeExpr(t, env, p, doubling(3))
	if len(issues) != 0 {
		t.Errorf("three links have issues %v, wanted none", issues)
	}
}

// TestMaxTypeSizeBoundsTheRefusal: an expression whose hundred type errors each format a deep
// type is bounded by the limit as well: the chain it needs is refused before the errors are
// formatted.
func TestMaxTypeSizeBoundsTheRefusal(t *testing.T) {
	const limit = 256
	link := ".map(x, " + strings.Repeat("[", 40) + "x" + strings.Repeat("]", 40) + ")"
	expr := "size([1]" + strings.Repeat(link, 40) + ".map(v, [" + strings.Repeat("v+1,", 100) + "v])) > 0"
	env, p := sizeTestEnv(t, limit)
	issues, alloc := checkSizeExpr(t, env, p, expr)
	var ru syscall.Rusage
	if err := syscall.Getrusage(0, &ru); err != nil {
		t.Fatalf("Getrusage() failed: %v", err)
	}
	t.Logf("the refusal: %d bytes allocated, getrusage maxrss %d", alloc, ru.Maxrss)
	if len(issues) == 0 {
		t.Fatal("the expression has no issues at all")
	}
	found := false
	for _, m := range issues {
		if strings.Contains(m, "larger than the checker reads") {
			found = true
		}
	}
	if !found {
		t.Errorf("issues are %v, wanted the size issue among them", issues)
	}
	if alloc > 64<<20 {
		t.Errorf("the refusal allocated %d bytes, wanted no more than 64 MiB", alloc)
	}
}

// TestMaxTypeSizeOffByDefault: without the option the doubling chain behaves as before the
// option existed - it compiles, at the cost the bound exists to refuse - and with the option
// the same chain is refused.
func TestMaxTypeSizeOffByDefault(t *testing.T) {
	doubling := func(k int) string {
		return "size([1]" + strings.Repeat(".map(x, {x: x})", k) + ") > 0"
	}
	plain, p := sizeTestEnv(t, 0)
	issues, alloc := checkSizeExpr(t, plain, p, doubling(12))
	if len(issues) != 0 {
		t.Fatalf("twelve links have issues %v without the option, wanted none", issues)
	}
	if alloc < 4<<20 {
		t.Errorf("twelve links allocated %d bytes without the option, wanted the doubling it has always had (more than 4 MiB)", alloc)
	}
	bounded, p2 := sizeTestEnv(t, 256)
	issues, alloc = checkSizeExpr(t, bounded, p2, doubling(12))
	if len(issues) != 1 || !strings.Contains(issues[0], "larger than the checker reads") {
		t.Errorf("twelve links have issues %v with the option, wanted the size issue alone", issues)
	}
	if alloc > 1<<20 {
		t.Errorf("twelve links allocated %d bytes with the option, wanted no more than 1 MiB", alloc)
	}
}
