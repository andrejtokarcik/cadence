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
	"github.com/onflow/cadence/runtime/common"
)

// Import declarations are handled in two phases:
//
// 1. Resolution of the import statement.
//
//     The default case is that one location is resolved to one location (itself),
//     though e.g. an address location may also be resolved into multiple "address contract" locations.
//
//     For example, an import declaration `import a, b from 0x1` specifies the import of the declarations with
//     the identifiers `a` and `b` from the address location `0x1`.
//     This import declaration might be resolved into just the location itself, i.e. the address location `0x1`,
//     but could also be resolved into multiple locations, e.g. the address contract locations `0x1.a` and `0x1.b`.
//
// 2. Acquiring the programs for the resolved imports. For each resolved import a separate program can be returned.
//

func (checker *Checker) VisitImportDeclaration(_ *ast.ImportDeclaration) ast.Repr {
	// Handled in `declareImportDeclaration`
	panic(&UnreachableStatementError{})
}

func (checker *Checker) declareImportDeclaration(declaration *ast.ImportDeclaration) ast.Repr {
	locationRange := ast.Range{
		StartPos: declaration.LocationPos,
		// TODO: improve
		EndPos: declaration.LocationPos,
	}

	resolvedLocations := checker.resolveLocation(declaration.Identifiers, declaration.Location)

	checker.Elaboration.ImportDeclarationsResolvedLocations[declaration] = resolvedLocations

	for _, resolvedLocation := range resolvedLocations {
		checker.importResolvedLocation(resolvedLocation, locationRange)
	}

	return nil
}

func (checker *Checker) resolveLocation(identifiers []ast.Identifier, location ast.Location) []ResolvedLocation {

	// If no location handler is available,
	// default to resolving to a single location that declares all identifiers

	if checker.locationHandler == nil {
		return []ResolvedLocation{
			{
				Location:    location,
				Identifiers: identifiers,
			},
		}
	}

	// A location handler is available,
	// use it to resolve the location / identifiers

	return checker.locationHandler(identifiers, location)
}

func (checker *Checker) importResolvedLocation(resolvedLocation ResolvedLocation, locationRange ast.Range) {

	// First, get the Import for the resolved location

	location := resolvedLocation.Location

	var imp Import

	if checker.importHandler != nil {
		var err *CheckerError
		imp, err = checker.importHandler(checker, location)
		if err != nil {
			checker.report(
				&ImportedProgramError{
					CheckerError:   err,
					ImportLocation: location,
					Range:          locationRange,
				},
			)
			return
		}
	}

	if imp == nil {
		checker.report(
			&UnresolvedImportError{
				ImportLocation: location,
				Range:          locationRange,
			},
		)
		return
	}

	// If the import itself is being checked right now,
	// then the import is cyclic

	if imp.IsChecking() {
		checker.report(
			&CyclicImportsError{
				Location: location,
				Range:    locationRange,
			},
		)
		return
	}

	// Attempt to import the requested value declarations

	allValueElements := imp.AllValueElements()
	foundValues, invalidAccessedValues := checker.importElements(
		checker.valueActivations,
		resolvedLocation.Identifiers,
		allValueElements,
		imp.IsImportableValue,
	)

	// Attempt to import the requested type declarations

	allTypeElements := imp.AllTypeElements()
	foundTypes, invalidAccessedTypes := checker.importElements(
		checker.typeActivations,
		resolvedLocation.Identifiers,
		allTypeElements,
		imp.IsImportableType,
	)

	// For each identifier, report if the import is invalid due to
	// restricted access and report an error (i.e. if there is
	// both a value and type with the same name, only report a single error)

	for _, identifier := range resolvedLocation.Identifiers {

		invalidAccessedElement, isInvalidAccess := invalidAccessedValues[identifier]
		if !isInvalidAccess {
			invalidAccessedElement, isInvalidAccess = invalidAccessedTypes[identifier]
		}

		if !isInvalidAccess {
			continue
		}

		checker.report(
			&InvalidAccessError{
				Name:              identifier.Identifier,
				RestrictingAccess: invalidAccessedElement.Access,
				DeclarationKind:   invalidAccessedElement.DeclarationKind,
				Range:             ast.NewRangeFromPositioned(identifier),
			},
		)
	}

	identifierCount := len(resolvedLocation.Identifiers)

	// Determine which requested declarations could neither be found
	// in the value nor in the type declarations of the imported program.
	// For each missing import, report an error and declare both a value
	// with an invalid type and an invalid type to avoid spurious errors
	// due to uses of the inaccessible value or type.
	//
	// Also show which declarations are available, to help with debugging.

	missing := make([]ast.Identifier, 0, identifierCount)

	for _, identifier := range resolvedLocation.Identifiers {
		if foundValues[identifier] || foundTypes[identifier] {
			continue
		}

		missing = append(missing, identifier)
	}

	if len(missing) > 0 {
		capacity := len(allValueElements) + len(allTypeElements)
		available := make([]string, 0, capacity)
		availableSet := make(map[string]struct{}, capacity)

		for identifier := range allValueElements {
			if _, ok := availableSet[identifier]; ok {
				continue
			}
			if !imp.IsImportableValue(identifier) {
				continue
			}
			availableSet[identifier] = struct{}{}
			available = append(available, identifier)
		}

		for identifier := range allTypeElements {
			if _, ok := availableSet[identifier]; ok {
				continue
			}
			if !imp.IsImportableType(identifier) {
				continue
			}
			availableSet[identifier] = struct{}{}
			available = append(available, identifier)
		}

		checker.handleMissingImports(missing, available, location)
	}
}

