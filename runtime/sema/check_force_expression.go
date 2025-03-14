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

package sema

import (
	"github.com/onflow/cadence/runtime/ast"
)

func (checker *Checker) VisitForceExpression(expression *ast.ForceExpression) ast.Repr {

	valueType := expression.Expression.Accept(checker).(Type)

	if valueType.IsInvalidType() {
		return valueType
	}

	checker.recordResourceInvalidation(
		expression.Expression,
		valueType,
		ResourceInvalidationKindMoveDefinite,
	)

	optionalType, ok := valueType.(*OptionalType)
	if !ok {
		checker.report(
			&NonOptionalForceError{
				Type:  valueType,
				Range: ast.NewRangeFromPositioned(expression.Expression),
			},
		)

		return valueType
	}

	return optionalType.Type
}
