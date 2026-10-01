// Copyright 2026 Google LLC
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
	"testing"

	"cel.dev/cel-go/common/types"
)

func TestIsAssignableMapping(t *testing.T) {
	tType := types.NewTypeParamType("T")
	uType := types.NewTypeParamType("U")
	intList := types.NewListType(types.IntType)
	dynList := types.NewListType(types.DynType)
	tests := []struct {
		name string
		// before are the substitutions the mapping starts with.
		before map[*types.Type]*types.Type
		l1, l2 []*types.Type
		want   bool
		// after are the substitutions the mapping ends with.
		after map[*types.Type]*types.Type
	}{
		{
			name: "success adds substitutions",
			l1:   []*types.Type{types.IntType, types.StringType},
			l2:   []*types.Type{tType, uType},
			want: true,
			after: map[*types.Type]*types.Type{
				tType: types.IntType,
				uType: types.StringType,
			},
		},
		{
			name: "failure removes added substitutions",
			l1:   []*types.Type{types.IntType, types.StringType, types.BoolType},
			l2:   []*types.Type{tType, uType, types.IntType},
			want: false,
		},
		{
			name:   "failure keeps earlier substitutions",
			before: map[*types.Type]*types.Type{tType: types.IntType},
			l1:     []*types.Type{types.StringType, types.BoolType},
			l2:     []*types.Type{uType, types.IntType},
			want:   false,
			after:  map[*types.Type]*types.Type{tType: types.IntType},
		},
		{
			name:   "success replaces substitution",
			before: map[*types.Type]*types.Type{tType: intList},
			l1:     []*types.Type{dynList},
			l2:     []*types.Type{tType},
			want:   true,
			after:  map[*types.Type]*types.Type{tType: dynList},
		},
		{
			name: "failure removes substitution replaced after it was added",
			l1:   []*types.Type{intList, dynList, types.StringType},
			l2:   []*types.Type{tType, tType, types.IntType},
			want: false,
		},
		{
			name:   "failure restores replaced substitution",
			before: map[*types.Type]*types.Type{tType: intList},
			l1:     []*types.Type{dynList, types.StringType},
			l2:     []*types.Type{tType, types.IntType},
			want:   false,
			after:  map[*types.Type]*types.Type{tType: intList},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			m := newMapping()
			for from, to := range tc.before {
				m.add(from, to)
			}
			if got := isAssignableList(m, tc.l1, tc.l2); got != tc.want {
				t.Fatalf("isAssignableList(%v, %v) got %v, wanted %v", tc.l1, tc.l2, got, tc.want)
			}
			if len(m.mapping) != len(tc.after) {
				t.Errorf("got %d substitutions, wanted %d", len(m.mapping), len(tc.after))
			}
			for from, want := range tc.after {
				got, found := m.find(from)
				if !found || !got.IsExactType(want) {
					t.Errorf("find(%v) got %v, wanted %v", from, got, want)
				}
			}
		})
	}
}

func TestIsAssignableMappingRetry(t *testing.T) {
	tType := types.NewTypeParamType("T")
	m := newMapping()
	if isAssignable(m, types.NewListType(types.IntType), types.NewListType(tType)) != true {
		t.Fatal("isAssignable(list(int), list(T)) got false, wanted true")
	}
	// A failed assignment must not undo the substitutions of an earlier one.
	if isAssignable(m, types.NewListType(types.StringType), types.NewListType(tType)) {
		t.Fatal("isAssignable(list(string), list(T)) got true, wanted false")
	}
	if got, found := m.find(tType); !found || !got.IsExactType(types.IntType) {
		t.Errorf("find(T) got %v, wanted int", got)
	}
}
