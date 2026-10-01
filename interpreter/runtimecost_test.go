// Copyright 2022 Google LLC
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

package interpreter

import (
	"fmt"
	"math"
	"math/rand"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"cel.dev/cel-go/checker"
	"cel.dev/cel-go/common"
	"cel.dev/cel-go/common/containers"
	"cel.dev/cel-go/common/decls"
	"cel.dev/cel-go/common/overloads"
	"cel.dev/cel-go/common/types"
	"cel.dev/cel-go/common/types/ref"
	"cel.dev/cel-go/parser"

	proto3pb "cel.dev/cel-go/test/proto3pb"
)

func TestTrackCostAdvanced(t *testing.T) {
	var equalCases = []struct {
		in      any
		lhsExpr string
		rhsExpr string
	}{
		{
			lhsExpr: `1`,
			rhsExpr: `2`,
		},
		{
			lhsExpr: `"abc".contains("d")`,
			rhsExpr: `"def".contains("d")`,
		},
		{
			lhsExpr: `1 in [4, 5, 6]`,
			rhsExpr: `2 in [15, 17, 16]`,
		},
	}
	for _, tc := range equalCases {
		t.Run(tc.lhsExpr+" vs "+tc.rhsExpr, func(t *testing.T) {
			ctx := constructActivation(t, tc.in)
			lhsCost, _, err := computeCost(t, tc.lhsExpr, nil, ctx, nil)
			if err != nil {
				t.Fatalf("Interpreter.Eval(activation Activation) failed to eval expression due: %v", err)
			}
			rhsCost, _, err := computeCost(t, tc.rhsExpr, nil, ctx, nil)
			if err != nil {
				t.Fatalf("Interpreter.Eval(activation Activation) failed to eval expression due: %v", err)
			}
			if lhsCost != rhsCost {
				t.Errorf(`Interpreter.Eval(activation Activation) failed return a cost for %s of %d equal to a cost for %s of %d`,
					tc.lhsExpr, lhsCost, tc.rhsExpr, rhsCost)
			}
		})

	}
	var smallerCases = []struct {
		in      any
		lhsExpr string
		rhsExpr string
	}{
		{
			lhsExpr: `1`,
			rhsExpr: `1 + 2`,
		},
		{
			lhsExpr: `"abc".contains("d")`,
			rhsExpr: `"abcdhdflsfiehfieubdkwjbdwgxvuyagwsdwdnw qdbgquyidvbwqi".contains("e")`,
		},
		{
			lhsExpr: `1 in [4, 5, 6]`,
			rhsExpr: `1 in [4, 5, 6, 7, 8, 9]`,
		},
	}
	for _, tc := range smallerCases {
		t.Run(tc.lhsExpr+" vs "+tc.rhsExpr, func(t *testing.T) {
			ctx := constructActivation(t, tc.in)
			lhsCost, _, err := computeCost(t, tc.lhsExpr, nil, ctx, nil)
			if err != nil {
				t.Fatalf("Interpreter.Eval(activation Activation) failed to eval expression due: %v", err)
			}
			rhsCost, _, err := computeCost(t, tc.rhsExpr, nil, ctx, nil)
			if err != nil {
				t.Fatalf("Interpreter.Eval(activation Activation) failed to eval expression due: %v", err)
			}
			if lhsCost >= rhsCost {
				t.Errorf(`Interpreter.Eval(activation Activation) failed return a cost for %s of %d less than the cost for %s of %d`,
					tc.lhsExpr, lhsCost, tc.rhsExpr, rhsCost)
			}
		})
	}
}

func computeCost(t *testing.T, expr string, vars []*decls.VariableDecl, ctx Activation, options []CostTrackerOption) (cost uint64, est checker.CostEstimate, err error) {
	t.Helper()

	s := common.NewTextSource(expr)
	p, err := parser.NewParser(parser.Macros(parser.AllMacros...))
	if err != nil {
		t.Fatalf("Failed to initialize parser: %v", err)
	}
	parsed, errs := p.Parse(s)
	if len(errs.GetErrors()) != 0 {
		t.Fatalf(`Failed to Parse expression "%s", error: %v`, expr, errs.GetErrors())
	}

	cont := containers.DefaultContainer
	reg := newTestRegistry(t, types.ProtoTypeDefs(&proto3pb.TestAllTypes{}))
	attrs := NewAttributeFactory(cont, reg, reg)
	env := newTestEnv(t, cont, reg)
	err = env.AddIdents(vars...)
	if err != nil {
		t.Fatalf("Failed to initialize env: %v", err)
	}
	costTracker, err := NewCostTracker(&testRuntimeCostEstimator{}, options...)
	if err != nil {
		t.Fatalf("NewCostTracker() failed: %v", err)
	}
	costTracker, err = costTracker.Clone()
	if err != nil {
		t.Fatalf("checker.Clone() failed: %v", err)
	}
	checked, errs := checker.Check(parsed, s, env)
	if len(errs.GetErrors()) != 0 {
		t.Fatalf(`Failed to check expression "%s", error: %v`, expr, errs.GetErrors())
	}
	est, err = checker.Cost(checked, testCostEstimator{}, checker.PresenceTestHasCost(costTracker.presenceTestHasCost))
	if err != nil {
		t.Fatalf("checker.Cost() failed: %v", err)
	}
	interp := newStandardInterpreter(t, cont, reg, reg, attrs)
	prg, err := interp.NewInterpretable(checked,
		CostObserver(CostTrackerFactory(func() (*CostTracker, error) {
			return costTracker, nil
		})))
	if err != nil {
		t.Fatalf(`Failed to check expression "%s", error: %v`, expr, errs.GetErrors())
	}

	defer func() {
		if r := recover(); r != nil {
			switch t := r.(type) {
			case EvalCancelledError:
				err = t
			default:
				err = fmt.Errorf("internal error: %v", r)
			}
		}
	}()
	frame := AsFrame(ctx)
	prg.Exec(frame)
	// TODO: enable this once all attributes are properly pushed and popped from stack.
	//if len(costTracker.stack) != 1 {
	//	t.Fatalf(`Expected resulting stack size to be 1 but got %d: %#+v`, len(costTracker.stack), costTracker.stack)
	//}
	return costTracker.cost, est, err
}

