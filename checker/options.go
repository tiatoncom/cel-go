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

package checker

import "fmt"

type options struct {
	crossTypeNumericComparisons  bool
	homogeneousAggregateLiterals bool
	validatedDeclarations        *Scopes
	jsonFieldNames               bool
	maxTypeSize                  int
}

// Option is a functional option for configuring the type-checker
type Option func(*options) error

// CrossTypeNumericComparisons toggles type-checker support for numeric comparisons across type
// See https://github.com/google/cel-spec/wiki/proposal-210 for more details.
func CrossTypeNumericComparisons(enabled bool) Option {
	return func(opts *options) error {
		opts.crossTypeNumericComparisons = enabled
		return nil
	}
}

// ValidatedDeclarations provides a reference to validated declarations which will be inherited
// as a parent scope without copying.
func ValidatedDeclarations(env *Env) Option {
	return func(opts *options) error {
		opts.validatedDeclarations = env.validatedDeclarations()
		return nil
	}
}

// JSONFieldNames enables the use of json names instead of the standard protobuf snake_case field names
func JSONFieldNames(enabled bool) Option {
	return func(opts *options) error {
		opts.jsonFieldNames = enabled
		return nil
	}
}

// MaxTypeSize bounds the unfolded size of a type the checker builds: the number of nodes of
// the tree the type stands for, counted over the shared structure with memory (a child that
// appears twice is walked once and counted twice), so that counting a type whose every level
// doubles costs its shared structure, not the tree it unfolds to. A composite type that would
// carry the count over n is not built: the expression reports one issue of its own and the
// node takes the error type. n <= 0 disables the bound, which is the default.
func MaxTypeSize(n int) Option {
	return func(opts *options) error {
		if n < 0 {
			return fmt.Errorf("max type size cannot be negative: %d", n)
		}
		opts.maxTypeSize = n
		return nil
	}
}
