/*
 * Cadence - The resource-oriented smart contract programming language
 *
 * Copyright 2019-2020 Dapper Labs, Inc.
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *   http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 */

package interpreter

import (
	"github.com/onflow/cadence/runtime/trampoline"
)

// StatementTrampoline is useful for breakpoint debugging, which pauses the
// `Interpreter`'s interpretation work at the `Line` of the statement, and wraps
// the paused interpretation into `F`.
type StatementTrampoline struct {
	F           func() trampoline.Trampoline
	Interpreter *Interpreter
	Line        int
}

// Resume returns the paused computation
func (m StatementTrampoline) Resume() interface{} {
	return m.F
}

// FlatMap chains the StatementTrampoline with another trampoline
func (m StatementTrampoline) FlatMap(f func(interface{}) trampoline.Trampoline) trampoline.Trampoline {
	return trampoline.FlatMap{Subroutine: m, Continuation: f}
}

func (m StatementTrampoline) Map(f func(interface{}) interface{}) trampoline.Trampoline {
	return trampoline.MapTrampoline(m, f)
}

func (m StatementTrampoline) Then(f func(interface{})) trampoline.Trampoline {
	return trampoline.ThenTrampoline(m, f)
}

// Continue continues the paused interpretation
func (m StatementTrampoline) Continue() trampoline.Trampoline {
	return m.F()
}