func constructActivation(t *testing.T, in any) Activation {
	t.Helper()
	if in == nil {
		return EmptyActivation()
	}
	a, err := NewActivation(in)
	if err != nil {
		t.Fatalf("NewActivation(%v) failed: %v", in, err)
	}
	return a
}

const letterBytes = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ"

func randSeq(n int) []byte {
	b := make([]byte, n)
	for i := range b {
		b[i] = letterBytes[rand.Intn(len(letterBytes))]
	}
	return b
}

type testRuntimeCostEstimator struct {
}

var timeToYearCost uint64 = 7

func (e testRuntimeCostEstimator) CallCost(function, overloadID string, args []ref.Val, result ref.Val) *uint64 {
	argsSize := make([]uint64, len(args))
	for i, arg := range args {
		reflectV := reflect.ValueOf(arg.Value())
		switch reflectV.Kind() {
		// Note that the CEL bytes type is implemented with Go byte slices, therefore also supported by the following
		// code.
		case reflect.String, reflect.Array, reflect.Slice, reflect.Map:
			argsSize[i] = uint64(reflectV.Len())
		default:
			argsSize[i] = 1
		}
	}

	switch overloadID {
	case overloads.TimestampToYear:
		return &timeToYearCost
	default:
		return nil
	}
}

type testCostEstimator struct {
	hints map[string]int64
}

func (tc testCostEstimator) EstimateSize(element checker.AstNode) *checker.SizeEstimate {
	if l, ok := tc.hints[strings.Join(element.Path(), ".")]; ok {
		return &checker.SizeEstimate{Min: 0, Max: uint64(l)}
	}
	return nil
}

func (tc testCostEstimator) EstimateCallCost(function, overloadID string, target *checker.AstNode, args []checker.AstNode) *checker.CallEstimate {
	switch overloadID {
	case overloads.TimestampToYear:
		return &checker.CallEstimate{CostEstimate: checker.FixedCostEstimate(7)}
	}
	return nil
}

