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

package parser2

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/goleak"

	"github.com/onflow/cadence/runtime/ast"
	"github.com/onflow/cadence/runtime/parser2/lexer"
	"github.com/onflow/cadence/runtime/tests/utils"
)

func TestMain(m *testing.M) {
	goleak.VerifyTestMain(m)
}

func TestParseInvalid(t *testing.T) {

	t.Parallel()

	_, errs := ParseExpression("#")
	require.NotEmpty(t, errs)
}

func TestParseBuffering(t *testing.T) {

	t.Parallel()

	t.Run("buffer and accept, valid", func(t *testing.T) {

		t.Parallel()

		_, errs := Parse("a b c d", func(p *parser) interface{} {
			p.mustOneString(lexer.TokenIdentifier, "a")
			p.mustOne(lexer.TokenSpace)

			p.startBuffering()

			p.mustOneString(lexer.TokenIdentifier, "b")
			p.mustOne(lexer.TokenSpace)
			p.mustOneString(lexer.TokenIdentifier, "c")

			p.acceptBuffered()

			p.mustOne(lexer.TokenSpace)
			p.mustOneString(lexer.TokenIdentifier, "d")

			return nil
		})

		assert.Empty(t, errs)
	})

	t.Run("buffer and accept, invalid", func(t *testing.T) {

		t.Parallel()

		_, errs := Parse("a b x d", func(p *parser) interface{} {
			p.mustOneString(lexer.TokenIdentifier, "a")
			p.mustOne(lexer.TokenSpace)

			p.startBuffering()

			p.mustOneString(lexer.TokenIdentifier, "b")
			p.mustOne(lexer.TokenSpace)
			p.mustOneString(lexer.TokenIdentifier, "c")

			p.acceptBuffered()

			p.mustOne(lexer.TokenSpace)
			p.mustOneString(lexer.TokenIdentifier, "d")

			return nil
		})

		utils.AssertEqualWithDiff(t,
			[]error{
				&SyntaxError{
					Message: "expected token identifier with string value c",
					Pos:     ast.Position{Offset: 4, Line: 1, Column: 4},
				},
			},
			errs,
		)
	})

	t.Run("buffer and replay, valid", func(t *testing.T) {

		t.Parallel()

		_, errs := Parse("a b c d", func(p *parser) interface{} {
			p.mustOneString(lexer.TokenIdentifier, "a")
			p.mustOne(lexer.TokenSpace)

			p.startBuffering()

			p.mustOneString(lexer.TokenIdentifier, "b")
			p.mustOne(lexer.TokenSpace)
			p.mustOneString(lexer.TokenIdentifier, "c")

			p.replayBuffered()

			p.mustOneString(lexer.TokenIdentifier, "b")
			p.mustOne(lexer.TokenSpace)
			p.mustOneString(lexer.TokenIdentifier, "c")
			p.mustOne(lexer.TokenSpace)
			p.mustOneString(lexer.TokenIdentifier, "d")

			return nil
		})

		assert.Empty(t, errs)
	})

	t.Run("buffer and replay, invalid first", func(t *testing.T) {

		t.Parallel()

		_, errs := Parse("a b c d", func(p *parser) interface{} {
			p.mustOneString(lexer.TokenIdentifier, "a")
			p.mustOne(lexer.TokenSpace)

			p.startBuffering()

			firstSucceeded := false
			firstFailed := false

			(func() {
				defer func() {
					if r := recover(); r != nil {
						firstFailed = true
					}
				}()

				p.mustOneString(lexer.TokenIdentifier, "x")
				p.mustOne(lexer.TokenSpace)
				p.mustOneString(lexer.TokenIdentifier, "c")

				firstSucceeded = true
			})()

			assert.True(t, firstFailed)
			assert.False(t, firstSucceeded)

			p.replayBuffered()

			p.mustOneString(lexer.TokenIdentifier, "b")
			p.mustOne(lexer.TokenSpace)
			p.mustOneString(lexer.TokenIdentifier, "c")
			p.mustOne(lexer.TokenSpace)
			p.mustOneString(lexer.TokenIdentifier, "d")

			return nil
		})

		assert.Empty(t, errs)
	})

	t.Run("buffer and replay, invalid first and invalid second", func(t *testing.T) {

		t.Parallel()

		_, errs := Parse("a b c x", func(p *parser) interface{} {
			p.mustOneString(lexer.TokenIdentifier, "a")
			p.mustOne(lexer.TokenSpace)

			p.startBuffering()

			firstSucceeded := false
			firstFailed := false

			(func() {
				defer func() {
					if r := recover(); r != nil {
						firstFailed = true
					}
				}()

				p.mustOneString(lexer.TokenIdentifier, "x")
				p.mustOne(lexer.TokenSpace)
				p.mustOneString(lexer.TokenIdentifier, "c")

				firstSucceeded = true
			})()

			assert.True(t, firstFailed)
			assert.False(t, firstSucceeded)

			p.replayBuffered()

			p.mustOneString(lexer.TokenIdentifier, "b")
			p.mustOne(lexer.TokenSpace)
			p.mustOneString(lexer.TokenIdentifier, "c")
			p.mustOne(lexer.TokenSpace)
			p.mustOneString(lexer.TokenIdentifier, "d")

			return nil
		})

		utils.AssertEqualWithDiff(t,
			[]error{
				&SyntaxError{
					Message: "expected token identifier with string value d",
					Pos:     ast.Position{Offset: 6, Line: 1, Column: 6},
				},
			},
			errs,
		)
	})
}

func TestParseEOF(t *testing.T) {

	t.Parallel()

	_, errs := Parse("a b", func(p *parser) interface{} {
		p.mustOneString(lexer.TokenIdentifier, "a")
		p.skipSpaceAndComments(true)
		p.mustOneString(lexer.TokenIdentifier, "b")

		p.next()

		assert.Equal(t,
			lexer.Token{
				Type: lexer.TokenEOF,
				Range: ast.Range{
					StartPos: ast.Position{Offset: 3, Line: 1, Column: 3},
					EndPos:   ast.Position{Offset: 3, Line: 1, Column: 3},
				},
			},
			p.current,
		)

		p.next()

		assert.Equal(t,
			lexer.Token{
				Type: lexer.TokenEOF,
				Range: ast.Range{
					StartPos: ast.Position{Offset: 3, Line: 1, Column: 3},
					EndPos:   ast.Position{Offset: 3, Line: 1, Column: 3},
				},
			},
			p.current,
		)

		return nil
	})

	assert.Empty(t, errs)
}
