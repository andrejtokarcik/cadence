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

package trampoline

import (
	"strconv"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestFlatMapDone(t *testing.T) {

	t.Parallel()

	trampoline := Done{23}.
		FlatMap(func(value interface{}) Trampoline {
			number := value.(int)
			return Done{number * 42}
		})

	assert.Equal(t, Run(trampoline), 23*42)
}

func TestFlatMapMore(t *testing.T) {

	t.Parallel()

	trampoline :=
		More(func() Trampoline { return Done{23} }).
			FlatMap(func(value interface{}) Trampoline {
				number := value.(int)
				return Done{number * 42}
			})

	assert.Equal(t, Run(trampoline), 23*42)
}

func TestFlatMap2(t *testing.T) {

	t.Parallel()

	trampoline :=
		More(func() Trampoline { return Done{23} }).
			FlatMap(func(value interface{}) Trampoline {
				n := value.(int)
				return More(func() Trampoline {
					return Done{strconv.Itoa(n)}
				})
			}).
			FlatMap(func(value interface{}) Trampoline {
				str := value.(string)
				return Done{str + "42"}
			})

	assert.Equal(t, Run(trampoline), "2342")
}

func TestFlatMap3(t *testing.T) {

	t.Parallel()

	trampoline :=
		More(func() Trampoline {
			return Done{23}.
				FlatMap(func(value interface{}) Trampoline {
					n := value.(int)
					return Done{n * 42}
				})
		}).
			FlatMap(func(value interface{}) Trampoline {
				n := value.(int)
				return Done{strconv.Itoa(n)}
			})

	assert.Equal(t, Run(trampoline), strconv.Itoa(23*42))
}

func TestMap(t *testing.T) {

	t.Parallel()

	trampoline :=
		More(func() Trampoline { return Done{23} }).
			Map(func(value interface{}) interface{} {
				n := value.(int)
				return n * 42
			})

	assert.Equal(t, Run(trampoline), 23*42)
}

func TestMap2(t *testing.T) {

	t.Parallel()

	trampoline :=
		Done{23}.
			Map(func(value interface{}) interface{} {
				n := value.(int)
				return n * 42
			})

	assert.Equal(t, Run(trampoline), 23*42)
}

func TestEvenOdd(t *testing.T) {

	t.Parallel()

	var even, odd func(n interface{}) Trampoline

	even = func(value interface{}) Trampoline {
		n := value.(int)
		if n == 0 {
			return Done{true}
		}

		return More(func() Trampoline {
			return odd(n - 1)
		})
	}

	odd = func(value interface{}) Trampoline {
		n := value.(int)
		if n == 0 {
			return Done{false}
		}

		return More(func() Trampoline {
			return even(n - 1)
		})
	}

	assert.True(t, Run(odd(99999)).(bool))

	assert.True(t, Run(even(100000)).(bool))

	assert.False(t, Run(odd(100000)).(bool))

	assert.False(t, Run(even(99999)).(bool))
}

func TestAckermann(t *testing.T) {

	t.Parallel()

	// The recursive implementation of the Ackermann function
	// results in a stack overflow even for small inputs:
	//
	//  func ackermann(m, n int) int {
	//  	if m <= 0 {
	//  		return n + 1
	//  	}
	//
	//  	if n <= 0 {
	//  		return ackermann(m-1, 1)
	//  	}
	//
	//  	x := ackermann(m, n-1)
	//  	return ackermann(m-1, x)
	//  }
	//
	// The following version uses trampolines to avoid
	// the overflow:

	var ackermann func(m, n int) Trampoline

	ackermann = func(m, n int) Trampoline {
		if m <= 0 {
			return Done{n + 1}
		}
		if n <= 0 {
			return More(func() Trampoline {
				return ackermann(m-1, 1)
			})
		}
		first := More(func() Trampoline {
			return ackermann(m, n-1)
		})
		second := func(value interface{}) Trampoline {
			x := value.(int)
			return More(func() Trampoline {
				return ackermann(m-1, x)
			})
		}
		return first.FlatMap(second)
	}

	assert.Equal(t, Run(ackermann(1, 2)), 4)

	assert.Equal(t, Run(ackermann(3, 2)), 29)

	assert.Equal(t, Run(ackermann(3, 4)), 125)

	assert.Equal(t, Run(ackermann(3, 7)), 1021)
}

func TestDoneResume(t *testing.T) {

	add1 := func(v interface{}) interface{} {
		return v.(int) + 1
	}

	// Resume a Done trampline will return the result immediately
	assert.Equal(t, 3, Done{3}.Resume())

	// getting result by running a trampline
	assert.Equal(t, 3, Run(Done{3}))

	// Run a trampline that starts from a Done and maps over a function
	assert.Equal(t, 4, Run(Done{3}.Map(add1)))

	// Resume twices to get the result from a trampline that maps a function over a Done value
	assert.Equal(t, 4, Done{3}.
		Map(add1).
		Resume().(func() Trampoline)().
		Resume())

	// Resume one more time if mapped over a function one more time
	assert.Equal(t, 5, Done{3}.
		Map(add1).
		Map(add1).
		Resume().(func() Trampoline)().
		Resume().(func() Trampoline)().
		Resume())

	// Map-Resume-Map-Resume returns the same result as Map-Map-Resume-Resume
	assert.Equal(t, 5, Done{3}.
		Map(add1).
		Resume().(func() Trampoline)().
		Map(add1).
		Resume().(func() Trampoline)().
		Resume())
}