func TestRuntimeCost(t *testing.T) {
	allTypes := types.NewObjectType("google.expr.proto3.test.TestAllTypes")
	allList := types.NewListType(allTypes)
	intList := types.NewListType(types.IntType)
	nestedList := types.NewListType(allList)

	allMap := types.NewMapType(types.StringType, allTypes)
	nestedMap := types.NewMapType(types.StringType, allMap)
	cases := []struct {
		name         string
		expr         string
		vars         []*decls.VariableDecl
		want         uint64
		in           any
		testFuncCost bool
		limit        uint64
		options      []CostTrackerOption

		expectExceedsLimit bool
	}{
		{
			name: "const",
			expr: `"Hello World!"`,
			want: 0,
		},
		{
			name: "identity",
			expr: `input`,
			vars: []*decls.VariableDecl{decls.NewVariable("input", intList)},
			want: 1,
			in:   map[string]any{"input": []int{1, 2}},
		},
		{
			name: "select: map",
			expr: `input['key']`,
			vars: []*decls.VariableDecl{decls.NewVariable("input", types.NewMapType(types.StringType, types.StringType))},
			want: 2,
			in:   map[string]any{"input": map[string]string{"key": "v"}},
		},
		{
			name: "select: array index",
			expr: `input[0]`,
			vars: []*decls.VariableDecl{decls.NewVariable("input", types.NewListType(types.StringType))},
			want: 2,
			in:   map[string]any{"input": []string{"v"}},
		},
		{
			name: "select: field",
			expr: `input.single_int32`,
			vars: []*decls.VariableDecl{decls.NewVariable("input", allTypes)},
			want: 2,
			in: map[string]any{
				"input": &proto3pb.TestAllTypes{
					RepeatedBool: []bool{false},
					MapInt64NestedType: map[int64]*proto3pb.NestedTestAllTypes{
						1: {},
					},
					MapStringString: map[string]string{},
				},
			},
		},
		{
			name: "expr select: map",
			expr: `input['ke' + 'y']`,
			vars: []*decls.VariableDecl{decls.NewVariable("input", types.NewMapType(types.StringType, types.StringType))},
			want: 3,
			in:   map[string]any{"input": map[string]string{"key": "v"}},
		},
		{
			name: "expr select: array index",
			expr: `input[3-3]`,
			vars: []*decls.VariableDecl{decls.NewVariable("input", types.NewListType(types.StringType))},
			want: 3,
			in:   map[string]any{"input": []string{"v"}},
		},
		{
			name:    "select: field test only no has() cost",
			expr:    `has(input.single_int32)`,
			vars:    []*decls.VariableDecl{decls.NewVariable("input", types.NewObjectType("google.expr.proto3.test.TestAllTypes"))},
			want:    1,
			options: []CostTrackerOption{PresenceTestHasCost(false)},
			in: map[string]any{
				"input": &proto3pb.TestAllTypes{
					RepeatedBool: []bool{false},
					MapInt64NestedType: map[int64]*proto3pb.NestedTestAllTypes{
						1: {},
					},
					MapStringString: map[string]string{},
				},
			},
		},
		{
			name: "select: field test only",
			expr: `has(input.single_int32)`,
			vars: []*decls.VariableDecl{decls.NewVariable("input", types.NewObjectType("google.expr.proto3.test.TestAllTypes"))},
			want: 2,
			in: map[string]any{
				"input": &proto3pb.TestAllTypes{
					RepeatedBool: []bool{false},
					MapInt64NestedType: map[int64]*proto3pb.NestedTestAllTypes{
						1: {},
					},
					MapStringString: map[string]string{},
				},
			},
		},
		{
			name:    "select: non-proto field test has() cost",
			expr:    `has(input.testAttr.nestedAttr)`,
			vars:    []*decls.VariableDecl{decls.NewVariable("input", nestedMap)},
			want:    3,
			options: []CostTrackerOption{PresenceTestHasCost(true)},
			in: map[string]any{
				"input": map[string]any{
					"testAttr": map[string]any{
						"nestedAttr": "0",
					},
				},
			},
		},
		{
			name:    "select: non-proto field test no has() cost",
			expr:    `has(input.testAttr.nestedAttr)`,
			vars:    []*decls.VariableDecl{decls.NewVariable("input", nestedMap)},
			want:    2,
			options: []CostTrackerOption{PresenceTestHasCost(false)},
			in: map[string]any{
				"input": map[string]any{
					"testAttr": map[string]any{
						"nestedAttr": "0",
					},
				},
			},
		},
		{
			name: "select: non-proto field test",
			expr: `has(input.testAttr.nestedAttr)`,
			vars: []*decls.VariableDecl{decls.NewVariable("input", nestedMap)},
			want: 3,
			in: map[string]any{
				"input": map[string]any{
					"testAttr": map[string]any{
						"nestedAttr": "0",
					},
				},
			},
		},
		{
			name:         "estimated function call",
			expr:         `input.getFullYear()`,
			vars:         []*decls.VariableDecl{decls.NewVariable("input", types.TimestampType)},
			want:         8,
			in:           map[string]any{"input": time.Now()},
			testFuncCost: true,
		},
		{
			name: "create list",
			expr: `[1, 2, 3]`,
			want: 10,
		},
		{
			name: "create struct",
			expr: `google.expr.proto3.test.TestAllTypes{single_int32: 1, single_float: 3.14, single_string: 'str'}`,
			want: 40,
		},
		{
			name: "create map",
			expr: `{"a": 1, "b": 2, "c": 3}`,
			want: 30,
		},
		{
			name: "all comprehension",
			vars: []*decls.VariableDecl{decls.NewVariable("input", allList)},
			expr: `input.all(x, true)`,
			want: 2,
			in: map[string]any{
				"input": []*proto3pb.TestAllTypes{},
			},
		},
		{
			name: "nested all comprehension",
			vars: []*decls.VariableDecl{decls.NewVariable("input", nestedList)},
			expr: `input.all(x, x.all(y, true))`,
			want: 2,
			in: map[string]any{
				"input": []*proto3pb.TestAllTypes{},
			},
		},
		{
			name: "all comprehension on literal",
			expr: `[1, 2, 3].all(x, true)`,
			want: 20,
		},
		{
			name: "variable cost function",
			vars: []*decls.VariableDecl{decls.NewVariable("input", types.StringType)},
			expr: `input.matches('[0-9]')`,
			want: 103,
			in:   map[string]any{"input": string(randSeq(500))},
		},
		{
			name: "variable cost function with constant",
			expr: `'123'.matches('[0-9]')`,
			want: 2,
		},
		{
			name: "or",
			expr: `false || false`,
			want: 0,
		},
		{
			name: "or short-circuit",
			expr: `true || false`,
			want: 0,
		},

		{
			name: "or accumulated branch cost",
			expr: `a || b || c || d`,
			vars: []*decls.VariableDecl{
				decls.NewVariable("a", types.BoolType),
				decls.NewVariable("b", types.BoolType),
				decls.NewVariable("c", types.BoolType),
				decls.NewVariable("d", types.BoolType),
			},
			in: map[string]any{
				"a": false,
				"b": false,
				"c": false,
				"d": false,
			},
			want: 4,
		},
		{
			name: "and",
			expr: `true && false`,
			want: 0,
		},
		{
			name: "and short-circuit",
			expr: `false && true`,
			want: 0,
		},
		{
			name: "and accumulated branch cost",
			expr: `a && b && c && d`,
			vars: []*decls.VariableDecl{
				decls.NewVariable("a", types.BoolType),
				decls.NewVariable("b", types.BoolType),
				decls.NewVariable("c", types.BoolType),
				decls.NewVariable("d", types.BoolType),
			},
			in: map[string]any{
				"a": true,
				"b": true,
				"c": true,
				"d": true,
			},
			want: 4,
		},
		{
			name: "lt",
			expr: `1 < 2`,
			want: 1,
		},
		{
			name: "lte",
			expr: `1 <= 2`,
			want: 1,
		},
		{
			name: "eq",
			expr: `1 == 2`,
			want: 1,
		},
		{
			name: "gt",
			expr: `2 > 1`,
			want: 1,
		},
		{
			name: "gte",
			expr: `2 >= 1`,
			want: 1,
		},
		{
			name: "in",
			expr: `2 in [1, 2, 3]`,
			want: 13,
		},
		{
			name: "plus",
			expr: `1 + 1`,
			want: 1,
		},
		{
			name: "minus",
			expr: `1 - 1`,
			want: 1,
		},
		{
			name: "/",
			expr: `1 / 1`,
			want: 1,
		},
		{
			name: "/",
			expr: `1 * 1`,
			want: 1,
		},
		{
			name: "%",
			expr: `1 % 1`,
			want: 1,
		},
		{
			name: "ternary",
			expr: `true ? 1 : 2`,
			want: 0,
		},
		{
			name: "string size",
			expr: `size("123")`,
			want: 1,
		},
		{
			name: "str eq str",
			expr: `'12345678901234567890' == '123456789012345678901234567890'`,
			want: 2,
		},
		{
			name: "bytes to string conversion",
			vars: []*decls.VariableDecl{decls.NewVariable("input", types.BytesType)},
			expr: `string(input)`,
			want: 51,
			in:   map[string]any{"input": randSeq(500)},
		},
		{
			name: "string to bytes conversion",
			vars: []*decls.VariableDecl{decls.NewVariable("input", types.StringType)},
			expr: `bytes(input)`,
			want: 51,
			in:   map[string]any{"input": string(randSeq(500))},
		},
		{
			name: "int to string conversion",
			expr: `string(1)`,
			want: 1,
		},
		{
			name: "contains",
			expr: `input.contains(arg1)`,
			vars: []*decls.VariableDecl{
				decls.NewVariable("input", types.StringType),
				decls.NewVariable("arg1", types.StringType),
			},
			want: 2502,
			in:   map[string]any{"input": string(randSeq(500)), "arg1": string(randSeq(500))},
		},
		{
			name: "matches",
			expr: `input.matches('\\d+a\\d+b')`,
			vars: []*decls.VariableDecl{
				decls.NewVariable("input", types.StringType),
			},
			want: 103,
			in:   map[string]any{"input": string(randSeq(500)), "arg1": string(randSeq(500))},
		},
		{
			name: "matches global",
			expr: `matches(input, '\\d+a\\d+b')`,
			vars: []*decls.VariableDecl{
				decls.NewVariable("input", types.StringType),
			},
			want: 103,
			in:   map[string]any{"input": string(randSeq(500))},
		},
		{
			name: "startsWith",
			expr: `input.startsWith(arg1)`,
			vars: []*decls.VariableDecl{
				decls.NewVariable("input", types.StringType),
				decls.NewVariable("arg1", types.StringType),
			},
			want: 52,
			in:   map[string]any{"input": "idc", "arg1": string(randSeq(500))},
		},
		{
			name: "endsWith",
			expr: `input.endsWith(arg1)`,
			vars: []*decls.VariableDecl{
				decls.NewVariable("input", types.StringType),
				decls.NewVariable("arg1", types.StringType),
			},
			want: 52,
			in:   map[string]any{"input": "idc", "arg1": string(randSeq(500))},
		},
		{
			name: "size receiver",
			expr: `input.size()`,
			vars: []*decls.VariableDecl{
				decls.NewVariable("input", types.StringType),
			},
			want: 2,
			in:   map[string]any{"input": "500", "arg1": "500"},
		},
		{
			name: "size",
			expr: `size(input)`,
			vars: []*decls.VariableDecl{
				decls.NewVariable("input", types.StringType),
			},
			want: 2,
			in:   map[string]any{"input": "500", "arg1": "500"},
		},
		{
			name: "ternary eval",
			expr: `(x > 2 ? input1 : input2).all(y, true)`,
			vars: []*decls.VariableDecl{
				decls.NewVariable("x", types.IntType),
				decls.NewVariable("input1", allList),
				decls.NewVariable("input2", allList),
			},
			want: 6,
			in:   map[string]any{"input1": []*proto3pb.TestAllTypes{{}}, "input2": []*proto3pb.TestAllTypes{{}}, "x": 1},
		},
		{
			name: "ternary eval trivial, true",
			expr: `true ? false : 1 > 3`,
			want: 0,
			in:   map[string]any{},
		},
		{
			name: "ternary eval trivial, false",
			expr: `false ? false : 1 > 3`,
			want: 1,
			in:   map[string]any{},
		},
		{
			name: "comprehension over map",
			expr: `input.all(k, input[k].single_int32 > 3)`,
			vars: []*decls.VariableDecl{
				decls.NewVariable("input", allMap),
			},
			want: 9,
			in:   map[string]any{"input": map[string]any{"val": &proto3pb.TestAllTypes{}}},
		},
		{
			name: "comprehension over nested map of maps",
			expr: `input.all(k, input[k].all(x, true))`,
			vars: []*decls.VariableDecl{
				decls.NewVariable("input", nestedMap),
			},
			want: 2,
			in:   map[string]any{"input": map[string]any{}},
		},
		{
			name: "string size of map keys",
			expr: `input.all(k, k.contains(k))`,
			vars: []*decls.VariableDecl{
				decls.NewVariable("input", nestedMap),
			},
			want: 2,
			in:   map[string]any{"input": map[string]any{}},
		},
		{
			name: "comprehension variable shadowing",
			expr: `input.all(k, input[k].all(k, true) && k.contains(k))`,
			vars: []*decls.VariableDecl{
				decls.NewVariable("input", nestedMap),
			},
			want: 2,
			in:   map[string]any{"input": map[string]any{}},
		},
		{
			name: "comprehension variable shadowing",
			expr: `input.all(k, input[k].all(k, true) && k.contains(k))`,
			vars: []*decls.VariableDecl{
				decls.NewVariable("input", nestedMap),
			},
			want: 2,
			in:   map[string]any{"input": map[string]any{}},
		},
		{
			name: "list concat",
			expr: `(list1 + list2).all(x, true)`,
			vars: []*decls.VariableDecl{
				decls.NewVariable("list1", types.NewListType(types.IntType)),
				decls.NewVariable("list2", types.NewListType(types.IntType)),
			},
			want: 4,
			in:   map[string]any{"list1": []int{}, "list2": []int{}},
		},
		{
			name: "str concat",
			expr: `"abcdefg".contains(str1 + str2)`,
			vars: []*decls.VariableDecl{
				decls.NewVariable("str1", types.StringType),
				decls.NewVariable("str2", types.StringType),
			},
			want: 6,
			in:   map[string]any{"str1": "val1", "str2": "val2222222"},
		},
		{
			name: "str concat custom cost tracker",
			expr: `"abcdefg".contains(str1 + str2)`,
			vars: []*decls.VariableDecl{
				decls.NewVariable("str1", types.StringType),
				decls.NewVariable("str2", types.StringType),
			},
			options: []CostTrackerOption{
				OverloadCostTracker(overloads.ContainsString,
					func(args []ref.Val, result ref.Val) *uint64 {
						strCost := uint64(math.Ceil(float64(actualSize(args[0])) * 0.2))
						substrCost := uint64(math.Ceil(float64(actualSize(args[1])) * 0.2))
						cost := strCost * substrCost
						return &cost
					}),
			},
			want: 10,
			in:   map[string]any{"str1": "val1", "str2": "val2222222"},
		},
		{
			name: "at limit",
			expr: `"abcdefg".contains(str1 + str2)`,
			vars: []*decls.VariableDecl{
				decls.NewVariable("str1", types.StringType),
				decls.NewVariable("str2", types.StringType),
			},
			in:    map[string]any{"str1": "val1", "str2": "val2222222"},
			limit: 6,
			want:  6,
		},
		{
			name: "above limit",
			expr: `"abcdefg".contains(str1 + str2)`,
			vars: []*decls.VariableDecl{
				decls.NewVariable("str1", types.StringType),
				decls.NewVariable("str2", types.StringType),
			},
			in:                 map[string]any{"str1": "val1", "str2": "val2222222"},
			limit:              5,
			expectExceedsLimit: true,
		},
		{
			name: "ternary as operand",
			expr: `(1 > 2 ? 5 : 3) > 1`,
			vars: []*decls.VariableDecl{},
			in:   map[string]any{},
			want: 2,
		},
		{
			name: "ternary as operand",
			expr: `(1 > 2 || 2 > 1) == true`,
			vars: []*decls.VariableDecl{},
			in:   map[string]any{},
			want: 3,
		},
		{
			name: "list map literal",
			expr: `[{'k1': 1}, {'k2': 2}].all(x, true)`,
			vars: []*decls.VariableDecl{},
			in:   map[string]any{},
			want: 77,
		},
		{
			name: "list map literal",
			expr: `[{'k1': 1}, {'k2': 2}].all(x, true)`,
			vars: []*decls.VariableDecl{},
			in:   map[string]any{},
			want: 77,
		},
		{
			name: ".filter list literal",
			expr: `[1,2,3,4,5].filter(x, x % 2 == 0)`,
			vars: []*decls.VariableDecl{},
			in:   map[string]any{},
			want: 62,
		},
		{
			name: ".map list literal",
			expr: `[1,2,3,4,5].map(x, x)`,
			vars: []*decls.VariableDecl{},
			in:   map[string]any{},
			want: 86,
		},
		{
			name: ".map.filter list literal",
			expr: `[1,2,3,4,5].map(x, x).filter(x, x % 2 == 0)`,
			vars: []*decls.VariableDecl{},
			in:   map[string]any{},
			want: 138,
		},
		{
			name: ".map.exists list literal",
			expr: `[1,2,3,4,5].map(x, x).exists(x, x == 5) == true`,
			vars: []*decls.VariableDecl{},
			in:   map[string]any{},
			want: 118,
		},
		{
			name: ".map.map list literal",
			expr: `[1,2,3,4,5].map(x, x).map(x, x)`,
			vars: []*decls.VariableDecl{},
			in:   map[string]any{},
			want: 162,
		},
		{
			name: ".map.map list literal",
			expr: `[1,2,3,4,5].map(x, [x, x]).filter(z, z.size() == 2)`,
			vars: []*decls.VariableDecl{},
			in:   map[string]any{},
			want: 232,
		},
		{
			name: "comprehension on nested list",
			expr: `[1,2,3,4,5].map(x, [x, x]).all(y, y.all(y, y == 1))`,
			want: 171,
		},
		{
			name: "comprehension size",
			expr: `[1,2,3,4,5].map(x, x).map(x, x) + [1]`,
			want: 173,
		},
		{
			name: "nested comprehension",
			expr: `[1,2,3].all(i, i in [1,2,3].map(j, j + j))`,
			want: 86,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ctx := constructActivation(t, tc.in)
			var costLimit *uint64
			if tc.limit > 0 {
				costLimit = &tc.limit
			}
			options := tc.options
			if costLimit != nil {
				options = append(options, CostTrackerLimit(*costLimit))
			}
			actualCost, est, err := computeCost(t, tc.expr, tc.vars, ctx, options)
			if err != nil {
				if tc.expectExceedsLimit {
					return
				}
				t.Fatalf("Interpreter.Eval(activation Activation) failed due to: %v", err)
			}
			if tc.expectExceedsLimit {
				t.Fatalf("Interpreter.Eval(activation Activation) failed to return a cost exceeded error for limit %d, got cost %d", tc.limit, actualCost)
			}
			if actualCost != tc.want {
				t.Fatalf("Interpreter.Eval(activation Activation) failed to return expected runtime cost %d, got %d", tc.want, actualCost)
			}
			if est.Min > actualCost || est.Max < actualCost {
				t.Fatalf("Interpreter.Eval(activation Activation) failed to return cost in range of estimate cost [%d, %d], got %d",
					est.Min, est.Max, actualCost)
			}
		})
	}
}