// EnsureLoaded finds or create a checker for the imported program and checks it.
//
func (checker *Checker) EnsureLoaded(location ast.Location, loadProgram func() *ast.Program) (*Checker, *CheckerError) {

	locationID := location.ID()

	subChecker, ok := checker.allCheckers[locationID]
	if ok {
		return subChecker, nil
	}

	if !ok || subChecker == nil {
		var err error
		subChecker, err = NewChecker(
			loadProgram(),
			location,
			WithPredeclaredValues(checker.PredeclaredValues),
			WithPredeclaredTypes(checker.PredeclaredTypes),
			WithAccessCheckMode(checker.accessCheckMode),
			WithValidTopLevelDeclarationsHandler(checker.validTopLevelDeclarationsHandler),
			WithAllCheckers(checker.allCheckers),
			WithCheckHandler(checker.checkHandler),
			WithImportHandler(checker.importHandler),
		)
		if err == nil {
			checker.allCheckers[locationID] = subChecker
		}
	}

	// Check the imported program, if any.

	var checkerErr *CheckerError
	if subChecker.Program != nil {
		// NOTE: ignore generic `error`-typed result, get internal `*CheckerError`

		_ = subChecker.Check()
		checkerErr = subChecker.CheckerError()
	}

	return subChecker, checkerErr
}

func (checker *Checker) handleMissingImports(missing []ast.Identifier, available []string, importLocation ast.Location) {
	for _, identifier := range missing {
		checker.report(
			&NotExportedError{
				Name:           identifier.Identifier,
				ImportLocation: importLocation,
				Available:      available,
				Pos:            identifier.Pos,
			},
		)

		// NOTE: declare constant variable with invalid type to silence rest of program
		const access = ast.AccessPrivate

		_, err := checker.valueActivations.Declare(variableDeclaration{
			identifier:               identifier.Identifier,
			ty:                       &InvalidType{},
			access:                   access,
			kind:                     common.DeclarationKindValue,
			pos:                      identifier.Pos,
			isConstant:               true,
			allowOuterScopeShadowing: false,
		})
		checker.report(err)

		// NOTE: declare type with invalid type to silence rest of program
		_, err = checker.typeActivations.DeclareType(typeDeclaration{
			identifier:               identifier,
			ty:                       &InvalidType{},
			declarationKind:          common.DeclarationKindType,
			access:                   access,
			allowOuterScopeShadowing: false,
		})
		checker.report(err)
	}
}

func (checker *Checker) importElements(
	valueActivations *VariableActivations,
	requestedIdentifiers []ast.Identifier,
	availableElements map[string]ImportElement,
	filter func(name string) bool,
) (
	found map[ast.Identifier]bool,
	invalidAccessed map[ast.Identifier]ImportElement,
) {
	found = map[ast.Identifier]bool{}
	invalidAccessed = map[ast.Identifier]ImportElement{}

	// Determine which identifiers are imported /
	// which variables need to be declared

	explicitlyImported := map[string]ast.Identifier{}

	var elements map[string]ImportElement
	identifiersCount := len(requestedIdentifiers)
	if identifiersCount > 0 && availableElements != nil {
		elements = make(map[string]ImportElement, identifiersCount)
		for _, identifier := range requestedIdentifiers {
			name := identifier.Identifier
			element, ok := availableElements[name]
			if !ok {
				continue
			}
			elements[name] = element
			found[identifier] = true
			explicitlyImported[name] = identifier
		}
	} else {
		elements = availableElements
	}

	for name, element := range elements {

		if !filter(name) {
			continue
		}

		// If the variable can't be imported due to restricted access,
		// report an error, but still import the variable

		access := element.Access

		if !checker.isReadableAccess(access) {

			// If the variable was imported explicitly, report an error

			if identifier, ok := explicitlyImported[name]; ok {
				invalidAccessed[identifier] = element
			} else {
				// Don't import not explicitly imported inaccessible variable
				continue
			}
		}

		_, err := valueActivations.Declare(variableDeclaration{
			identifier: name,
			ty:         element.Type,
			// TODO: implies that type is "re-exported"
			access: access,
			kind:   element.DeclarationKind,
			// TODO:
			pos:                      ast.Position{},
			isConstant:               true,
			argumentLabels:           element.ArgumentLabels,
			allowOuterScopeShadowing: false,
		})
		checker.report(err)
	}

	return
}
