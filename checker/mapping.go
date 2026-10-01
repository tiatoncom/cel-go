// Copyright 2018 Google LLC
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
	"cel.dev/cel-go/common/types"
)

type mapping struct {
	mapping map[string]*types.Type

	// undo holds the bindings which add has replaced since the last call to begin.
	undo []binding
}

// binding is the type, if any, which a key mapped to before it was set.
type binding struct {
	key  string
	prev *types.Type
}

func newMapping() *mapping {
	return &mapping{
		mapping: make(map[string]*types.Type),
	}
}

func (m *mapping) add(from, to *types.Type) {
	key := FormatCELType(from)
	m.undo = append(m.undo, binding{key: key, prev: m.mapping[key]})
	m.mapping[key] = to
}

func (m *mapping) find(from *types.Type) (*types.Type, bool) {
	if r, found := m.mapping[FormatCELType(from)]; found {
		return r, found
	}
	return nil, false
}

// begin starts recording the additions to the mapping so that they can be undone.
func (m *mapping) begin() {
	m.undo = m.undo[:0]
}

// rollback reverts the additions made since the last call to begin.
func (m *mapping) rollback() {
	for i := len(m.undo) - 1; i >= 0; i-- {
		b := m.undo[i]
		if b.prev == nil {
			delete(m.mapping, b.key)
		} else {
			m.mapping[b.key] = b.prev
		}
	}
	m.undo = m.undo[:0]
}