func TestRefValStackDrop(t *testing.T) {
	tests := []struct {
		name string
		ids  []int64
		drop []int64
		want []int64
	}{
		{
			name: "found",
			ids:  []int64{1, 2, 3},
			drop: []int64{2},
			want: []int64{1},
		},
		{
			name: "not found",
			ids:  []int64{1, 2, 3},
			drop: []int64{4},
			want: []int64{1, 2, 3},
		},
		{
			name: "empty",
			drop: []int64{1},
		},
		{
			name: "topmost of repeated ids",
			ids:  []int64{1, 2, 1, 3},
			drop: []int64{1},
			want: []int64{1, 2},
		},
		{
			name: "repeated ids one by one",
			ids:  []int64{1, 2, 1, 3},
			drop: []int64{1, 1},
		},
		{
			name: "repeated id past the top",
			ids:  []int64{1, 2, 1, 3},
			drop: []int64{2, 1},
		},
		{
			name: "ids in order",
			ids:  []int64{1, 2, 3, 4},
			drop: []int64{4, 2},
			want: []int64{1},
		},
		{
			name: "ids out of order",
			ids:  []int64{1, 2, 3, 4},
			drop: []int64{2, 4},
			want: []int64{1},
		},
		{
			name: "missing id between found ids",
			ids:  []int64{1, 2, 3, 4},
			drop: []int64{3, 5, 2},
			want: []int64{1},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			for _, indexed := range []bool{false, true} {
				s := newTestRefValStack(indexed, tc.ids...)
				s.drop(tc.drop...)
				if got := refValStackIDs(s); !slices.Equal(got, tc.want) {
					t.Errorf("drop(%v) with index %v got %v, wanted %v", tc.drop, indexed, got, tc.want)
				}
			}
		})
	}
}

func TestRefValStackDropArgs(t *testing.T) {
	tests := []struct {
		name     string
		ids      []int64
		args     []int64
		want     []int64
		wantVals []ref.Val
		wantOK   bool
	}{
		{
			name:     "found",
			ids:      []int64{1, 2, 3},
			args:     []int64{1, 2},
			want:     []int64{},
			wantVals: []ref.Val{types.Int(0), types.Int(1)},
			wantOK:   true,
		},
		{
			name:     "above the args",
			ids:      []int64{1, 2, 3, 4},
			args:     []int64{2},
			want:     []int64{1},
			wantVals: []ref.Val{types.Int(1)},
			wantOK:   true,
		},
		{
			name:     "no args",
			ids:      []int64{1, 2},
			want:     []int64{1, 2},
			wantVals: []ref.Val{},
			wantOK:   true,
		},
		{
			name:     "topmost of repeated ids",
			ids:      []int64{1, 2, 1, 3},
			args:     []int64{2, 1},
			want:     []int64{1},
			wantVals: []ref.Val{types.Int(1), types.Int(2)},
			wantOK:   true,
		},
		{
			name:     "repeated args",
			ids:      []int64{1, 2, 1, 3},
			args:     []int64{1, 1},
			want:     []int64{},
			wantVals: []ref.Val{types.Int(0), types.Int(2)},
			wantOK:   true,
		},
		{
			name:   "not found",
			ids:    []int64{1, 2, 3},
			args:   []int64{4},
			want:   []int64{1, 2, 3},
			wantOK: false,
		},
		{
			name:   "last arg removed before the missing one",
			ids:    []int64{1, 2, 3, 4},
			args:   []int64{1, 5, 3},
			want:   []int64{1, 2},
			wantOK: false,
		},
		{
			name:   "args out of order",
			ids:    []int64{1, 2, 3},
			args:   []int64{2, 1},
			want:   []int64{},
			wantOK: false,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			args := make([]InterpretableV2, len(tc.args))
			for i, id := range tc.args {
				args[i] = NewConstValue(id, types.NullValue)
			}
			for _, indexed := range []bool{false, true} {
				s := newTestRefValStack(indexed, tc.ids...)
				vals, ok := s.dropArgs(args)
				if ok != tc.wantOK {
					t.Errorf("dropArgs(%v) with index %v got ok %v, wanted %v", tc.args, indexed, ok, tc.wantOK)
				}
				if !reflect.DeepEqual(vals, tc.wantVals) {
					t.Errorf("dropArgs(%v) with index %v got values %v, wanted %v", tc.args, indexed, vals, tc.wantVals)
				}
				if got := refValStackIDs(s); !slices.Equal(got, tc.want) {
					t.Errorf("dropArgs(%v) with index %v left %v, wanted %v", tc.args, indexed, got, tc.want)
				}
			}
		})
	}
}

func TestRefValStackPushAfterDrop(t *testing.T) {
	for _, indexed := range []bool{false, true} {
		s := newTestRefValStack(indexed, 1, 2, 3)
		s.drop(2)
		s.push(types.Int(3), 2)
		s.push(types.Int(4), 1)
		s.push(types.Int(5), 3)
		if got, want := refValStackIDs(s), []int64{1, 2, 1, 3}; !slices.Equal(got, want) {
			t.Fatalf("with index %v got %v, wanted %v", indexed, got, want)
		}
		s.drop(1)
		if got, want := refValStackIDs(s), []int64{1, 2}; !slices.Equal(got, want) {
			t.Fatalf("drop(1) with index %v got %v, wanted %v", indexed, got, want)
		}
		s.drop(3)
		if got, want := refValStackIDs(s), []int64{1, 2}; !slices.Equal(got, want) {
			t.Fatalf("drop(3) with index %v got %v, wanted %v", indexed, got, want)
		}
		s.drop(1)
		if got := refValStackIDs(s); len(got) != 0 {
			t.Fatalf("drop(1) with index %v got %v, wanted an empty stack", indexed, got)
		}
	}
}

// TestRefValStackIndexRepeatedIDs indexes a stack whose entries repeat IDs, up to and including
// the entry which makes the stack indexed.
func TestRefValStackIndexRepeatedIDs(t *testing.T) {
	s := &refValStack{}
	var want []stackVal
	for i := 0; i < refValStackIndexSize; i++ {
		id := int64(1 + i%2)
		if i == refValStackIndexSize-1 {
			id = 1
		}
		if s.top != nil {
			t.Fatalf("got an index with %d entries, wanted one with %d", len(s.vals), refValStackIndexSize)
		}
		s.push(types.Int(i), id)
		want = append(want, stackVal{Val: types.Int(i), ID: id})
	}
	if s.top == nil {
		t.Fatalf("got no index with %d entries, wanted one", len(s.vals))
	}
	check := func(step string) {
		t.Helper()
		if err := compareRefValStack(s, want); err != "" {
			t.Fatalf("%s: %s", step, err)
		}
	}
	check("pushes")
	for i, ids := range [][]int64{{1}, {1}, {1}, {2}, {2, 1}, {1, 2, 1}} {
		s.drop(ids...)
		want = dropFromList(want, ids...)
		check(fmt.Sprintf("drop(%v) #%d", ids, i))
	}
	for i := 0; i < 4; i++ {
		s.push(types.Int(1000+i), int64(1+i%2))
		want = append(want, stackVal{Val: types.Int(1000 + i), ID: int64(1 + i%2)})
	}
	args := []InterpretableV2{NewConstValue(2, types.NullValue), NewConstValue(1, types.NullValue)}
	vals, ok := s.dropArgs(args)
	wantVals, wantOK, rest := dropArgsFromList(want, 2, 1)
	want = rest
	if ok != wantOK || !reflect.DeepEqual(vals, wantVals) {
		t.Fatalf("dropArgs got (%v, %v), wanted (%v, %v)", vals, ok, wantVals, wantOK)
	}
	check("dropArgs")
	s.drop(1)
	want = dropFromList(want, 1)
	check("drop(1) at the end")
}

// TestRefValStackMatchesSearch compares the stack to a search of a plain list of entries for the
// topmost entry with an ID.
func TestRefValStackMatchesSearch(t *testing.T) {
	rnd := rand.New(rand.NewSource(1))
	s := &refValStack{}
	var want []stackVal
	randomIDs := func() []int64 {
		ids := make([]int64, 1+rnd.Intn(3))
		for i := range ids {
			// Some of the IDs are not on the stack.
			ids[i] = int64(rnd.Intn(10))
		}
		return ids
	}
	maxLen := 0
	for step := 0; step < 24000; step++ {
		// The stack grows for a while, then shrinks.
		pushes := 18
		if step%6000 >= 3000 {
			pushes = 15
		}
		switch op := rnd.Intn(20); {
		case op < pushes:
			id := int64(rnd.Intn(6))
			s.push(types.Int(step), id)
			want = append(want, stackVal{Val: types.Int(step), ID: id})
		case op < (pushes+20)/2:
			ids := randomIDs()
			s.drop(ids...)
			want = dropFromList(want, ids...)
		default:
			ids := randomIDs()
			args := make([]InterpretableV2, len(ids))
			for i, id := range ids {
				args[i] = NewConstValue(id, types.NullValue)
			}
			vals, ok := s.dropArgs(args)
			wantVals, wantOK, rest := dropArgsFromList(want, ids...)
			want = rest
			if ok != wantOK || !reflect.DeepEqual(vals, wantVals) {
				t.Fatalf("step %d: dropArgs(%v) got (%v, %v), wanted (%v, %v)", step, ids, vals, ok, wantVals, wantOK)
			}
		}
		maxLen = max(maxLen, len(want))
		if err := compareRefValStack(s, want); err != "" {
			t.Fatalf("step %d: %s", step, err)
		}
	}
	if maxLen < 2*refValStackIndexSize {
		t.Errorf("the stack grew to %d entries, wanted more than %d to be indexed for a while", maxLen, 2*refValStackIndexSize)
	}
}

// dropFromList removes the topmost entry with each ID and the entries above it from a list of
// entries, which is how the stack looked for the IDs to drop before it was indexed.
func dropFromList(list []stackVal, ids ...int64) []stackVal {
	for _, id := range ids {
		for idx := len(list) - 1; idx >= 0; idx-- {
			if list[idx].ID == id {
				list = list[:idx]
				break
			}
		}
	}
	return list
}

// dropArgsFromList is dropFromList for the args of a call, which returns the values of the entries
// it removes, and false if any of the IDs is not found.
func dropArgsFromList(list []stackVal, ids ...int64) ([]ref.Val, bool, []stackVal) {
	vals := make([]ref.Val, len(ids))
args:
	for i := len(ids) - 1; i >= 0; i-- {
		for idx := len(list) - 1; idx >= 0; idx-- {
			if list[idx].ID == ids[i] {
				vals[i] = list[idx].Val
				list = list[:idx]
				continue args
			}
		}
		return nil, false, list
	}
	return vals, true, list
}

// compareRefValStack returns a description of the difference between the entries of the stack and
// a list of entries, or an empty string.
func compareRefValStack(s *refValStack, want []stackVal) string {
	if len(s.vals) != len(want) {
		return fmt.Sprintf("got %d entries, wanted %d", len(s.vals), len(want))
	}
	for i, el := range want {
		if s.vals[i] != el {
			return fmt.Sprintf("got entry %d of %v, wanted %v", i, s.vals[i], el)
		}
	}
	return ""
}

// newTestRefValStack returns a stack holding an entry for each ID, whose value is the position of
// the entry in the stack. When indexed is true, the entries lie on top of enough other entries
// for the stack to be indexed.
func newTestRefValStack(indexed bool, ids ...int64) *refValStack {
	s := &refValStack{}
	if indexed {
		for i := 0; i < refValStackIndexSize; i++ {
			s.push(types.Int(-1), int64(1000+i))
		}
	}
	for i, id := range ids {
		s.push(types.Int(i), id)
	}
	return s
}

// refValStackIDs returns the IDs of the entries of a stack made by newTestRefValStack, but for
// the ones below them.
func refValStackIDs(s *refValStack) []int64 {
	ids := []int64{}
	for _, el := range s.vals {
		if el.ID < 1000 {
			ids = append(ids, el.ID)
		}
	}
	return ids
}

// TestCostTrackerStackSteps checks that the work of the cost tracker's value stack grows linearly
// with the number of iterations of a comprehension. The iterations leave entries on the stack until
// the loop ends, so a search of the whole stack for an ID which is not there makes it quadratic.
func TestCostTrackerStackSteps(t *testing.T) {
	tests := []string{
		`l.exists(i, i < 0)`,
		`l.all(i, i >= 0)`,
		`l.exists_one(i, i < 0)`,
		`l.filter(i, i < 0).size() == 0`,
		`l.map(i, i + 1).size() == 0`,
		`l.map(i, i >= 0, i + 1).size() == 0`,
		`l.exists(i, i < 0 && i % 2 == 0)`,
		`l.exists(i, [i, i].size() < 0)`,
	}
	// Each iteration leaves two entries, so the stack grows far beyond the size at which it is indexed.
	const n = 4 * refValStackIndexSize
	for _, expr := range tests {
		t.Run(expr, func(t *testing.T) {
			small := costTrackerSteps(t, expr, n)
			large := costTrackerSteps(t, expr, 10*n)
			// Ten times the iterations should take ten times the steps. Allow for the setup and
			// teardown of the loop.
			if small == 0 || large > 11*small {
				t.Errorf("got %d steps for %d elements and %d steps for %d elements, wanted a linear growth",
					small, n, large, 10*n)
			}
		})
	}
}

// costTrackerSteps evaluates expr over a list `l` of n integers and returns the number of stack
// entries the cost tracker looked up or removed.
func costTrackerSteps(t *testing.T, expr string, n int) int {
	t.Helper()

	s := common.NewTextSource(expr)
	p, err := parser.NewParser(parser.Macros(parser.AllMacros...))
	if err != nil {
		t.Fatalf("Failed to initialize parser: %v", err)
	}
	parsed, errs := p.Parse(s)
	if len(errs.GetErrors()) != 0 {
		t.Fatalf(`Failed to Parse expression "%s", error: %v`, expr, errs.GetErrors())
	}
	cont := containers.DefaultContainer
	reg := newTestRegistry(t)
	env := newTestEnv(t, cont, reg)
	err = env.AddIdents(decls.NewVariable("l", types.NewListType(types.IntType)))
	if err != nil {
		t.Fatalf("Failed to initialize env: %v", err)
	}
	checked, errs := checker.Check(parsed, s, env)
	if len(errs.GetErrors()) != 0 {
		t.Fatalf(`Failed to check expression "%s", error: %v`, expr, errs.GetErrors())
	}
	costTracker, err := NewCostTracker(nil)
	if err != nil {
		t.Fatalf("NewCostTracker() failed: %v", err)
	}
	interp := newStandardInterpreter(t, cont, reg, reg, NewAttributeFactory(cont, reg, reg))
	prg, err := interp.NewInterpretable(checked,
		CostObserver(CostTrackerFactory(func() (*CostTracker, error) {
			return costTracker, nil
		})))
	if err != nil {
		t.Fatalf(`Failed to plan expression "%s", error: %v`, expr, err)
	}
	l := make([]int64, n)
	for i := range l {
		l[i] = int64(i)
	}
	prg.Exec(AsFrame(constructActivation(t, map[string]any{"l": l})))
	return costTracker.stack.steps
}
