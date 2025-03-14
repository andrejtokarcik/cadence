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
	"fmt"
	"math"
	"math/big"
	"strings"

	"github.com/onflow/cadence/fixedpoint"
	"github.com/onflow/cadence/runtime/ast"
	"github.com/onflow/cadence/runtime/common"
	"github.com/onflow/cadence/runtime/errors"
)

func qualifiedIdentifier(identifier string, containerType Type) string {

	// Gather all identifiers: this, parent, grand-parent, etc.

	identifiers := []string{identifier}

	for containerType != nil {
		switch typedContainerType := containerType.(type) {
		case *InterfaceType:
			identifiers = append(identifiers, typedContainerType.Identifier)
			containerType = typedContainerType.ContainerType
		case *CompositeType:
			identifiers = append(identifiers, typedContainerType.Identifier)
			containerType = typedContainerType.ContainerType
		default:
			panic(errors.NewUnreachableError())
		}
	}

	// Append all identifiers, in reverse order

	var sb strings.Builder

	for i := len(identifiers) - 1; i >= 0; i-- {
		sb.WriteString(identifiers[i])
		if i != 0 {
			sb.WriteRune('.')
		}
	}

	return sb.String()
}

type TypeID string

type Type interface {
	IsType()
	ID() TypeID
	String() string
	QualifiedString() string
	Equal(other Type) bool

	// IsResourceType returns true if the type is itself a resource (a `CompositeType` with resource kind),
	// or it contains a resource type (e.g. for optionals, arrays, dictionaries, etc.)
	IsResourceType() bool

	// IsInvalidType returns true if the type is itself the invalid type (see `InvalidType`),
	// or it contains an invalid type (e.g. for optionals, arrays, dictionaries, etc.)
	IsInvalidType() bool

	// IsStorable returns true if the type is allowed to be a stored,
	// e.g. in a field of a composite type.
	IsStorable(results map[*Member]bool) bool

	// IsEquatable returns true if values of the type can be equated
	IsEquatable() bool

	TypeAnnotationState() TypeAnnotationState
	RewriteWithRestrictedTypes() (result Type, rewritten bool)

	// Unify attempts to unify the given type with this type, i.e., resolve type parameters
	// in generic types (see `GenericType`) using the given type parameters.
	//
	// For a generic type, unification assigns a given type with a type parameter.
	//
	// If the type parameter has not been previously unified with a type,
	// through an explicitly provided type argument in an invocation
	// or through a previous unification, the type parameter is assigned the given type.
	//
	// If the type parameter has already been previously unified with a type,
	// the type parameter's unified .
	//
	// The boolean return value indicates if a generic type was encountered during unification.
	// For primitives (e.g. `Int`, `String`, etc.) it would be false, as .
	// For types with nested types (e.g. optionals, arrays, and dictionaries)
	// the result is the successful unification of the inner types.
	//
	// The boolean return value does *not* indicate if unification succeeded or not.
	//
	Unify(
		other Type,
		typeParameters map[*TypeParameter]Type,
		report func(err error),
		outerRange ast.Range,
	) bool

	// Resolve returns a type that is free of generic types (see `GenericType`),
	// i.e. it resolves the type parameters in generic types given the type parameter
	// unifications of `typeParameters`.
	//
	// If resolution fails, it returns `nil`.
	//
	Resolve(typeArguments map[*TypeParameter]Type) Type

	GetMembers() map[string]MemberResolver
}

// ValueIndexableType is a type which can be indexed into using a value
//
type ValueIndexableType interface {
	Type
	isValueIndexableType() bool
	AllowsValueIndexingAssignment() bool
	ElementType(isAssignment bool) Type
	IndexingType() Type
}

type MemberResolver struct {
	Kind    common.DeclarationKind
	Resolve func(identifier string, targetRange ast.Range, report func(error)) *Member
}

// ContainedType is a type which might have a container type
//
type ContainedType interface {
	Type
	GetContainerType() Type
}

// ContainerType is a type which might have nested types
//
type ContainerType interface {
	Type
	NestedTypes() map[string]Type
}

func VisitContainerAndNested(t ContainerType, visit func(ty Type)) {
	visit(t)

	for _, nestedType := range t.NestedTypes() {
		if nestedContainerType, ok := nestedType.(ContainerType); ok {
			VisitContainerAndNested(nestedContainerType, visit)
		} else {
			visit(nestedType)
		}
	}
}

// CompositeKindedType is a type which has a composite kind
//
type CompositeKindedType interface {
	Type
	GetCompositeKind() common.CompositeKind
}

// LocatedType is a type which has a location
//
type LocatedType interface {
	Type
	GetLocation() ast.Location
}

// ParameterizedType is a type which might have type parameters
//
type ParameterizedType interface {
	Type
	TypeParameters() []*TypeParameter
	Instantiate(typeArguments []Type, report func(err error)) Type
	BaseType() Type
	TypeArguments() []Type
}

// TypeAnnotation

type TypeAnnotation struct {
	IsResource bool
	Type       Type
}

func (a *TypeAnnotation) TypeAnnotationState() TypeAnnotationState {
	if a.Type.IsInvalidType() {
		return TypeAnnotationStateValid
	}

	innerState := a.Type.TypeAnnotationState()
	if innerState != TypeAnnotationStateValid {
		return innerState
	}

	isResourceType := a.Type.IsResourceType()
	switch {
	case isResourceType && !a.IsResource:
		return TypeAnnotationStateMissingResourceAnnotation
	case !isResourceType && a.IsResource:
		return TypeAnnotationStateInvalidResourceAnnotation
	default:
		return TypeAnnotationStateValid
	}
}

func (a *TypeAnnotation) String() string {
	if a.IsResource {
		return fmt.Sprintf(
			"%s%s",
			common.CompositeKindResource.Annotation(),
			a.Type,
		)
	} else {
		return fmt.Sprint(a.Type)
	}
}

func (a *TypeAnnotation) QualifiedString() string {
	qualifiedString := a.Type.QualifiedString()
	if a.IsResource {
		return fmt.Sprintf(
			"%s%s",
			common.CompositeKindResource.Annotation(),
			qualifiedString,
		)
	} else {
		return fmt.Sprint(qualifiedString)
	}
}

func (a *TypeAnnotation) Equal(other *TypeAnnotation) bool {
	return a.IsResource == other.IsResource &&
		a.Type.Equal(other.Type)
}

func NewTypeAnnotation(ty Type) *TypeAnnotation {
	return &TypeAnnotation{
		IsResource: ty.IsResourceType(),
		Type:       ty,
	}
}

// isInstance

const IsInstanceFunctionName = "isInstance"

var isInstanceFunctionType = &FunctionType{
	Parameters: []*Parameter{
		{
			Label:      ArgumentLabelNotRequired,
			Identifier: "type",
			TypeAnnotation: NewTypeAnnotation(
				&MetaType{},
			),
		},
	},
	ReturnTypeAnnotation: NewTypeAnnotation(
		&BoolType{},
	),
}

const isInstanceFunctionDocString = `
Returns true if the object conforms to the given type at runtime
`

// toString

const ToStringFunctionName = "toString"

var toStringFunctionType = &FunctionType{
	ReturnTypeAnnotation: NewTypeAnnotation(
		&StringType{},
	),
}

const toStringFunctionDocString = `
A textual representation of this object
`

// toBigEndianBytes

const ToBigEndianBytesFunctionName = "toBigEndianBytes"

var toBigEndianBytesFunctionType = &FunctionType{
	ReturnTypeAnnotation: NewTypeAnnotation(
		&VariableSizedType{
			Type: &UInt8Type{},
		},
	),
}

const toBigEndianBytesFunctionDocString = `
Returns an array containing the big-endian byte representation of the number
`

func withBuiltinMembers(ty Type, members map[string]MemberResolver) map[string]MemberResolver {
	if members == nil {
		members = map[string]MemberResolver{}
	}

	// All types have a predeclared member `fun isInstance(_ type: Type): Bool`

	members[IsInstanceFunctionName] = MemberResolver{
		Kind: common.DeclarationKindFunction,
		Resolve: func(identifier string, _ ast.Range, _ func(error)) *Member {
			return NewPublicFunctionMember(
				ty,
				identifier,
				isInstanceFunctionType,
				isInstanceFunctionDocString,
			)
		},
	}

	// All number types and addresses have a `toString` function

	if IsSubType(ty, &NumberType{}) || IsSubType(ty, &AddressType{}) {

		members[ToStringFunctionName] = MemberResolver{
			Kind: common.DeclarationKindFunction,
			Resolve: func(identifier string, _ ast.Range, _ func(error)) *Member {
				return NewPublicFunctionMember(
					ty,
					identifier,
					toStringFunctionType,
					toStringFunctionDocString,
				)
			},
		}
	}

	// All number types have a `toBigEndianBytes` function

	if IsSubType(ty, &NumberType{}) {

		members[ToBigEndianBytesFunctionName] = MemberResolver{
			Kind: common.DeclarationKindFunction,
			Resolve: func(identifier string, _ ast.Range, _ func(error)) *Member {
				return NewPublicFunctionMember(
					ty,
					identifier,
					toBigEndianBytesFunctionType,
					toBigEndianBytesFunctionDocString,
				)
			},
		}
	}

	return members
}

// MetaType represents the type of a type.
type MetaType struct{}

func (*MetaType) IsType() {}

func (*MetaType) String() string {
	return "Type"
}

func (*MetaType) QualifiedString() string {
	return "Type"
}

func (*MetaType) ID() TypeID {
	return "Type"
}

func (*MetaType) Equal(other Type) bool {
	_, ok := other.(*MetaType)
	return ok
}

func (*MetaType) IsResourceType() bool {
	return false
}

func (*MetaType) IsInvalidType() bool {
	return false
}

func (*MetaType) IsStorable(_ map[*Member]bool) bool {
	return true
}

func (*MetaType) IsEquatable() bool {
	return true
}

func (*MetaType) TypeAnnotationState() TypeAnnotationState {
	return TypeAnnotationStateValid
}

func (t *MetaType) RewriteWithRestrictedTypes() (result Type, rewritten bool) {
	return t, false
}

func (*MetaType) Unify(_ Type, _ map[*TypeParameter]Type, _ func(err error), _ ast.Range) bool {
	return false
}

func (t *MetaType) Resolve(_ map[*TypeParameter]Type) Type {
	return t
}

const typeIdentifierDocString = `
The fully-qualified identifier of the type
`

func (t *MetaType) GetMembers() map[string]MemberResolver {
	return withBuiltinMembers(t, map[string]MemberResolver{
		"identifier": {
			Kind: common.DeclarationKindField,
			Resolve: func(identifier string, _ ast.Range, _ func(error)) *Member {
				return NewPublicConstantFieldMember(
					t,
					identifier,
					&StringType{},
					typeIdentifierDocString,
				)
			},
		},
	})
}

// AnyType represents the top type of all types.
// NOTE: This type is only used internally and not available in programs.
type AnyType struct{}

func (*AnyType) IsType() {}

func (*AnyType) String() string {
	return "Any"
}

func (*AnyType) QualifiedString() string {
	return "Any"
}

func (*AnyType) ID() TypeID {
	return "Any"
}

func (*AnyType) Equal(other Type) bool {
	_, ok := other.(*AnyType)
	return ok
}

func (*AnyType) IsResourceType() bool {
	return false
}

func (*AnyType) IsInvalidType() bool {
	return false
}

func (*AnyType) IsStorable(_ map[*Member]bool) bool {
	// The actual storability of a value is checked at run-time
	return false
}

func (*AnyType) IsEquatable() bool {
	return false
}

func (*AnyType) TypeAnnotationState() TypeAnnotationState {
	return TypeAnnotationStateValid
}

func (t *AnyType) RewriteWithRestrictedTypes() (result Type, rewritten bool) {
	return t, false
}

func (*AnyType) Unify(_ Type, _ map[*TypeParameter]Type, _ func(err error), _ ast.Range) bool {
	return false
}

func (t *AnyType) Resolve(_ map[*TypeParameter]Type) Type {
	return t
}

func (t *AnyType) GetMembers() map[string]MemberResolver {
	return withBuiltinMembers(t, nil)
}

// AnyStructType represents the top type of all non-resource types
type AnyStructType struct{}

func (*AnyStructType) IsType() {}

func (*AnyStructType) String() string {
	return "AnyStruct"
}

func (*AnyStructType) QualifiedString() string {
	return "AnyStruct"
}

func (*AnyStructType) ID() TypeID {
	return "AnyStruct"
}

func (*AnyStructType) Equal(other Type) bool {
	_, ok := other.(*AnyStructType)
	return ok
}

func (*AnyStructType) IsResourceType() bool {
	return false
}

func (*AnyStructType) IsInvalidType() bool {
	return false
}

func (*AnyStructType) IsStorable(_ map[*Member]bool) bool {
	// The actual storability of a value is checked at run-time
	return true
}

func (*AnyStructType) IsEquatable() bool {
	return false
}

func (*AnyStructType) TypeAnnotationState() TypeAnnotationState {
	return TypeAnnotationStateValid
}

func (t *AnyStructType) RewriteWithRestrictedTypes() (result Type, rewritten bool) {
	return t, false
}

func (*AnyStructType) Unify(_ Type, _ map[*TypeParameter]Type, _ func(err error), _ ast.Range) bool {
	return false
}

func (t *AnyStructType) Resolve(_ map[*TypeParameter]Type) Type {
	return t
}

func (t *AnyStructType) GetMembers() map[string]MemberResolver {
	return withBuiltinMembers(t, nil)
}

// AnyResourceType represents the top type of all resource types
type AnyResourceType struct{}

func (*AnyResourceType) IsType() {}

func (*AnyResourceType) String() string {
	return "AnyResource"
}

func (*AnyResourceType) QualifiedString() string {
	return "AnyResource"
}

func (*AnyResourceType) ID() TypeID {
	return "AnyResource"
}

func (*AnyResourceType) Equal(other Type) bool {
	_, ok := other.(*AnyResourceType)
	return ok
}

func (*AnyResourceType) IsResourceType() bool {
	return true
}

func (*AnyResourceType) IsInvalidType() bool {
	return false
}

func (*AnyResourceType) IsStorable(_ map[*Member]bool) bool {
	// The actual storability of a value is checked at run-time
	return true
}

func (*AnyResourceType) IsEquatable() bool {
	return false
}

func (*AnyResourceType) TypeAnnotationState() TypeAnnotationState {
	return TypeAnnotationStateValid
}

func (t *AnyResourceType) RewriteWithRestrictedTypes() (result Type, rewritten bool) {
	return t, false
}

func (*AnyResourceType) Unify(_ Type, _ map[*TypeParameter]Type, _ func(err error), _ ast.Range) bool {
	return false
}

func (t *AnyResourceType) Resolve(_ map[*TypeParameter]Type) Type {
	return t
}

func (t *AnyResourceType) GetMembers() map[string]MemberResolver {
	return withBuiltinMembers(t, nil)
}

// NeverType represents the bottom type
type NeverType struct{}

func (*NeverType) IsType() {}

func (*NeverType) String() string {
	return "Never"
}

func (*NeverType) QualifiedString() string {
	return "Never"
}

func (*NeverType) ID() TypeID {
	return "Never"
}

func (*NeverType) Equal(other Type) bool {
	_, ok := other.(*NeverType)
	return ok
}

func (*NeverType) IsResourceType() bool {
	return false
}

func (*NeverType) IsInvalidType() bool {
	return false
}

func (*NeverType) IsStorable(_ map[*Member]bool) bool {
	return false
}

func (*NeverType) IsEquatable() bool {
	return false
}

func (*NeverType) TypeAnnotationState() TypeAnnotationState {
	return TypeAnnotationStateValid
}

func (t *NeverType) RewriteWithRestrictedTypes() (result Type, rewritten bool) {
	return t, false
}

func (*NeverType) Unify(_ Type, _ map[*TypeParameter]Type, _ func(err error), _ ast.Range) bool {
	return false
}

func (t *NeverType) Resolve(_ map[*TypeParameter]Type) Type {
	return t
}

func (t *NeverType) GetMembers() map[string]MemberResolver {
	return withBuiltinMembers(t, nil)
}

// VoidType represents the void type
type VoidType struct{}

func (*VoidType) IsType() {}

func (*VoidType) String() string {
	return "Void"
}

func (*VoidType) QualifiedString() string {
	return "Void"
}

func (*VoidType) ID() TypeID {
	return "Void"
}

func (*VoidType) Equal(other Type) bool {
	_, ok := other.(*VoidType)
	return ok
}

func (*VoidType) IsResourceType() bool {
	return false
}

func (*VoidType) IsInvalidType() bool {
	return false
}

func (*VoidType) IsStorable(_ map[*Member]bool) bool {
	return false
}

func (*VoidType) IsEquatable() bool {
	return false
}

func (*VoidType) TypeAnnotationState() TypeAnnotationState {
	return TypeAnnotationStateValid
}

func (t *VoidType) RewriteWithRestrictedTypes() (result Type, rewritten bool) {
	return t, false
}

func (*VoidType) Unify(_ Type, _ map[*TypeParameter]Type, _ func(err error), _ ast.Range) bool {
	return false
}

func (t *VoidType) Resolve(_ map[*TypeParameter]Type) Type {
	return t
}

func (t *VoidType) GetMembers() map[string]MemberResolver {
	return withBuiltinMembers(t, nil)
}

// InvalidType represents a type that is invalid.
// It is the result of type checking failing and
// can't be expressed in programs.
//
type InvalidType struct{}

func (*InvalidType) IsType() {}

func (*InvalidType) String() string {
	return "<<invalid>>"
}

func (*InvalidType) QualifiedString() string {
	return "<<invalid>>"
}

func (*InvalidType) ID() TypeID {
	return "<<invalid>>"
}

func (*InvalidType) Equal(other Type) bool {
	_, ok := other.(*InvalidType)
	return ok
}

func (*InvalidType) IsResourceType() bool {
	return false
}

func (*InvalidType) IsInvalidType() bool {
	return true
}

func (*InvalidType) IsStorable(_ map[*Member]bool) bool {
	return false
}

func (*InvalidType) IsEquatable() bool {
	return false
}

func (*InvalidType) TypeAnnotationState() TypeAnnotationState {
	return TypeAnnotationStateValid
}

func (t *InvalidType) RewriteWithRestrictedTypes() (result Type, rewritten bool) {
	return t, false
}

func (*InvalidType) Unify(_ Type, _ map[*TypeParameter]Type, _ func(err error), _ ast.Range) bool {
	return false
}

func (t *InvalidType) Resolve(_ map[*TypeParameter]Type) Type {
	return t
}

func (t *InvalidType) GetMembers() map[string]MemberResolver {
	return withBuiltinMembers(t, nil)
}

// OptionalType represents the optional variant of another type
type OptionalType struct {
	Type Type
}

func (*OptionalType) IsType() {}

func (t *OptionalType) String() string {
	if t.Type == nil {
		return "optional"
	}
	return fmt.Sprintf("%s?", t.Type)
}

func (t *OptionalType) QualifiedString() string {
	if t.Type == nil {
		return "optional"
	}
	return fmt.Sprintf("%s?", t.Type.QualifiedString())
}

func (t *OptionalType) ID() TypeID {
	var id string
	if t.Type != nil {
		id = string(t.Type.ID())
	}
	return TypeID(fmt.Sprintf("%s?", id))
}

func (t *OptionalType) Equal(other Type) bool {
	otherOptional, ok := other.(*OptionalType)
	if !ok {
		return false
	}
	return t.Type.Equal(otherOptional.Type)
}

func (t *OptionalType) IsResourceType() bool {
	return t.Type.IsResourceType()
}

func (t *OptionalType) IsInvalidType() bool {
	return t.Type.IsInvalidType()
}

func (t *OptionalType) IsStorable(results map[*Member]bool) bool {
	return t.Type.IsStorable(results)
}

func (t *OptionalType) IsEquatable() bool {
	return t.Type.IsEquatable()
}

func (t *OptionalType) TypeAnnotationState() TypeAnnotationState {
	return t.Type.TypeAnnotationState()
}

func (t *OptionalType) RewriteWithRestrictedTypes() (Type, bool) {
	rewrittenType, rewritten := t.Type.RewriteWithRestrictedTypes()
	if rewritten {
		return &OptionalType{
			Type: rewrittenType,
		}, true
	} else {
		return t, false
	}
}

func (t *OptionalType) Unify(other Type, typeParameters map[*TypeParameter]Type, report func(err error), outerRange ast.Range) bool {
	otherOptional, ok := other.(*OptionalType)
	if !ok {
		return false
	}

	return t.Type.Unify(otherOptional.Type, typeParameters, report, outerRange)
}

func (t *OptionalType) Resolve(typeParameters map[*TypeParameter]Type) Type {

	newInnerType := t.Type.Resolve(typeParameters)
	if newInnerType == nil {
		return nil
	}

	return &OptionalType{
		Type: newInnerType,
	}
}

const optionalTypeMapFunctionDocString = `
Returns an optional of the result of calling the given function
with the value of this optional when it is not nil.

Returns nil if this optional is nil
`

func (t *OptionalType) GetMembers() map[string]MemberResolver {

	members := map[string]MemberResolver{
		"map": {
			Kind: common.DeclarationKindFunction,
			Resolve: func(identifier string, targetRange ast.Range, report func(error)) *Member {

				// It invalid for an optional of a resource to have a `map` function

				if t.Type.IsResourceType() {
					report(
						&InvalidResourceOptionalMemberError{
							Name:            identifier,
							DeclarationKind: common.DeclarationKindFunction,
							Range:           targetRange,
						},
					)
				}

				typeParameter := &TypeParameter{
					Name: "T",
				}

				resultType := &GenericType{
					TypeParameter: typeParameter,
				}

				return NewPublicFunctionMember(
					t,
					identifier,
					&FunctionType{
						TypeParameters: []*TypeParameter{
							typeParameter,
						},
						Parameters: []*Parameter{
							{
								Label:      ArgumentLabelNotRequired,
								Identifier: "transform",
								TypeAnnotation: NewTypeAnnotation(
									&FunctionType{
										Parameters: []*Parameter{
											{
												Label:          ArgumentLabelNotRequired,
												Identifier:     "value",
												TypeAnnotation: NewTypeAnnotation(t.Type),
											},
										},
										ReturnTypeAnnotation: NewTypeAnnotation(
											resultType,
										),
									},
								),
							},
						},
						ReturnTypeAnnotation: NewTypeAnnotation(
							&OptionalType{
								Type: resultType,
							},
						),
					},
					optionalTypeMapFunctionDocString,
				)
			},
		},
	}

	return withBuiltinMembers(t, members)
}

// GenericType
//
type GenericType struct {
	TypeParameter *TypeParameter
}

func (*GenericType) IsType() {}

func (t *GenericType) String() string {
	return t.TypeParameter.Name
}

func (t *GenericType) QualifiedString() string {
	return t.TypeParameter.Name
}

func (t *GenericType) ID() TypeID {
	return TypeID(t.TypeParameter.Name)
}

func (t *GenericType) Equal(other Type) bool {
	otherType, ok := other.(*GenericType)
	if !ok {
		return false
	}
	return t.TypeParameter == otherType.TypeParameter
}

func (t *GenericType) IsResourceType() bool {
	return false
}

func (t *GenericType) IsInvalidType() bool {
	return false
}

func (t *GenericType) IsStorable(_ map[*Member]bool) bool {
	return false
}

func (*GenericType) IsEquatable() bool {
	return false
}

func (t *GenericType) TypeAnnotationState() TypeAnnotationState {
	return TypeAnnotationStateValid
}

func (t *GenericType) RewriteWithRestrictedTypes() (result Type, rewritten bool) {
	return t, false
}

func (t *GenericType) Unify(
	other Type,
	typeParameters map[*TypeParameter]Type,
	report func(err error),
	outerRange ast.Range,
) bool {

	if unifiedType, ok := typeParameters[t.TypeParameter]; ok {

		// If the type parameter is already unified with a type argument
		// (either explicit by a type argument, or implicit through an argument's type),
		// check that this argument's type matches the unified type

		if !other.Equal(unifiedType) {
			report(
				&TypeParameterTypeMismatchError{
					TypeParameter: t.TypeParameter,
					ExpectedType:  unifiedType,
					ActualType:    other,
					Range:         outerRange,
				},
			)
		}

	} else {
		// If the type parameter is not yet unified to a type argument, unify it.

		typeParameters[t.TypeParameter] = other

		// If the type parameter corresponding to the type argument has a type bound,
		// then check that the argument's type is a subtype of the type bound.

		err := t.TypeParameter.checkTypeBound(other, outerRange)
		if err != nil {
			report(err)
		}
	}

	return true
}

func (t *GenericType) Resolve(typeParameters map[*TypeParameter]Type) Type {
	ty, ok := typeParameters[t.TypeParameter]
	if !ok {
		return nil
	}
	return ty
}

func (t *GenericType) GetMembers() map[string]MemberResolver {
	return withBuiltinMembers(t, nil)
}

// BoolType represents the boolean type
type BoolType struct{}

func (*BoolType) IsType() {}

func (*BoolType) String() string {
	return "Bool"
}

func (*BoolType) QualifiedString() string {
	return "Bool"
}

func (*BoolType) ID() TypeID {
	return "Bool"
}

func (*BoolType) Equal(other Type) bool {
	_, ok := other.(*BoolType)
	return ok
}

func (*BoolType) IsResourceType() bool {
	return false
}

func (*BoolType) IsInvalidType() bool {
	return false
}

func (*BoolType) IsStorable(_ map[*Member]bool) bool {
	return true
}

func (*BoolType) IsEquatable() bool {
	return true
}

func (*BoolType) TypeAnnotationState() TypeAnnotationState {
	return TypeAnnotationStateValid
}

func (t *BoolType) RewriteWithRestrictedTypes() (result Type, rewritten bool) {
	return t, false
}

func (*BoolType) Unify(_ Type, _ map[*TypeParameter]Type, _ func(err error), _ ast.Range) bool {
	return false
}

func (t *BoolType) Resolve(_ map[*TypeParameter]Type) Type {
	return t
}

func (t *BoolType) GetMembers() map[string]MemberResolver {
	return withBuiltinMembers(t, nil)
}

// CharacterType represents the character type

type CharacterType struct{}

func (*CharacterType) IsType() {}

func (*CharacterType) String() string {
	return "Character"
}

func (*CharacterType) QualifiedString() string {
	return "Character"
}

func (*CharacterType) ID() TypeID {
	return "Character"
}

func (*CharacterType) Equal(other Type) bool {
	_, ok := other.(*CharacterType)
	return ok
}

func (*CharacterType) IsResourceType() bool {
	return false
}

func (*CharacterType) IsInvalidType() bool {
	return false
}

func (*CharacterType) IsStorable(_ map[*Member]bool) bool {
	return true
}

func (*CharacterType) IsEquatable() bool {
	return true
}

func (*CharacterType) TypeAnnotationState() TypeAnnotationState {
	return TypeAnnotationStateValid
}

func (t *CharacterType) RewriteWithRestrictedTypes() (result Type, rewritten bool) {
	return t, false
}

func (*CharacterType) Unify(_ Type, _ map[*TypeParameter]Type, _ func(err error), _ ast.Range) bool {
	return false
}

func (t *CharacterType) Resolve(_ map[*TypeParameter]Type) Type {
	return t
}

func (t *CharacterType) GetMembers() map[string]MemberResolver {
	return withBuiltinMembers(t, nil)
}

// StringType represents the string type
type StringType struct{}

func (*StringType) IsType() {}

func (*StringType) String() string {
	return "String"
}

func (*StringType) QualifiedString() string {
	return "String"
}

func (*StringType) ID() TypeID {
	return "String"
}

func (*StringType) Equal(other Type) bool {
	_, ok := other.(*StringType)
	return ok
}

func (*StringType) IsResourceType() bool {
	return false
}

func (*StringType) IsInvalidType() bool {
	return false
}

func (*StringType) IsStorable(_ map[*Member]bool) bool {
	return true
}

func (*StringType) IsEquatable() bool {
	return true
}

func (*StringType) TypeAnnotationState() TypeAnnotationState {
	return TypeAnnotationStateValid
}

func (t *StringType) RewriteWithRestrictedTypes() (result Type, rewritten bool) {
	return t, false
}

var stringTypeConcatFunctionType = &FunctionType{
	Parameters: []*Parameter{
		{
			Label:          ArgumentLabelNotRequired,
			Identifier:     "other",
			TypeAnnotation: NewTypeAnnotation(&StringType{}),
		},
	},
	ReturnTypeAnnotation: NewTypeAnnotation(
		&StringType{},
	),
}

const stringTypeConcatFunctionDocString = `
Returns a new string which contains the given string concatenated to the end of the original string, but does not modify the original string
`

var stringTypeSliceFunctionType = &FunctionType{
	Parameters: []*Parameter{
		{
			Identifier:     "from",
			TypeAnnotation: NewTypeAnnotation(&IntType{}),
		},
		{
			Identifier:     "upTo",
			TypeAnnotation: NewTypeAnnotation(&IntType{}),
		},
	},
	ReturnTypeAnnotation: NewTypeAnnotation(
		&StringType{},
	),
}

const stringTypeSliceFunctionDocString = `
Returns a new string containing the slice of the characters in the given string from start index ` + "`from`" + ` up to, but not including, the end index ` + "`upTo`" + `.

This function creates a new string whose length is ` + "`upTo - from`" + `.
It does not modify the original string.
If either of the parameters are out of the bounds of the string, the function will fail
`

var stringTypeDecodeHexFunctionType = &FunctionType{
	ReturnTypeAnnotation: NewTypeAnnotation(
		&VariableSizedType{
			Type: &UInt8Type{},
		},
	),
}

const stringTypeDecodeHexFunctionDocString = `
Returns an array containing the bytes represented by the given hexadecimal string.

The given string must only contain hexadecimal characters and must have an even length.
If the string is malformed, the program aborts
`

const stringTypeLengthFieldDocString = `
The number of characters in the string
`

func (t *StringType) GetMembers() map[string]MemberResolver {
	return withBuiltinMembers(t, map[string]MemberResolver{
		"concat": {
			Kind: common.DeclarationKindFunction,
			Resolve: func(identifier string, _ ast.Range, _ func(error)) *Member {
				return NewPublicFunctionMember(
					t,
					identifier,
					stringTypeConcatFunctionType,
					stringTypeConcatFunctionDocString,
				)
			},
		},
		"slice": {
			Kind: common.DeclarationKindFunction,
			Resolve: func(identifier string, _ ast.Range, _ func(error)) *Member {
				return NewPublicFunctionMember(
					t,
					identifier,
					stringTypeSliceFunctionType,
					stringTypeSliceFunctionDocString,
				)
			},
		},
		"decodeHex": {
			Kind: common.DeclarationKindFunction,
			Resolve: func(identifier string, _ ast.Range, _ func(error)) *Member {
				return NewPublicFunctionMember(
					t,
					identifier,
					stringTypeDecodeHexFunctionType,
					stringTypeDecodeHexFunctionDocString,
				)
			},
		},
		"length": {
			Kind: common.DeclarationKindField,
			Resolve: func(identifier string, _ ast.Range, _ func(error)) *Member {
				return NewPublicConstantFieldMember(
					t,
					identifier,
					&IntType{},
					stringTypeLengthFieldDocString,
				)
			},
		},
	})
}

func (*StringType) isValueIndexableType() bool {
	return true
}

func (*StringType) AllowsValueIndexingAssignment() bool {
	return false
}

func (t *StringType) ElementType(_ bool) Type {
	return &CharacterType{}
}

func (t *StringType) IndexingType() Type {
	return &IntegerType{}
}

func (*StringType) Unify(_ Type, _ map[*TypeParameter]Type, _ func(err error), _ ast.Range) bool {
	return false
}

func (t *StringType) Resolve(_ map[*TypeParameter]Type) Type {
	return t
}

// NumberType represents the super-type of all signed number types
type NumberType struct{}

func (*NumberType) IsType() {}

func (*NumberType) String() string {
	return "Number"
}

func (*NumberType) QualifiedString() string {
	return "Number"
}

func (*NumberType) ID() TypeID {
	return "Number"
}

func (*NumberType) Equal(other Type) bool {
	_, ok := other.(*NumberType)
	return ok
}

func (*NumberType) IsResourceType() bool {
	return false
}

func (*NumberType) IsInvalidType() bool {
	return false
}

func (*NumberType) IsStorable(_ map[*Member]bool) bool {
	return true
}

func (*NumberType) IsEquatable() bool {
	return true
}

func (*NumberType) TypeAnnotationState() TypeAnnotationState {
	return TypeAnnotationStateValid
}

func (t *NumberType) RewriteWithRestrictedTypes() (result Type, rewritten bool) {
	return t, false
}

func (*NumberType) MinInt() *big.Int {
	return nil
}

func (*NumberType) MaxInt() *big.Int {
	return nil
}

func (*NumberType) Unify(_ Type, _ map[*TypeParameter]Type, _ func(err error), _ ast.Range) bool {
	return false
}

func (t *NumberType) Resolve(_ map[*TypeParameter]Type) Type {
	return t
}

func (t *NumberType) GetMembers() map[string]MemberResolver {
	return withBuiltinMembers(t, nil)
}

// SignedNumberType represents the super-type of all signed number types
type SignedNumberType struct{}

func (*SignedNumberType) IsType() {}

func (*SignedNumberType) String() string {
	return "SignedNumber"
}

func (*SignedNumberType) QualifiedString() string {
	return "SignedNumber"
}

func (*SignedNumberType) ID() TypeID {
	return "SignedNumber"
}

func (*SignedNumberType) Equal(other Type) bool {
	_, ok := other.(*SignedNumberType)
	return ok
}

func (*SignedNumberType) IsResourceType() bool {
	return false
}

func (*SignedNumberType) IsInvalidType() bool {
	return false
}

func (*SignedNumberType) IsStorable(_ map[*Member]bool) bool {
	return true
}

func (*SignedNumberType) IsEquatable() bool {
	return true
}

func (*SignedNumberType) TypeAnnotationState() TypeAnnotationState {
	return TypeAnnotationStateValid
}

func (t *SignedNumberType) RewriteWithRestrictedTypes() (result Type, rewritten bool) {
	return t, false
}

func (*SignedNumberType) MinInt() *big.Int {
	return nil
}

func (*SignedNumberType) MaxInt() *big.Int {
	return nil
}

func (*SignedNumberType) Unify(_ Type, _ map[*TypeParameter]Type, _ func(err error), _ ast.Range) bool {
	return false
}

func (t *SignedNumberType) Resolve(_ map[*TypeParameter]Type) Type {
	return t
}

func (t *SignedNumberType) GetMembers() map[string]MemberResolver {
	return withBuiltinMembers(t, nil)
}

// IntegerRangedType

type IntegerRangedType interface {
	Type
	MinInt() *big.Int
	MaxInt() *big.Int
}

type FractionalRangedType interface {
	IntegerRangedType
	Scale() uint
	MinFractional() *big.Int
	MaxFractional() *big.Int
}

// IntegerType represents the super-type of all integer types
type IntegerType struct{}

func (*IntegerType) IsType() {}

func (*IntegerType) String() string {
	return "Integer"
}

func (*IntegerType) QualifiedString() string {
	return "Integer"
}

func (*IntegerType) ID() TypeID {
	return "Integer"
}

func (*IntegerType) Equal(other Type) bool {
	_, ok := other.(*IntegerType)
	return ok
}

func (*IntegerType) IsResourceType() bool {
	return false
}

func (*IntegerType) IsInvalidType() bool {
	return false
}

func (*IntegerType) IsStorable(_ map[*Member]bool) bool {
	return true
}

func (*IntegerType) IsEquatable() bool {
	return true
}

func (*IntegerType) TypeAnnotationState() TypeAnnotationState {
	return TypeAnnotationStateValid
}

func (t *IntegerType) RewriteWithRestrictedTypes() (result Type, rewritten bool) {
	return t, false
}

func (*IntegerType) MinInt() *big.Int {
	return nil
}

func (*IntegerType) MaxInt() *big.Int {
	return nil
}

func (*IntegerType) Unify(_ Type, _ map[*TypeParameter]Type, _ func(err error), _ ast.Range) bool {
	return false
}

func (t *IntegerType) Resolve(_ map[*TypeParameter]Type) Type {
	return t
}

func (t *IntegerType) GetMembers() map[string]MemberResolver {
	return withBuiltinMembers(t, nil)
}

// SignedIntegerType represents the super-type of all signed integer types
type SignedIntegerType struct{}

func (*SignedIntegerType) IsType() {}

func (*SignedIntegerType) String() string {
	return "SignedInteger"
}

func (*SignedIntegerType) QualifiedString() string {
	return "SignedInteger"
}

func (*SignedIntegerType) ID() TypeID {
	return "SignedInteger"
}

func (*SignedIntegerType) Equal(other Type) bool {
	_, ok := other.(*SignedIntegerType)
	return ok
}

func (*SignedIntegerType) IsResourceType() bool {
	return false
}

func (*SignedIntegerType) IsInvalidType() bool {
	return false
}

func (*SignedIntegerType) IsStorable(_ map[*Member]bool) bool {
	return true
}

func (*SignedIntegerType) IsEquatable() bool {
	return true
}

func (*SignedIntegerType) TypeAnnotationState() TypeAnnotationState {
	return TypeAnnotationStateValid
}

func (t *SignedIntegerType) RewriteWithRestrictedTypes() (result Type, rewritten bool) {
	return t, false
}

func (*SignedIntegerType) MinInt() *big.Int {
	return nil
}

func (*SignedIntegerType) MaxInt() *big.Int {
	return nil
}

func (*SignedIntegerType) Unify(_ Type, _ map[*TypeParameter]Type, _ func(err error), _ ast.Range) bool {
	return false
}

func (t *SignedIntegerType) Resolve(_ map[*TypeParameter]Type) Type {
	return t
}

func (t *SignedIntegerType) GetMembers() map[string]MemberResolver {
	return withBuiltinMembers(t, nil)
}

// IntType represents the arbitrary-precision integer type `Int`
type IntType struct{}

func (*IntType) IsType() {}

func (*IntType) String() string {
	return "Int"
}

func (*IntType) QualifiedString() string {
	return "Int"
}

func (*IntType) ID() TypeID {
	return "Int"
}

func (*IntType) Equal(other Type) bool {
	_, ok := other.(*IntType)
	return ok
}

func (*IntType) IsResourceType() bool {
	return false
}

func (*IntType) IsInvalidType() bool {
	return false
}

func (*IntType) IsStorable(_ map[*Member]bool) bool {
	return true
}

func (*IntType) IsEquatable() bool {
	return true
}

func (*IntType) TypeAnnotationState() TypeAnnotationState {
	return TypeAnnotationStateValid
}

func (t *IntType) RewriteWithRestrictedTypes() (result Type, rewritten bool) {
	return t, false
}

func (*IntType) MinInt() *big.Int {
	return nil
}

func (*IntType) MaxInt() *big.Int {
	return nil
}

func (*IntType) Unify(_ Type, _ map[*TypeParameter]Type, _ func(err error), _ ast.Range) bool {
	return false
}

func (t *IntType) Resolve(_ map[*TypeParameter]Type) Type {
	return t
}

func (t *IntType) GetMembers() map[string]MemberResolver {
	return withBuiltinMembers(t, nil)
}

// Int8Type represents the 8-bit signed integer type `Int8`

type Int8Type struct{}

func (*Int8Type) IsType() {}

func (*Int8Type) String() string {
	return "Int8"
}

func (*Int8Type) QualifiedString() string {
	return "Int8"
}

func (*Int8Type) ID() TypeID {
	return "Int8"
}

func (*Int8Type) Equal(other Type) bool {
	_, ok := other.(*Int8Type)
	return ok
}

func (*Int8Type) IsResourceType() bool {
	return false
}

func (*Int8Type) IsInvalidType() bool {
	return false
}

func (*Int8Type) IsStorable(_ map[*Member]bool) bool {
	return true
}

func (*Int8Type) IsEquatable() bool {
	return true
}

func (*Int8Type) TypeAnnotationState() TypeAnnotationState {
	return TypeAnnotationStateValid
}

func (t *Int8Type) RewriteWithRestrictedTypes() (result Type, rewritten bool) {
	return t, false
}

var Int8TypeMinInt = new(big.Int).SetInt64(math.MinInt8)
var Int8TypeMaxInt = new(big.Int).SetInt64(math.MaxInt8)

func (*Int8Type) MinInt() *big.Int {
	return Int8TypeMinInt
}

func (*Int8Type) MaxInt() *big.Int {
	return Int8TypeMaxInt
}

func (*Int8Type) Unify(_ Type, _ map[*TypeParameter]Type, _ func(err error), _ ast.Range) bool {
	return false
}

func (t *Int8Type) Resolve(_ map[*TypeParameter]Type) Type {
	return t
}

func (t *Int8Type) GetMembers() map[string]MemberResolver {
	return withBuiltinMembers(t, nil)
}

// Int16Type represents the 16-bit signed integer type `Int16`
type Int16Type struct{}

func (*Int16Type) IsType() {}

func (*Int16Type) String() string {
	return "Int16"
}

func (*Int16Type) QualifiedString() string {
	return "Int16"
}

func (*Int16Type) ID() TypeID {
	return "Int16"
}

func (*Int16Type) Equal(other Type) bool {
	_, ok := other.(*Int16Type)
	return ok
}

func (*Int16Type) IsResourceType() bool {
	return false
}

func (*Int16Type) IsInvalidType() bool {
	return false
}

func (*Int16Type) IsStorable(_ map[*Member]bool) bool {
	return true
}

func (*Int16Type) IsEquatable() bool {
	return true
}

func (*Int16Type) TypeAnnotationState() TypeAnnotationState {
	return TypeAnnotationStateValid
}

func (t *Int16Type) RewriteWithRestrictedTypes() (result Type, rewritten bool) {
	return t, false
}

var Int16TypeMinInt = new(big.Int).SetInt64(math.MinInt16)
var Int16TypeMaxInt = new(big.Int).SetInt64(math.MaxInt16)

func (*Int16Type) MinInt() *big.Int {
	return Int16TypeMinInt
}

func (*Int16Type) MaxInt() *big.Int {
	return Int16TypeMaxInt
}

func (*Int16Type) Unify(_ Type, _ map[*TypeParameter]Type, _ func(err error), _ ast.Range) bool {
	return false
}

func (t *Int16Type) Resolve(_ map[*TypeParameter]Type) Type {
	return t
}

func (t *Int16Type) GetMembers() map[string]MemberResolver {
	return withBuiltinMembers(t, nil)
}

// Int32Type represents the 32-bit signed integer type `Int32`
type Int32Type struct{}

func (*Int32Type) IsType() {}

func (*Int32Type) String() string {
	return "Int32"
}

func (*Int32Type) QualifiedString() string {
	return "Int32"
}

func (*Int32Type) ID() TypeID {
	return "Int32"
}

func (*Int32Type) Equal(other Type) bool {
	_, ok := other.(*Int32Type)
	return ok
}

func (*Int32Type) IsResourceType() bool {
	return false
}

func (*Int32Type) IsInvalidType() bool {
	return false
}

func (*Int32Type) IsStorable(_ map[*Member]bool) bool {
	return true
}

func (*Int32Type) IsEquatable() bool {
	return true
}

func (*Int32Type) TypeAnnotationState() TypeAnnotationState {
	return TypeAnnotationStateValid
}

func (t *Int32Type) RewriteWithRestrictedTypes() (result Type, rewritten bool) {
	return t, false
}

var Int32TypeMinInt = new(big.Int).SetInt64(math.MinInt32)
var Int32TypeMaxInt = new(big.Int).SetInt64(math.MaxInt32)

func (*Int32Type) MinInt() *big.Int {
	return Int32TypeMinInt
}

func (*Int32Type) MaxInt() *big.Int {
	return Int32TypeMaxInt
}

func (*Int32Type) Unify(_ Type, _ map[*TypeParameter]Type, _ func(err error), _ ast.Range) bool {
	return false
}

func (t *Int32Type) Resolve(_ map[*TypeParameter]Type) Type {
	return t
}

func (t *Int32Type) GetMembers() map[string]MemberResolver {
	return withBuiltinMembers(t, nil)
}

// Int64Type represents the 64-bit signed integer type `Int64`
type Int64Type struct{}

func (*Int64Type) IsType() {}

func (*Int64Type) String() string {
	return "Int64"
}

func (*Int64Type) QualifiedString() string {
	return "Int64"
}

func (*Int64Type) ID() TypeID {
	return "Int64"
}

func (*Int64Type) Equal(other Type) bool {
	_, ok := other.(*Int64Type)
	return ok
}

func (*Int64Type) IsResourceType() bool {
	return false
}

func (*Int64Type) IsInvalidType() bool {
	return false
}

func (*Int64Type) IsStorable(_ map[*Member]bool) bool {
	return true
}

func (*Int64Type) IsEquatable() bool {
	return true
}

func (*Int64Type) TypeAnnotationState() TypeAnnotationState {
	return TypeAnnotationStateValid
}

func (t *Int64Type) RewriteWithRestrictedTypes() (result Type, rewritten bool) {
	return t, false
}

var Int64TypeMinInt = new(big.Int).SetInt64(math.MinInt64)
var Int64TypeMaxInt = new(big.Int).SetInt64(math.MaxInt64)

func (*Int64Type) MinInt() *big.Int {
	return Int64TypeMinInt
}

func (*Int64Type) MaxInt() *big.Int {
	return Int64TypeMaxInt
}

func (*Int64Type) Unify(_ Type, _ map[*TypeParameter]Type, _ func(err error), _ ast.Range) bool {
	return false
}

func (t *Int64Type) Resolve(_ map[*TypeParameter]Type) Type {
	return t
}

func (t *Int64Type) GetMembers() map[string]MemberResolver {
	return withBuiltinMembers(t, nil)
}

// Int128Type represents the 128-bit signed integer type `Int128`
type Int128Type struct{}

func (*Int128Type) IsType() {}

func (*Int128Type) String() string {
	return "Int128"
}

func (*Int128Type) QualifiedString() string {
	return "Int128"
}

func (*Int128Type) ID() TypeID {
	return "Int128"
}

func (*Int128Type) Equal(other Type) bool {
	_, ok := other.(*Int128Type)
	return ok
}

func (*Int128Type) IsResourceType() bool {
	return false
}

func (*Int128Type) IsInvalidType() bool {
	return false
}

func (*Int128Type) IsStorable(_ map[*Member]bool) bool {
	return true
}

func (*Int128Type) IsEquatable() bool {
	return true
}

func (*Int128Type) TypeAnnotationState() TypeAnnotationState {
	return TypeAnnotationStateValid
}

func (t *Int128Type) RewriteWithRestrictedTypes() (result Type, rewritten bool) {
	return t, false
}

var Int128TypeMinIntBig *big.Int

func init() {
	Int128TypeMinIntBig = big.NewInt(-1)
	Int128TypeMinIntBig.Lsh(Int128TypeMinIntBig, 127)
}

var Int128TypeMaxIntBig *big.Int

func init() {
	Int128TypeMaxIntBig = big.NewInt(1)
	Int128TypeMaxIntBig.Lsh(Int128TypeMaxIntBig, 127)
	Int128TypeMaxIntBig.Sub(Int128TypeMaxIntBig, big.NewInt(1))
}

func (*Int128Type) MinInt() *big.Int {
	return Int128TypeMinIntBig
}

func (*Int128Type) MaxInt() *big.Int {
	return Int128TypeMaxIntBig
}

func (*Int128Type) Unify(_ Type, _ map[*TypeParameter]Type, _ func(err error), _ ast.Range) bool {
	return false
}

func (t *Int128Type) Resolve(_ map[*TypeParameter]Type) Type {
	return t
}

func (t *Int128Type) GetMembers() map[string]MemberResolver {
	return withBuiltinMembers(t, nil)
}

// Int256Type represents the 256-bit signed integer type `Int256`
type Int256Type struct{}

func (*Int256Type) IsType() {}

func (*Int256Type) String() string {
	return "Int256"
}

func (*Int256Type) QualifiedString() string {
	return "Int256"
}

func (*Int256Type) ID() TypeID {
	return "Int256"
}

func (*Int256Type) Equal(other Type) bool {
	_, ok := other.(*Int256Type)
	return ok
}

func (*Int256Type) IsResourceType() bool {
	return false
}

func (*Int256Type) IsInvalidType() bool {
	return false
}

func (*Int256Type) IsStorable(_ map[*Member]bool) bool {
	return true
}

func (*Int256Type) IsEquatable() bool {
	return true
}

func (*Int256Type) TypeAnnotationState() TypeAnnotationState {
	return TypeAnnotationStateValid
}

func (t *Int256Type) RewriteWithRestrictedTypes() (result Type, rewritten bool) {
	return t, false
}

var Int256TypeMinIntBig *big.Int

func init() {
	Int256TypeMinIntBig = big.NewInt(-1)
	Int256TypeMinIntBig.Lsh(Int256TypeMinIntBig, 255)
}

var Int256TypeMaxIntBig *big.Int

func init() {
	Int256TypeMaxIntBig = big.NewInt(1)
	Int256TypeMaxIntBig.Lsh(Int256TypeMaxIntBig, 255)
	Int256TypeMaxIntBig.Sub(Int256TypeMaxIntBig, big.NewInt(1))
}

func (*Int256Type) MinInt() *big.Int {
	return Int256TypeMinIntBig
}

func (*Int256Type) MaxInt() *big.Int {
	return Int256TypeMaxIntBig
}

func (*Int256Type) Unify(_ Type, _ map[*TypeParameter]Type, _ func(err error), _ ast.Range) bool {
	return false
}

func (t *Int256Type) Resolve(_ map[*TypeParameter]Type) Type {
	return t
}

func (t *Int256Type) GetMembers() map[string]MemberResolver {
	return withBuiltinMembers(t, nil)
}

// UIntType represents the arbitrary-precision unsigned integer type `UInt`
type UIntType struct{}

func (*UIntType) IsType() {}

func (*UIntType) String() string {
	return "UInt"
}

func (*UIntType) QualifiedString() string {
	return "UInt"
}

func (*UIntType) ID() TypeID {
	return "UInt"
}

func (*UIntType) Equal(other Type) bool {
	_, ok := other.(*UIntType)
	return ok
}

func (*UIntType) IsResourceType() bool {
	return false
}

func (*UIntType) IsInvalidType() bool {
	return false
}

func (*UIntType) IsStorable(_ map[*Member]bool) bool {
	return true
}

func (*UIntType) IsEquatable() bool {
	return true
}

func (*UIntType) TypeAnnotationState() TypeAnnotationState {
	return TypeAnnotationStateValid
}

func (t *UIntType) RewriteWithRestrictedTypes() (result Type, rewritten bool) {
	return t, false
}

var UIntTypeMin = new(big.Int)

func (*UIntType) MinInt() *big.Int {
	return UIntTypeMin
}

func (*UIntType) MaxInt() *big.Int {
	return nil
}

func (*UIntType) Unify(_ Type, _ map[*TypeParameter]Type, _ func(err error), _ ast.Range) bool {
	return false
}

func (t *UIntType) Resolve(_ map[*TypeParameter]Type) Type {
	return t
}

func (t *UIntType) GetMembers() map[string]MemberResolver {
	return withBuiltinMembers(t, nil)
}

// UInt8Type represents the 8-bit unsigned integer type `UInt8`
// which checks for overflow and underflow
type UInt8Type struct{}

func (*UInt8Type) IsType() {}

func (*UInt8Type) String() string {
	return "UInt8"
}

func (*UInt8Type) QualifiedString() string {
	return "UInt8"
}

func (*UInt8Type) ID() TypeID {
	return "UInt8"
}

func (*UInt8Type) Equal(other Type) bool {
	_, ok := other.(*UInt8Type)
	return ok
}

func (*UInt8Type) IsResourceType() bool {
	return false
}

func (*UInt8Type) IsInvalidType() bool {
	return false
}

func (*UInt8Type) IsStorable(_ map[*Member]bool) bool {
	return true
}

func (*UInt8Type) IsEquatable() bool {
	return true
}

func (*UInt8Type) TypeAnnotationState() TypeAnnotationState {
	return TypeAnnotationStateValid
}

func (t *UInt8Type) RewriteWithRestrictedTypes() (result Type, rewritten bool) {
	return t, false
}

var UInt8TypeMinInt = new(big.Int)
var UInt8TypeMaxInt = new(big.Int).SetUint64(math.MaxUint8)

func (*UInt8Type) MinInt() *big.Int {
	return UInt8TypeMinInt
}

func (*UInt8Type) MaxInt() *big.Int {
	return UInt8TypeMaxInt
}

func (*UInt8Type) Unify(_ Type, _ map[*TypeParameter]Type, _ func(err error), _ ast.Range) bool {
	return false
}

func (t *UInt8Type) Resolve(_ map[*TypeParameter]Type) Type {
	return t
}

func (t *UInt8Type) GetMembers() map[string]MemberResolver {
	return withBuiltinMembers(t, nil)
}

// UInt16Type represents the 16-bit unsigned integer type `UInt16`
// which checks for overflow and underflow
type UInt16Type struct{}

func (*UInt16Type) IsType() {}

func (*UInt16Type) String() string {
	return "UInt16"
}

func (*UInt16Type) QualifiedString() string {
	return "UInt16"
}

func (*UInt16Type) ID() TypeID {
	return "UInt16"
}

func (*UInt16Type) Equal(other Type) bool {
	_, ok := other.(*UInt16Type)
	return ok
}

func (*UInt16Type) IsResourceType() bool {
	return false
}

func (*UInt16Type) IsInvalidType() bool {
	return false
}

func (*UInt16Type) IsStorable(_ map[*Member]bool) bool {
	return true
}

func (*UInt16Type) IsEquatable() bool {
	return true
}

func (*UInt16Type) TypeAnnotationState() TypeAnnotationState {
	return TypeAnnotationStateValid
}

func (t *UInt16Type) RewriteWithRestrictedTypes() (result Type, rewritten bool) {
	return t, false
}

var UInt16TypeMinInt = new(big.Int)
var UInt16TypeMaxInt = new(big.Int).SetUint64(math.MaxUint16)

func (*UInt16Type) MinInt() *big.Int {
	return UInt16TypeMinInt
}

func (*UInt16Type) MaxInt() *big.Int {
	return UInt16TypeMaxInt
}

func (*UInt16Type) Unify(_ Type, _ map[*TypeParameter]Type, _ func(err error), _ ast.Range) bool {
	return false
}

func (t *UInt16Type) Resolve(_ map[*TypeParameter]Type) Type {
	return t
}

func (t *UInt16Type) GetMembers() map[string]MemberResolver {
	return withBuiltinMembers(t, nil)
}

// UInt32Type represents the 32-bit unsigned integer type `UInt32`
// which checks for overflow and underflow
type UInt32Type struct{}

func (*UInt32Type) IsType() {}

func (*UInt32Type) String() string {
	return "UInt32"
}

func (*UInt32Type) QualifiedString() string {
	return "UInt32"
}

func (*UInt32Type) ID() TypeID {
	return "UInt32"
}

func (*UInt32Type) Equal(other Type) bool {
	_, ok := other.(*UInt32Type)
	return ok
}

func (*UInt32Type) IsResourceType() bool {
	return false
}

func (*UInt32Type) IsInvalidType() bool {
	return false
}

func (*UInt32Type) IsStorable(_ map[*Member]bool) bool {
	return true
}

func (*UInt32Type) IsEquatable() bool {
	return true
}

func (*UInt32Type) TypeAnnotationState() TypeAnnotationState {
	return TypeAnnotationStateValid
}

func (t *UInt32Type) RewriteWithRestrictedTypes() (result Type, rewritten bool) {
	return t, false
}

var UInt32TypeMinInt = new(big.Int)
var UInt32TypeMaxInt = new(big.Int).SetUint64(math.MaxUint32)

func (*UInt32Type) MinInt() *big.Int {
	return UInt32TypeMinInt
}

func (*UInt32Type) MaxInt() *big.Int {
	return UInt32TypeMaxInt
}

func (*UInt32Type) Unify(_ Type, _ map[*TypeParameter]Type, _ func(err error), _ ast.Range) bool {
	return false
}

func (t *UInt32Type) Resolve(_ map[*TypeParameter]Type) Type {
	return t
}

func (t *UInt32Type) GetMembers() map[string]MemberResolver {
	return withBuiltinMembers(t, nil)
}

// UInt64Type represents the 64-bit unsigned integer type `UInt64`
// which checks for overflow and underflow
type UInt64Type struct{}

func (*UInt64Type) IsType() {}

func (*UInt64Type) String() string {
	return "UInt64"
}

func (*UInt64Type) QualifiedString() string {
	return "UInt64"
}

func (*UInt64Type) ID() TypeID {
	return "UInt64"
}

func (*UInt64Type) Equal(other Type) bool {
	_, ok := other.(*UInt64Type)
	return ok
}

func (*UInt64Type) IsResourceType() bool {
	return false
}

func (*UInt64Type) IsInvalidType() bool {
	return false
}

func (*UInt64Type) IsStorable(_ map[*Member]bool) bool {
	return true
}

func (*UInt64Type) IsEquatable() bool {
	return true
}

func (*UInt64Type) TypeAnnotationState() TypeAnnotationState {
	return TypeAnnotationStateValid
}

func (t *UInt64Type) RewriteWithRestrictedTypes() (result Type, rewritten bool) {
	return t, false
}

var UInt64TypeMinInt = new(big.Int)
var UInt64TypeMaxInt = new(big.Int).SetUint64(math.MaxUint64)

func (*UInt64Type) MinInt() *big.Int {
	return UInt64TypeMinInt
}

func (*UInt64Type) MaxInt() *big.Int {
	return UInt64TypeMaxInt
}

func (*UInt64Type) Unify(_ Type, _ map[*TypeParameter]Type, _ func(err error), _ ast.Range) bool {
	return false
}

func (t *UInt64Type) Resolve(_ map[*TypeParameter]Type) Type {
	return t
}

func (t *UInt64Type) GetMembers() map[string]MemberResolver {
	return withBuiltinMembers(t, nil)
}

// UInt128Type represents the 128-bit unsigned integer type `UInt128`
// which checks for overflow and underflow
type UInt128Type struct{}

func (*UInt128Type) IsType() {}

func (*UInt128Type) String() string {
	return "UInt128"
}

func (*UInt128Type) QualifiedString() string {
	return "UInt128"
}

func (*UInt128Type) ID() TypeID {
	return "UInt128"
}

func (*UInt128Type) Equal(other Type) bool {
	_, ok := other.(*UInt128Type)
	return ok
}

func (*UInt128Type) IsResourceType() bool {
	return false
}

func (*UInt128Type) IsInvalidType() bool {
	return false
}

func (*UInt128Type) IsStorable(_ map[*Member]bool) bool {
	return true
}

func (*UInt128Type) IsEquatable() bool {
	return true
}

func (*UInt128Type) TypeAnnotationState() TypeAnnotationState {
	return TypeAnnotationStateValid
}

func (t *UInt128Type) RewriteWithRestrictedTypes() (result Type, rewritten bool) {
	return t, false
}

var UInt128TypeMinIntBig = new(big.Int)
var UInt128TypeMaxIntBig *big.Int

func init() {
	UInt128TypeMaxIntBig = big.NewInt(1)
	UInt128TypeMaxIntBig.Lsh(UInt128TypeMaxIntBig, 128)
	UInt128TypeMaxIntBig.Sub(UInt128TypeMaxIntBig, big.NewInt(1))
}

func (*UInt128Type) MinInt() *big.Int {
	return UInt128TypeMinIntBig
}

func (*UInt128Type) MaxInt() *big.Int {
	return UInt128TypeMaxIntBig
}

func (*UInt128Type) Unify(_ Type, _ map[*TypeParameter]Type, _ func(err error), _ ast.Range) bool {
	return false
}

func (t *UInt128Type) Resolve(_ map[*TypeParameter]Type) Type {
	return t
}

func (t *UInt128Type) GetMembers() map[string]MemberResolver {
	return withBuiltinMembers(t, nil)
}

// UInt256Type represents the 256-bit unsigned integer type `UInt256`
// which checks for overflow and underflow
type UInt256Type struct{}

func (*UInt256Type) IsType() {}

func (*UInt256Type) String() string {
	return "UInt256"
}

func (*UInt256Type) QualifiedString() string {
	return "UInt256"
}

func (*UInt256Type) ID() TypeID {
	return "UInt256"
}

func (*UInt256Type) Equal(other Type) bool {
	_, ok := other.(*UInt256Type)
	return ok
}

func (*UInt256Type) IsResourceType() bool {
	return false
}

func (*UInt256Type) IsInvalidType() bool {
	return false
}

func (*UInt256Type) IsStorable(_ map[*Member]bool) bool {
	return true
}

func (*UInt256Type) IsEquatable() bool {
	return true
}

func (*UInt256Type) TypeAnnotationState() TypeAnnotationState {
	return TypeAnnotationStateValid
}

func (t *UInt256Type) RewriteWithRestrictedTypes() (result Type, rewritten bool) {
	return t, false
}

var UInt256TypeMinIntBig = new(big.Int)
var UInt256TypeMaxIntBig *big.Int

func init() {
	UInt256TypeMaxIntBig = big.NewInt(1)
	UInt256TypeMaxIntBig.Lsh(UInt256TypeMaxIntBig, 256)
	UInt256TypeMaxIntBig.Sub(UInt256TypeMaxIntBig, big.NewInt(1))
}

func (*UInt256Type) MinInt() *big.Int {
	return UInt256TypeMinIntBig
}

func (*UInt256Type) MaxInt() *big.Int {
	return UInt256TypeMaxIntBig
}

func (*UInt256Type) Unify(_ Type, _ map[*TypeParameter]Type, _ func(err error), _ ast.Range) bool {
	return false
}

func (t *UInt256Type) Resolve(_ map[*TypeParameter]Type) Type {
	return t
}

func (t *UInt256Type) GetMembers() map[string]MemberResolver {
	return withBuiltinMembers(t, nil)
}

// Word8Type represents the 8-bit unsigned integer type `Word8`
// which does NOT check for overflow and underflow
type Word8Type struct{}

func (*Word8Type) IsType() {}

func (*Word8Type) String() string {
	return "Word8"
}

func (*Word8Type) QualifiedString() string {
	return "Word8"
}

func (*Word8Type) ID() TypeID {
	return "Word8"
}

func (*Word8Type) Equal(other Type) bool {
	_, ok := other.(*Word8Type)
	return ok
}

func (*Word8Type) IsResourceType() bool {
	return false
}

func (*Word8Type) IsInvalidType() bool {
	return false
}

func (*Word8Type) IsStorable(_ map[*Member]bool) bool {
	return true
}

func (*Word8Type) IsEquatable() bool {
	return true
}

func (*Word8Type) TypeAnnotationState() TypeAnnotationState {
	return TypeAnnotationStateValid
}

func (t *Word8Type) RewriteWithRestrictedTypes() (result Type, rewritten bool) {
	return t, false
}

var Word8TypeMinInt = new(big.Int)
var Word8TypeMaxInt = new(big.Int).SetUint64(math.MaxUint8)

func (*Word8Type) MinInt() *big.Int {
	return Word8TypeMinInt
}

func (*Word8Type) MaxInt() *big.Int {
	return Word8TypeMaxInt
}

func (*Word8Type) Unify(_ Type, _ map[*TypeParameter]Type, _ func(err error), _ ast.Range) bool {
	return false
}

func (t *Word8Type) Resolve(_ map[*TypeParameter]Type) Type {
	return t
}

func (t *Word8Type) GetMembers() map[string]MemberResolver {
	return withBuiltinMembers(t, nil)
}

// Word16Type represents the 16-bit unsigned integer type `Word16`
// which does NOT check for overflow and underflow
type Word16Type struct{}

func (*Word16Type) IsType() {}

func (*Word16Type) String() string {
	return "Word16"
}

func (*Word16Type) QualifiedString() string {
	return "Word16"
}

func (*Word16Type) ID() TypeID {
	return "Word16"
}

func (*Word16Type) Equal(other Type) bool {
	_, ok := other.(*Word16Type)
	return ok
}

func (*Word16Type) IsResourceType() bool {
	return false
}

func (*Word16Type) IsInvalidType() bool {
	return false
}

func (*Word16Type) IsStorable(_ map[*Member]bool) bool {
	return true
}

func (*Word16Type) IsEquatable() bool {
	return true
}

func (*Word16Type) TypeAnnotationState() TypeAnnotationState {
	return TypeAnnotationStateValid
}

func (t *Word16Type) RewriteWithRestrictedTypes() (result Type, rewritten bool) {
	return t, false
}

var Word16TypeMinInt = new(big.Int)
var Word16TypeMaxInt = new(big.Int).SetUint64(math.MaxUint16)

func (*Word16Type) MinInt() *big.Int {
	return Word16TypeMinInt
}

func (*Word16Type) MaxInt() *big.Int {
	return Word16TypeMaxInt
}

func (*Word16Type) Unify(_ Type, _ map[*TypeParameter]Type, _ func(err error), _ ast.Range) bool {
	return false
}

func (t *Word16Type) Resolve(_ map[*TypeParameter]Type) Type {
	return t
}

func (t *Word16Type) GetMembers() map[string]MemberResolver {
	return withBuiltinMembers(t, nil)
}

// Word32Type represents the 32-bit unsigned integer type `Word32`
// which does NOT check for overflow and underflow
type Word32Type struct{}

func (*Word32Type) IsType() {}

func (*Word32Type) String() string {
	return "Word32"
}

func (*Word32Type) QualifiedString() string {
	return "Word32"
}

func (*Word32Type) ID() TypeID {
	return "Word32"
}

func (*Word32Type) Equal(other Type) bool {
	_, ok := other.(*Word32Type)
	return ok
}

func (*Word32Type) IsResourceType() bool {
	return false
}

func (*Word32Type) IsInvalidType() bool {
	return false
}

func (*Word32Type) IsStorable(_ map[*Member]bool) bool {
	return true
}

func (*Word32Type) IsEquatable() bool {
	return true
}

func (*Word32Type) TypeAnnotationState() TypeAnnotationState {
	return TypeAnnotationStateValid
}

func (t *Word32Type) RewriteWithRestrictedTypes() (result Type, rewritten bool) {
	return t, false
}

var Word32TypeMinInt = new(big.Int)
var Word32TypeMaxInt = new(big.Int).SetUint64(math.MaxUint32)

func (*Word32Type) MinInt() *big.Int {
	return Word32TypeMinInt
}

func (*Word32Type) MaxInt() *big.Int {
	return Word32TypeMaxInt
}

func (*Word32Type) Unify(_ Type, _ map[*TypeParameter]Type, _ func(err error), _ ast.Range) bool {
	return false
}

func (t *Word32Type) Resolve(_ map[*TypeParameter]Type) Type {
	return t
}

func (t *Word32Type) GetMembers() map[string]MemberResolver {
	return withBuiltinMembers(t, nil)
}

// Word64Type represents the 64-bit unsigned integer type `Word64`
// which does NOT check for overflow and underflow
type Word64Type struct{}

func (*Word64Type) IsType() {}

func (*Word64Type) String() string {
	return "Word64"
}

func (*Word64Type) QualifiedString() string {
	return "Word64"
}

func (*Word64Type) ID() TypeID {
	return "Word64"
}

func (*Word64Type) Equal(other Type) bool {
	_, ok := other.(*Word64Type)
	return ok
}

func (*Word64Type) IsResourceType() bool {
	return false
}

func (*Word64Type) IsInvalidType() bool {
	return false
}

func (*Word64Type) IsStorable(_ map[*Member]bool) bool {
	return true
}

func (*Word64Type) IsEquatable() bool {
	return true
}

func (*Word64Type) TypeAnnotationState() TypeAnnotationState {
	return TypeAnnotationStateValid
}

func (t *Word64Type) RewriteWithRestrictedTypes() (result Type, rewritten bool) {
	return t, false
}

var Word64TypeMinInt = new(big.Int)
var Word64TypeMaxInt = new(big.Int).SetUint64(math.MaxUint64)

func (*Word64Type) MinInt() *big.Int {
	return Word64TypeMinInt
}

func (*Word64Type) MaxInt() *big.Int {
	return Word64TypeMaxInt
}

func (*Word64Type) Unify(_ Type, _ map[*TypeParameter]Type, _ func(err error), _ ast.Range) bool {
	return false
}

func (t *Word64Type) Resolve(_ map[*TypeParameter]Type) Type {
	return t
}

func (t *Word64Type) GetMembers() map[string]MemberResolver {
	return withBuiltinMembers(t, nil)
}

// FixedPointType represents the super-type of all fixed-point types
type FixedPointType struct{}

func (*FixedPointType) IsType() {}

func (*FixedPointType) String() string {
	return "FixedPoint"
}

func (*FixedPointType) QualifiedString() string {
	return "FixedPoint"
}

func (*FixedPointType) ID() TypeID {
	return "FixedPoint"
}

func (*FixedPointType) Equal(other Type) bool {
	_, ok := other.(*FixedPointType)
	return ok
}

func (*FixedPointType) IsResourceType() bool {
	return false
}

func (*FixedPointType) IsInvalidType() bool {
	return false
}

func (*FixedPointType) IsStorable(_ map[*Member]bool) bool {
	return true
}

func (*FixedPointType) IsEquatable() bool {
	return true
}

func (*FixedPointType) TypeAnnotationState() TypeAnnotationState {
	return TypeAnnotationStateValid
}

func (t *FixedPointType) RewriteWithRestrictedTypes() (result Type, rewritten bool) {
	return t, false
}

func (*FixedPointType) MinInt() *big.Int {
	return nil
}

func (*FixedPointType) MaxInt() *big.Int {
	return nil
}

func (*FixedPointType) Unify(_ Type, _ map[*TypeParameter]Type, _ func(err error), _ ast.Range) bool {
	return false
}

func (t *FixedPointType) Resolve(_ map[*TypeParameter]Type) Type {
	return t
}

func (t *FixedPointType) GetMembers() map[string]MemberResolver {
	return withBuiltinMembers(t, nil)
}

// SignedFixedPointType represents the super-type of all signed fixed-point types
type SignedFixedPointType struct{}

func (*SignedFixedPointType) IsType() {}

func (*SignedFixedPointType) String() string {
	return "SignedFixedPoint"
}

func (*SignedFixedPointType) QualifiedString() string {
	return "SignedFixedPoint"
}

func (*SignedFixedPointType) ID() TypeID {
	return "SignedFixedPoint"
}

func (*SignedFixedPointType) Equal(other Type) bool {
	_, ok := other.(*SignedFixedPointType)
	return ok
}

func (*SignedFixedPointType) IsResourceType() bool {
	return false
}

func (*SignedFixedPointType) IsInvalidType() bool {
	return false
}

func (*SignedFixedPointType) IsStorable(_ map[*Member]bool) bool {
	return true
}

func (*SignedFixedPointType) IsEquatable() bool {
	return true
}

func (*SignedFixedPointType) TypeAnnotationState() TypeAnnotationState {
	return TypeAnnotationStateValid
}

func (t *SignedFixedPointType) RewriteWithRestrictedTypes() (result Type, rewritten bool) {
	return t, false
}

func (*SignedFixedPointType) MinInt() *big.Int {
	return nil
}

func (*SignedFixedPointType) MaxInt() *big.Int {
	return nil
}

func (*SignedFixedPointType) Unify(_ Type, _ map[*TypeParameter]Type, _ func(err error), _ ast.Range) bool {
	return false
}

func (t *SignedFixedPointType) Resolve(_ map[*TypeParameter]Type) Type {
	return t
}

func (t *SignedFixedPointType) GetMembers() map[string]MemberResolver {
	return withBuiltinMembers(t, nil)
}

const Fix64Scale = fixedpoint.Fix64Scale
const Fix64Factor = fixedpoint.Fix64Factor

// Fix64Type represents the 64-bit signed decimal fixed-point type `Fix64`
// which has a scale of Fix64Scale, and checks for overflow and underflow
type Fix64Type struct{}

func (*Fix64Type) IsType() {}

func (*Fix64Type) String() string {
	return "Fix64"
}

func (*Fix64Type) QualifiedString() string {
	return "Fix64"
}

func (*Fix64Type) ID() TypeID {
	return "Fix64"
}

func (*Fix64Type) Equal(other Type) bool {
	_, ok := other.(*Fix64Type)
	return ok
}

func (*Fix64Type) IsResourceType() bool {
	return false
}

func (*Fix64Type) IsInvalidType() bool {
	return false
}

func (*Fix64Type) IsStorable(_ map[*Member]bool) bool {
	return true
}

func (*Fix64Type) IsEquatable() bool {
	return true
}

func (*Fix64Type) TypeAnnotationState() TypeAnnotationState {
	return TypeAnnotationStateValid
}

func (t *Fix64Type) RewriteWithRestrictedTypes() (result Type, rewritten bool) {
	return t, false
}

const Fix64TypeMinInt = fixedpoint.Fix64TypeMinInt
const Fix64TypeMaxInt = fixedpoint.Fix64TypeMaxInt

var Fix64TypeMinIntBig = fixedpoint.Fix64TypeMinIntBig
var Fix64TypeMaxIntBig = fixedpoint.Fix64TypeMaxIntBig

const Fix64TypeMinFractional = fixedpoint.Fix64TypeMinFractional
const Fix64TypeMaxFractional = fixedpoint.Fix64TypeMaxFractional

var Fix64TypeMinFractionalBig = fixedpoint.Fix64TypeMinFractionalBig
var Fix64TypeMaxFractionalBig = fixedpoint.Fix64TypeMaxFractionalBig

func (*Fix64Type) MinInt() *big.Int {
	return Fix64TypeMinIntBig
}

func (*Fix64Type) MaxInt() *big.Int {
	return Fix64TypeMaxIntBig
}

func (*Fix64Type) Scale() uint {
	return Fix64Scale
}

func (*Fix64Type) MinFractional() *big.Int {
	return Fix64TypeMinFractionalBig
}

func (*Fix64Type) MaxFractional() *big.Int {
	return Fix64TypeMaxFractionalBig
}

func (*Fix64Type) Unify(_ Type, _ map[*TypeParameter]Type, _ func(err error), _ ast.Range) bool {
	return false
}

func (t *Fix64Type) Resolve(_ map[*TypeParameter]Type) Type {
	return t
}

func (t *Fix64Type) GetMembers() map[string]MemberResolver {
	return withBuiltinMembers(t, nil)
}

// UFix64Type represents the 64-bit unsigned decimal fixed-point type `UFix64`
// which has a scale of 1E9, and checks for overflow and underflow
type UFix64Type struct{}

func (*UFix64Type) IsType() {}

func (*UFix64Type) String() string {
	return "UFix64"
}

func (*UFix64Type) QualifiedString() string {
	return "UFix64"
}

func (*UFix64Type) ID() TypeID {
	return "UFix64"
}

func (*UFix64Type) Equal(other Type) bool {
	_, ok := other.(*UFix64Type)
	return ok
}

func (*UFix64Type) IsResourceType() bool {
	return false
}

func (*UFix64Type) IsInvalidType() bool {
	return false
}

func (*UFix64Type) IsStorable(_ map[*Member]bool) bool {
	return true
}

func (*UFix64Type) IsEquatable() bool {
	return true
}

func (*UFix64Type) TypeAnnotationState() TypeAnnotationState {
	return TypeAnnotationStateValid
}

func (t *UFix64Type) RewriteWithRestrictedTypes() (result Type, rewritten bool) {
	return t, false
}

const UFix64TypeMinInt = fixedpoint.UFix64TypeMinInt
const UFix64TypeMaxInt = fixedpoint.UFix64TypeMaxInt

var UFix64TypeMinIntBig = fixedpoint.UFix64TypeMinIntBig
var UFix64TypeMaxIntBig = fixedpoint.UFix64TypeMaxIntBig

const UFix64TypeMinFractional = fixedpoint.UFix64TypeMinFractional
const UFix64TypeMaxFractional = fixedpoint.UFix64TypeMaxFractional

var UFix64TypeMinFractionalBig = fixedpoint.UFix64TypeMinFractionalBig
var UFix64TypeMaxFractionalBig = fixedpoint.UFix64TypeMaxFractionalBig

func (*UFix64Type) MinInt() *big.Int {
	return UFix64TypeMinIntBig
}

func (*UFix64Type) MaxInt() *big.Int {
	return UFix64TypeMaxIntBig
}

func (*UFix64Type) Scale() uint {
	return Fix64Scale
}

func (*UFix64Type) MinFractional() *big.Int {
	return UFix64TypeMinFractionalBig
}

func (*UFix64Type) MaxFractional() *big.Int {
	return UFix64TypeMaxFractionalBig
}

func (*UFix64Type) Unify(_ Type, _ map[*TypeParameter]Type, _ func(err error), _ ast.Range) bool {
	return false
}

func (t *UFix64Type) Resolve(_ map[*TypeParameter]Type) Type {
	return t
}

func (t *UFix64Type) GetMembers() map[string]MemberResolver {
	return withBuiltinMembers(t, nil)
}

// ArrayType

type ArrayType interface {
	ValueIndexableType
	isArrayType()
}

const arrayTypeContainsFunctionDocString = `
Returns true if the given object is in the array
`

const arrayTypeLengthFieldDocString = `
Returns the number of elements in the array
`

const arrayTypeAppendFunctionDocString = `
Adds the given element to the end of the array
`

const arrayTypeConcatFunctionDocString = `
Returns a new array which contains the given array concatenated to the end of the original array, but does not modify the original array
`

const arrayTypeInsertFunctionDocString = `
Inserts the given element at the given index of the array.

The index must be within the bounds of the array.
If the index is outside the bounds, the program aborts.

The existing element at the supplied index is not overwritten.

All the elements after the new inserted element are shifted to the right by one
`

const arrayTypeRemoveFunctionDocString = `
Removes the element at the given index from the array and returns it.

The index must be within the bounds of the array.
If the index is outside the bounds, the program aborts
`

const arrayTypeRemoveFirstFunctionDocString = `
Removes the first element from the array and returns it.

The array must not be empty. If the array is empty, the program aborts
`

const arrayTypeRemoveLastFunctionDocString = `
Removes the last element from the array and returns it.

The array must not be empty. If the array is empty, the program aborts
`

func getArrayMembers(arrayType ArrayType) map[string]MemberResolver {

	members := map[string]MemberResolver{
		"contains": {
			Kind: common.DeclarationKindFunction,
			Resolve: func(identifier string, targetRange ast.Range, report func(error)) *Member {

				elementType := arrayType.ElementType(false)

				// It impossible for an array of resources to have a `contains` function:
				// if the resource is passed as an argument, it cannot be inside the array

				if elementType.IsResourceType() {
					report(
						&InvalidResourceArrayMemberError{
							Name:            identifier,
							DeclarationKind: common.DeclarationKindFunction,
							Range:           targetRange,
						},
					)
				}

				// TODO: implement Equatable interface: https://github.com/dapperlabs/bamboo-node/issues/78

				if !elementType.IsEquatable() {
					report(
						&NotEquatableTypeError{
							Type:  elementType,
							Range: targetRange,
						},
					)
				}

				return NewPublicFunctionMember(
					arrayType,
					identifier,
					&FunctionType{
						Parameters: []*Parameter{
							{
								Label:          ArgumentLabelNotRequired,
								Identifier:     "element",
								TypeAnnotation: NewTypeAnnotation(elementType),
							},
						},
						ReturnTypeAnnotation: NewTypeAnnotation(
							&BoolType{},
						),
					},
					arrayTypeContainsFunctionDocString,
				)
			},
		},
		"length": {
			Kind: common.DeclarationKindField,
			Resolve: func(identifier string, _ ast.Range, _ func(error)) *Member {
				return NewPublicConstantFieldMember(
					arrayType,
					identifier,
					&IntType{},
					arrayTypeLengthFieldDocString,
				)
			},
		},
	}

	// TODO: maybe still return members but report a helpful error?

	if _, ok := arrayType.(*VariableSizedType); ok {

		members["append"] = MemberResolver{
			Kind: common.DeclarationKindFunction,
			Resolve: func(identifier string, targetRange ast.Range, report func(error)) *Member {
				elementType := arrayType.ElementType(false)
				return NewPublicFunctionMember(
					arrayType,
					identifier,
					&FunctionType{
						Parameters: []*Parameter{
							{
								Label:          ArgumentLabelNotRequired,
								Identifier:     "element",
								TypeAnnotation: NewTypeAnnotation(elementType),
							},
						},
						ReturnTypeAnnotation: NewTypeAnnotation(
							&VoidType{},
						),
					},
					arrayTypeAppendFunctionDocString,
				)
			},
		}

		members["concat"] = MemberResolver{
			Kind: common.DeclarationKindFunction,
			Resolve: func(identifier string, targetRange ast.Range, report func(error)) *Member {

				// TODO: maybe allow for resource element type

				elementType := arrayType.ElementType(false)

				if elementType.IsResourceType() {
					report(
						&InvalidResourceArrayMemberError{
							Name:            identifier,
							DeclarationKind: common.DeclarationKindFunction,
							Range:           targetRange,
						},
					)
				}

				typeAnnotation := NewTypeAnnotation(arrayType)

				return NewPublicFunctionMember(
					arrayType,
					identifier,
					&FunctionType{
						Parameters: []*Parameter{
							{
								Label:          ArgumentLabelNotRequired,
								Identifier:     "other",
								TypeAnnotation: typeAnnotation,
							},
						},
						ReturnTypeAnnotation: typeAnnotation,
					},
					arrayTypeConcatFunctionDocString,
				)
			},
		}

		members["insert"] = MemberResolver{
			Kind: common.DeclarationKindFunction,
			Resolve: func(identifier string, _ ast.Range, _ func(error)) *Member {

				elementType := arrayType.ElementType(false)

				return NewPublicFunctionMember(
					arrayType,
					identifier,
					&FunctionType{
						Parameters: []*Parameter{
							{
								Identifier:     "at",
								TypeAnnotation: NewTypeAnnotation(&IntegerType{}),
							},
							{
								Label:          ArgumentLabelNotRequired,
								Identifier:     "element",
								TypeAnnotation: NewTypeAnnotation(elementType),
							},
						},
						ReturnTypeAnnotation: NewTypeAnnotation(
							&VoidType{},
						),
					},
					arrayTypeInsertFunctionDocString,
				)
			},
		}

		members["remove"] = MemberResolver{
			Kind: common.DeclarationKindFunction,
			Resolve: func(identifier string, _ ast.Range, _ func(error)) *Member {

				elementType := arrayType.ElementType(false)

				return NewPublicFunctionMember(
					arrayType,
					identifier,
					&FunctionType{
						Parameters: []*Parameter{
							{
								Identifier:     "at",
								TypeAnnotation: NewTypeAnnotation(&IntegerType{}),
							},
						},
						ReturnTypeAnnotation: NewTypeAnnotation(
							elementType,
						),
					},
					arrayTypeRemoveFunctionDocString,
				)
			},
		}

		members["removeFirst"] = MemberResolver{
			Kind: common.DeclarationKindFunction,
			Resolve: func(identifier string, _ ast.Range, _ func(error)) *Member {

				elementType := arrayType.ElementType(false)

				return NewPublicFunctionMember(
					arrayType,
					identifier,
					&FunctionType{
						ReturnTypeAnnotation: NewTypeAnnotation(
							elementType,
						),
					},

					arrayTypeRemoveFirstFunctionDocString,
				)
			},
		}

		members["removeLast"] = MemberResolver{
			Kind: common.DeclarationKindFunction,
			Resolve: func(identifier string, _ ast.Range, _ func(error)) *Member {

				elementType := arrayType.ElementType(false)

				return NewPublicFunctionMember(
					arrayType,
					identifier,
					&FunctionType{
						ReturnTypeAnnotation: NewTypeAnnotation(
							elementType,
						),
					},
					arrayTypeRemoveLastFunctionDocString,
				)
			},
		}
	}

	return withBuiltinMembers(arrayType, members)
}

// VariableSizedType is a variable sized array type
type VariableSizedType struct {
	Type Type
}

func (*VariableSizedType) IsType() {}

func (*VariableSizedType) isArrayType() {}

func (t *VariableSizedType) String() string {
	return fmt.Sprintf("[%s]", t.Type)
}

func (t *VariableSizedType) QualifiedString() string {
	return fmt.Sprintf("[%s]", t.Type.QualifiedString())
}

func (t *VariableSizedType) ID() TypeID {
	return TypeID(fmt.Sprintf("[%s]", t.Type.ID()))
}

func (t *VariableSizedType) Equal(other Type) bool {
	otherArray, ok := other.(*VariableSizedType)
	if !ok {
		return false
	}

	return t.Type.Equal(otherArray.Type)
}

func (t *VariableSizedType) GetMembers() map[string]MemberResolver {
	return getArrayMembers(t)
}

func (t *VariableSizedType) IsResourceType() bool {
	return t.Type.IsResourceType()
}

func (t *VariableSizedType) IsInvalidType() bool {
	return t.Type.IsInvalidType()
}

func (t *VariableSizedType) IsStorable(results map[*Member]bool) bool {
	return t.Type.IsStorable(results)
}

func (*VariableSizedType) IsEquatable() bool {
	// TODO:
	return false
}

func (t *VariableSizedType) TypeAnnotationState() TypeAnnotationState {
	return t.Type.TypeAnnotationState()
}

func (t *VariableSizedType) RewriteWithRestrictedTypes() (Type, bool) {
	rewrittenType, rewritten := t.Type.RewriteWithRestrictedTypes()
	if rewritten {
		return &VariableSizedType{
			Type: rewrittenType,
		}, true
	} else {
		return t, false
	}
}

func (*VariableSizedType) isValueIndexableType() bool {
	return true
}

func (*VariableSizedType) AllowsValueIndexingAssignment() bool {
	return true
}

func (t *VariableSizedType) ElementType(_ bool) Type {
	return t.Type
}

func (t *VariableSizedType) IndexingType() Type {
	return &IntegerType{}
}

func (t *VariableSizedType) Unify(
	other Type,
	typeParameters map[*TypeParameter]Type,
	report func(err error),
	outerRange ast.Range,
) bool {
	otherArray, ok := other.(*VariableSizedType)
	if !ok {
		return false
	}

	return t.Type.Unify(otherArray.Type, typeParameters, report, outerRange)
}

func (t *VariableSizedType) Resolve(typeParameters map[*TypeParameter]Type) Type {
	newInnerType := t.Type.Resolve(typeParameters)
	if newInnerType == nil {
		return nil
	}

	return &VariableSizedType{
		Type: newInnerType,
	}
}

// ConstantSizedType is a constant sized array type
type ConstantSizedType struct {
	Type Type
	Size int64
}

func (*ConstantSizedType) IsType() {}

func (*ConstantSizedType) isArrayType() {}

func (t *ConstantSizedType) String() string {
	return fmt.Sprintf("[%s; %d]", t.Type, t.Size)
}

func (t *ConstantSizedType) QualifiedString() string {
	return fmt.Sprintf("[%s; %d]", t.Type.QualifiedString(), t.Size)
}

func (t *ConstantSizedType) ID() TypeID {
	return TypeID(fmt.Sprintf("[%s;%d]", t.Type.ID(), t.Size))
}

func (t *ConstantSizedType) Equal(other Type) bool {
	otherArray, ok := other.(*ConstantSizedType)
	if !ok {
		return false
	}

	return t.Type.Equal(otherArray.Type) &&
		t.Size == otherArray.Size
}

func (t *ConstantSizedType) GetMembers() map[string]MemberResolver {
	return getArrayMembers(t)
}

func (t *ConstantSizedType) IsResourceType() bool {
	return t.Type.IsResourceType()
}

func (t *ConstantSizedType) IsInvalidType() bool {
	return t.Type.IsInvalidType()
}

func (t *ConstantSizedType) IsStorable(results map[*Member]bool) bool {
	return t.Type.IsStorable(results)
}

func (*ConstantSizedType) IsEquatable() bool {
	// TODO:
	return false
}

func (t *ConstantSizedType) TypeAnnotationState() TypeAnnotationState {
	return t.Type.TypeAnnotationState()
}

func (t *ConstantSizedType) RewriteWithRestrictedTypes() (Type, bool) {
	rewrittenType, rewritten := t.Type.RewriteWithRestrictedTypes()
	if rewritten {
		return &ConstantSizedType{
			Type: rewrittenType,
			Size: t.Size,
		}, true
	} else {
		return t, false
	}
}

func (*ConstantSizedType) isValueIndexableType() bool {
	return true
}

func (*ConstantSizedType) AllowsValueIndexingAssignment() bool {
	return true
}

func (t *ConstantSizedType) ElementType(_ bool) Type {
	return t.Type
}

func (t *ConstantSizedType) IndexingType() Type {
	return &IntegerType{}
}

func (t *ConstantSizedType) Unify(
	other Type,
	typeParameters map[*TypeParameter]Type,
	report func(err error),
	outerRange ast.Range,
) bool {
	otherArray, ok := other.(*ConstantSizedType)
	if !ok {
		return false
	}

	if t.Size != otherArray.Size {
		return false
	}

	return t.Type.Unify(otherArray.Type, typeParameters, report, outerRange)
}

func (t *ConstantSizedType) Resolve(typeParameters map[*TypeParameter]Type) Type {
	newInnerType := t.Type.Resolve(typeParameters)
	if newInnerType == nil {
		return nil
	}

	return &ConstantSizedType{
		Type: newInnerType,
		Size: t.Size,
	}
}

// InvokableType

type InvokableType interface {
	Type
	InvocationFunctionType() *FunctionType
	CheckArgumentExpressions(checker *Checker, argumentExpressions []ast.Expression, invocationRange ast.Range)
	ArgumentLabels() []string
}

// Parameter

func formatParameter(spaces bool, label, identifier, typeAnnotation string) string {
	var builder strings.Builder

	if label != "" {
		builder.WriteString(label)
		if spaces {
			builder.WriteRune(' ')
		}
	}

	if identifier != "" {
		builder.WriteString(identifier)
		builder.WriteRune(':')
		if spaces {
			builder.WriteRune(' ')
		}
	}

	builder.WriteString(typeAnnotation)

	return builder.String()
}

type Parameter struct {
	Label          string
	Identifier     string
	TypeAnnotation *TypeAnnotation
}

func (p *Parameter) String() string {
	return formatParameter(
		true,
		p.Label,
		p.Identifier,
		p.TypeAnnotation.String(),
	)
}

func (p *Parameter) QualifiedString() string {
	return formatParameter(
		true,
		p.Label,
		p.Identifier,
		p.TypeAnnotation.QualifiedString(),
	)
}

// EffectiveArgumentLabel returns the effective argument label that
// an argument in a call must use:
// If no argument label is declared for parameter,
// the parameter name is used as the argument label
//
func (p *Parameter) EffectiveArgumentLabel() string {
	if p.Label != "" {
		return p.Label
	}
	return p.Identifier
}

// TypeParameter

type TypeParameter struct {
	Name      string
	TypeBound Type
	Optional  bool
}

func (p TypeParameter) string(typeFormatter func(Type) string) string {
	var builder strings.Builder
	builder.WriteString(p.Name)
	if p.TypeBound != nil {
		builder.WriteString(": ")
		builder.WriteString(typeFormatter(p.TypeBound))
	}
	return builder.String()
}

func (p TypeParameter) String() string {
	return p.string(func(t Type) string {
		return t.String()
	})
}

func (p TypeParameter) QualifiedString() string {
	return p.string(func(t Type) string {
		return t.QualifiedString()
	})
}

func (p TypeParameter) Equal(other *TypeParameter) bool {
	if p.Name != other.Name {
		return false
	}

	if p.TypeBound == nil {
		if other.TypeBound != nil {
			return false
		}
	} else {
		if other.TypeBound == nil ||
			!p.TypeBound.Equal(other.TypeBound) {

			return false
		}
	}

	return p.Optional == other.Optional
}

func (p TypeParameter) checkTypeBound(ty Type, typeRange ast.Range) error {
	if p.TypeBound == nil ||
		p.TypeBound.IsInvalidType() ||
		ty.IsInvalidType() {

		return nil
	}

	if !IsSubType(ty, p.TypeBound) {
		return &TypeMismatchError{
			ExpectedType: p.TypeBound,
			ActualType:   ty,
			Range:        typeRange,
		}
	}

	return nil
}

// Function types

func formatFunctionType(
	spaces bool,
	typeParameters []string,
	parameters []string,
	returnTypeAnnotation string,
) string {

	var builder strings.Builder
	builder.WriteRune('(')
	if len(typeParameters) > 0 {
		builder.WriteRune('<')
		for i, typeParameter := range typeParameters {
			if i > 0 {
				builder.WriteRune(',')
				if spaces {
					builder.WriteRune(' ')
				}
			}
			builder.WriteString(typeParameter)
		}
		builder.WriteRune('>')
	}
	builder.WriteRune('(')
	for i, parameter := range parameters {
		if i > 0 {
			builder.WriteRune(',')
			if spaces {
				builder.WriteRune(' ')
			}
		}
		builder.WriteString(parameter)
	}
	builder.WriteString("):")
	if spaces {
		builder.WriteRune(' ')
	}
	builder.WriteString(returnTypeAnnotation)
	builder.WriteRune(')')
	return builder.String()
}

// FunctionType
//
type FunctionType struct {
	TypeParameters        []*TypeParameter
	Parameters            []*Parameter
	ReturnTypeAnnotation  *TypeAnnotation
	RequiredArgumentCount *int
}

func (*FunctionType) IsType() {}

func (t *FunctionType) InvocationFunctionType() *FunctionType {
	return t
}

func (*FunctionType) CheckArgumentExpressions(checker *Checker, argumentExpressions []ast.Expression, invocationRange ast.Range) {
	// NO-OP: no checks for normal functions
}

func (t *FunctionType) String() string {

	typeParameters := make([]string, len(t.TypeParameters))

	for i, typeParameter := range t.TypeParameters {
		typeParameters[i] = typeParameter.String()
	}

	parameters := make([]string, len(t.Parameters))

	for i, parameter := range t.Parameters {
		parameters[i] = parameter.String()
	}

	returnTypeAnnotation := t.ReturnTypeAnnotation.String()

	return formatFunctionType(
		true,
		typeParameters,
		parameters,
		returnTypeAnnotation,
	)
}

func (t *FunctionType) QualifiedString() string {

	typeParameters := make([]string, len(t.TypeParameters))

	for i, typeParameter := range t.TypeParameters {
		typeParameters[i] = typeParameter.QualifiedString()
	}

	parameters := make([]string, len(t.Parameters))

	for i, parameter := range t.Parameters {
		parameters[i] = parameter.QualifiedString()
	}

	returnTypeAnnotation := t.ReturnTypeAnnotation.QualifiedString()

	return formatFunctionType(
		true,
		typeParameters,
		parameters,
		returnTypeAnnotation,
	)
}

// NOTE: parameter names and argument labels are *not* part of the ID!
func (t *FunctionType) ID() TypeID {
	typeParameters := make([]string, len(t.TypeParameters))

	for i, typeParameter := range t.TypeParameters {
		typeParameters[i] = string(typeParameter.TypeBound.ID())
	}

	parameters := make([]string, len(t.Parameters))

	for i, parameter := range t.Parameters {
		parameters[i] = string(parameter.TypeAnnotation.Type.ID())
	}

	returnTypeAnnotation := string(t.ReturnTypeAnnotation.Type.ID())

	return TypeID(
		formatFunctionType(
			false,
			typeParameters,
			parameters,
			returnTypeAnnotation,
		),
	)
}

// NOTE: parameter names and argument labels are intentionally *not* considered!
func (t *FunctionType) Equal(other Type) bool {
	otherFunction, ok := other.(*FunctionType)
	if !ok {
		return false
	}

	// type parameters

	if len(t.TypeParameters) != len(otherFunction.TypeParameters) {
		return false
	}

	for i, typeParameter := range t.TypeParameters {
		otherTypeParameter := otherFunction.TypeParameters[i]
		if !typeParameter.Equal(otherTypeParameter) {
			return false
		}
	}

	// parameters

	if len(t.Parameters) != len(otherFunction.Parameters) {
		return false
	}

	for i, parameter := range t.Parameters {
		otherParameter := otherFunction.Parameters[i]
		if !parameter.TypeAnnotation.Equal(otherParameter.TypeAnnotation) {
			return false
		}
	}

	// return type

	return t.ReturnTypeAnnotation.Equal(otherFunction.ReturnTypeAnnotation)
}

func (t *FunctionType) HasSameArgumentLabels(other *FunctionType) bool {
	if len(t.Parameters) != len(other.Parameters) {
		return false
	}

	for i, parameter := range t.Parameters {
		otherParameter := other.Parameters[i]
		if parameter.EffectiveArgumentLabel() != otherParameter.EffectiveArgumentLabel() {
			return false
		}
	}

	return true
}

func (*FunctionType) IsResourceType() bool {
	return false
}

func (t *FunctionType) IsInvalidType() bool {

	for _, typeParameter := range t.TypeParameters {

		if typeParameter.TypeBound != nil &&
			typeParameter.TypeBound.IsInvalidType() {

			return true
		}
	}

	for _, parameter := range t.Parameters {
		if parameter.TypeAnnotation.Type.IsInvalidType() {
			return true
		}
	}

	return t.ReturnTypeAnnotation.Type.IsInvalidType()
}

func (t *FunctionType) IsStorable(_ map[*Member]bool) bool {
	// Functions cannot be stored, as they cannot be serialized
	return false
}

func (*FunctionType) IsEquatable() bool {
	return false
}

func (t *FunctionType) TypeAnnotationState() TypeAnnotationState {

	for _, typeParameter := range t.TypeParameters {
		typeParameterTypeAnnotationState := typeParameter.TypeBound.TypeAnnotationState()
		if typeParameterTypeAnnotationState != TypeAnnotationStateValid {
			return typeParameterTypeAnnotationState
		}
	}

	for _, parameter := range t.Parameters {
		parameterTypeAnnotationState := parameter.TypeAnnotation.TypeAnnotationState()
		if parameterTypeAnnotationState != TypeAnnotationStateValid {
			return parameterTypeAnnotationState
		}
	}

	returnTypeAnnotationState := t.ReturnTypeAnnotation.TypeAnnotationState()
	if returnTypeAnnotationState != TypeAnnotationStateValid {
		return returnTypeAnnotationState
	}

	return TypeAnnotationStateValid
}

func (t *FunctionType) RewriteWithRestrictedTypes() (Type, bool) {
	anyRewritten := false

	rewrittenTypeParameterTypeBounds := map[*TypeParameter]Type{}

	for _, typeParameter := range t.TypeParameters {
		if typeParameter.TypeBound == nil {
			continue
		}

		rewrittenType, rewritten := typeParameter.TypeBound.RewriteWithRestrictedTypes()
		if rewritten {
			anyRewritten = true
			rewrittenTypeParameterTypeBounds[typeParameter] = rewrittenType
		}
	}

	rewrittenParameterTypes := map[*Parameter]Type{}

	for _, parameter := range t.Parameters {
		rewrittenType, rewritten := parameter.TypeAnnotation.Type.RewriteWithRestrictedTypes()
		if rewritten {
			anyRewritten = true
			rewrittenParameterTypes[parameter] = rewrittenType
		}
	}

	rewrittenReturnType, rewritten := t.ReturnTypeAnnotation.Type.RewriteWithRestrictedTypes()
	if rewritten {
		anyRewritten = true
	}

	if anyRewritten {
		var rewrittenTypeParameters []*TypeParameter
		if len(t.TypeParameters) > 0 {
			rewrittenTypeParameters = make([]*TypeParameter, len(t.TypeParameters))
			for i, typeParameter := range t.TypeParameters {
				rewrittenTypeBound, ok := rewrittenTypeParameterTypeBounds[typeParameter]
				if ok {
					rewrittenTypeParameters[i] = &TypeParameter{
						Name:      typeParameter.Name,
						TypeBound: rewrittenTypeBound,
						Optional:  typeParameter.Optional,
					}
				} else {
					rewrittenTypeParameters[i] = typeParameter
				}
			}
		}

		var rewrittenParameters []*Parameter
		if len(t.Parameters) > 0 {
			rewrittenParameters = make([]*Parameter, len(t.Parameters))
			for i, parameter := range t.Parameters {
				rewrittenParameterType, ok := rewrittenParameterTypes[parameter]
				if ok {
					rewrittenParameters[i] = &Parameter{
						Label:          parameter.Label,
						Identifier:     parameter.Identifier,
						TypeAnnotation: NewTypeAnnotation(rewrittenParameterType),
					}
				} else {
					rewrittenParameters[i] = parameter
				}
			}
		}

		return &FunctionType{
			TypeParameters:        rewrittenTypeParameters,
			Parameters:            rewrittenParameters,
			ReturnTypeAnnotation:  NewTypeAnnotation(rewrittenReturnType),
			RequiredArgumentCount: t.RequiredArgumentCount,
		}, true
	} else {
		return t, false
	}
}

func (t *FunctionType) ArgumentLabels() (argumentLabels []string) {

	for _, parameter := range t.Parameters {

		argumentLabel := ArgumentLabelNotRequired
		if parameter.Label != "" {
			argumentLabel = parameter.Label
		} else if parameter.Identifier != "" {
			argumentLabel = parameter.Identifier
		}

		argumentLabels = append(argumentLabels, argumentLabel)
	}

	return
}

func (t *FunctionType) Unify(
	other Type,
	typeParameters map[*TypeParameter]Type,
	report func(err error),
	outerRange ast.Range,
) (
	result bool,
) {

	otherFunction, ok := other.(*FunctionType)
	if !ok {
		return false
	}

	// TODO: type parameters ?

	if len(t.TypeParameters) > 0 ||
		len(otherFunction.TypeParameters) > 0 {

		return false
	}

	// parameters

	if len(t.Parameters) != len(otherFunction.Parameters) {
		return false
	}

	for i, parameter := range t.Parameters {
		otherParameter := otherFunction.Parameters[i]
		parameterUnified := parameter.TypeAnnotation.Type.Unify(
			otherParameter.TypeAnnotation.Type,
			typeParameters,
			report,
			outerRange,
		)
		result = result || parameterUnified
	}

	// return type

	returnTypeUnified := t.ReturnTypeAnnotation.Type.Unify(
		otherFunction.ReturnTypeAnnotation.Type,
		typeParameters,
		report,
		outerRange,
	)

	result = result || returnTypeUnified

	return
}

func (t *FunctionType) Resolve(typeParameters map[*TypeParameter]Type) Type {

	// TODO: type parameters ?

	// parameters

	var newParameters []*Parameter

	for _, parameter := range t.Parameters {
		newParameterType := parameter.TypeAnnotation.Type.Resolve(typeParameters)
		if newParameterType == nil {
			return nil
		}

		newParameters = append(newParameters,
			&Parameter{
				Label:          parameter.Label,
				Identifier:     parameter.Identifier,
				TypeAnnotation: NewTypeAnnotation(newParameterType),
			},
		)
	}

	// return type

	newReturnType := t.ReturnTypeAnnotation.Type.Resolve(typeParameters)
	if newReturnType == nil {
		return nil
	}

	return &FunctionType{
		Parameters:            newParameters,
		ReturnTypeAnnotation:  NewTypeAnnotation(newReturnType),
		RequiredArgumentCount: t.RequiredArgumentCount,
	}

}

func (t *FunctionType) GetMembers() map[string]MemberResolver {
	return withBuiltinMembers(t, nil)
}

// SpecialFunctionType is the the type representing a special function,
// i.e., a constructor or destructor

type SpecialFunctionType struct {
	*FunctionType
	Members map[string]*Member
}

func (t *SpecialFunctionType) GetMembers() map[string]MemberResolver {
	// TODO: optimize
	members := make(map[string]MemberResolver, len(t.Members))
	for name, loopMember := range t.Members {
		// NOTE: don't capture loop variable
		member := loopMember
		members[name] = MemberResolver{
			Kind: member.DeclarationKind,
			Resolve: func(_ string, _ ast.Range, _ func(error)) *Member {
				return member
			},
		}
	}

	return withBuiltinMembers(t, members)
}

// CheckedFunctionType is the the type representing a function that checks the arguments,
// e.g., integer functions

type ArgumentExpressionsCheck func(
	checker *Checker,
	argumentExpressions []ast.Expression,
	invocationRange ast.Range,
)

type CheckedFunctionType struct {
	*FunctionType
	ArgumentExpressionsCheck ArgumentExpressionsCheck
}

func (t *CheckedFunctionType) CheckArgumentExpressions(
	checker *Checker,
	argumentExpressions []ast.Expression,
	invocationRange ast.Range,
) {
	t.ArgumentExpressionsCheck(checker, argumentExpressions, invocationRange)
}

// baseTypes are the nominal types available in programs

var baseTypes map[string]Type

func init() {

	baseTypes = map[string]Type{
		"": &VoidType{},
	}

	otherTypes := []Type{
		&MetaType{},
		&VoidType{},
		&AnyStructType{},
		&AnyResourceType{},
		&NeverType{},
		&BoolType{},
		&CharacterType{},
		&StringType{},
		&AddressType{},
		&AuthAccountType{},
		&PublicAccountType{},
		&PathType{},
		&CapabilityType{},
	}

	types := append(
		AllNumberTypes,
		otherTypes...,
	)

	for _, ty := range types {
		typeName := ty.String()

		// check type is not accidentally redeclared
		if _, ok := baseTypes[typeName]; ok {
			panic(errors.NewUnreachableError())
		}

		baseTypes[typeName] = ty
	}
}

// baseValues are the values available in programs

var BaseValues = map[string]ValueDeclaration{}

type baseFunction struct {
	name           string
	invokableType  InvokableType
	argumentLabels []string
}

func (f baseFunction) ValueDeclarationType() Type {
	return f.invokableType
}

func (baseFunction) ValueDeclarationKind() common.DeclarationKind {
	return common.DeclarationKindFunction
}

func (baseFunction) ValueDeclarationPosition() ast.Position {
	return ast.Position{}
}

func (baseFunction) ValueDeclarationIsConstant() bool {
	return true
}

func (f baseFunction) ValueDeclarationArgumentLabels() []string {
	return f.argumentLabels
}

var AllSignedFixedPointTypes = []Type{
	&Fix64Type{},
}

var AllUnsignedFixedPointTypes = []Type{
	&UFix64Type{},
}

var AllFixedPointTypes = append(
	append(
		AllUnsignedFixedPointTypes[:],
		AllSignedFixedPointTypes...,
	),
	&FixedPointType{},
	&SignedFixedPointType{},
)

var AllSignedIntegerTypes = []Type{
	&IntType{},
	&Int8Type{},
	&Int16Type{},
	&Int32Type{},
	&Int64Type{},
	&Int128Type{},
	&Int256Type{},
}

var AllUnsignedIntegerTypes = []Type{
	// UInt*
	&UIntType{},
	&UInt8Type{},
	&UInt16Type{},
	&UInt32Type{},
	&UInt64Type{},
	&UInt128Type{},
	&UInt256Type{},
	// Word*
	&Word8Type{},
	&Word16Type{},
	&Word32Type{},
	&Word64Type{},
}

var AllIntegerTypes = append(
	append(
		AllUnsignedIntegerTypes[:],
		AllSignedIntegerTypes...,
	),
	&IntegerType{},
	&SignedIntegerType{},
)

var AllNumberTypes = append(
	append(
		AllIntegerTypes[:],
		AllFixedPointTypes...,
	),
	&NumberType{},
	&SignedNumberType{},
)

func init() {

	// Declare a conversion function for all (leaf) number types

	for _, numberType := range AllNumberTypes {

		switch numberType.(type) {
		case *NumberType, *SignedNumberType,
			*IntegerType, *SignedIntegerType,
			*FixedPointType, *SignedFixedPointType:
			continue

		default:
			typeName := numberType.String()

			// Check that the type is not accidentally redeclared

			if _, ok := BaseValues[typeName]; ok {
				panic(errors.NewUnreachableError())
			}

			BaseValues[typeName] = baseFunction{
				name: typeName,
				invokableType: &CheckedFunctionType{
					FunctionType: &FunctionType{
						Parameters: []*Parameter{
							{
								Label:          ArgumentLabelNotRequired,
								Identifier:     "value",
								TypeAnnotation: NewTypeAnnotation(&NumberType{}),
							},
						},
						ReturnTypeAnnotation: &TypeAnnotation{Type: numberType},
					},
					ArgumentExpressionsCheck: numberFunctionArgumentExpressionsChecker(numberType),
				},
			}
		}
	}
}

func init() {
	addressType := &AddressType{}
	typeName := addressType.String()

	// check type is not accidentally redeclared
	if _, ok := BaseValues[typeName]; ok {
		panic(errors.NewUnreachableError())
	}

	BaseValues[typeName] = baseFunction{
		name: typeName,
		invokableType: &CheckedFunctionType{
			FunctionType: &FunctionType{
				Parameters: []*Parameter{
					{
						Label:          ArgumentLabelNotRequired,
						Identifier:     "value",
						TypeAnnotation: NewTypeAnnotation(&IntegerType{}),
					},
				},
				ReturnTypeAnnotation: &TypeAnnotation{Type: addressType},
			},
			ArgumentExpressionsCheck: func(checker *Checker, argumentExpressions []ast.Expression, _ ast.Range) {
				if len(argumentExpressions) < 1 {
					return
				}

				intExpression, ok := argumentExpressions[0].(*ast.IntegerExpression)
				if !ok {
					return
				}

				checker.checkAddressLiteral(intExpression)
			},
		},
	}
}

func numberFunctionArgumentExpressionsChecker(targetType Type) ArgumentExpressionsCheck {
	return func(checker *Checker, arguments []ast.Expression, invocationRange ast.Range) {
		if len(arguments) < 1 {
			return
		}

		argument := arguments[0]

		switch argument := argument.(type) {
		case *ast.IntegerExpression:
			if checker.checkIntegerLiteral(argument, targetType) {

				suggestIntegerLiteralConversionReplacement(checker, argument, targetType, invocationRange)
			}

		case *ast.FixedPointExpression:
			if checker.checkFixedPointLiteral(argument, targetType) {

				suggestFixedPointLiteralConversionReplacement(checker, targetType, argument, invocationRange)
			}
		}
	}
}

func suggestIntegerLiteralConversionReplacement(
	checker *Checker,
	argument *ast.IntegerExpression,
	targetType Type,
	invocationRange ast.Range,
) {
	negative := argument.Value.Sign() < 0

	if IsSubType(targetType, &FixedPointType{}) {

		// If the integer literal is converted to a fixed-point type,
		// suggest replacing it with a fixed-point literal

		signed := IsSubType(targetType, &SignedFixedPointType{})

		var hintExpression ast.Expression = &ast.FixedPointExpression{
			Negative:        negative,
			UnsignedInteger: new(big.Int).Abs(argument.Value),
			Fractional:      new(big.Int),
			Scale:           1,
		}

		// If the fixed-point literal is positive
		// and the the target fixed-point type is signed,
		// then a static cast is required

		if !negative && signed {
			hintExpression = &ast.CastingExpression{
				Expression: hintExpression,
				Operation:  ast.OperationCast,
				TypeAnnotation: &ast.TypeAnnotation{
					IsResource: false,
					Type: &ast.NominalType{
						Identifier: ast.Identifier{
							Identifier: targetType.String(),
						},
					},
				},
			}
		}

		checker.hint(
			&ReplacementHint{
				Expression: hintExpression,
				Range:      invocationRange,
			},
		)

	} else if IsSubType(targetType, &IntegerType{}) {

		// If the integer literal is converted to an integer type,
		// suggest replacing it with a fixed-point literal

		var hintExpression ast.Expression = argument

		// If the target type is not `Int`,
		// then a static cast is required,
		// as all integer literals (positive and negative)
		// are inferred to be of type `Int`

		if !IsSubType(targetType, &IntType{}) {
			hintExpression = &ast.CastingExpression{
				Expression: hintExpression,
				Operation:  ast.OperationCast,
				TypeAnnotation: &ast.TypeAnnotation{
					IsResource: false,
					Type: &ast.NominalType{
						Identifier: ast.Identifier{
							Identifier: targetType.String(),
						},
					},
				},
			}
		}

		checker.hint(
			&ReplacementHint{
				Expression: hintExpression,
				Range:      invocationRange,
			},
		)
	}
}

func suggestFixedPointLiteralConversionReplacement(
	checker *Checker,
	targetType Type,
	argument *ast.FixedPointExpression,
	invocationRange ast.Range,
) {
	// If the fixed-point literal is converted to a fixed-point type,
	// suggest replacing it with a fixed-point literal

	if !IsSubType(targetType, &FixedPointType{}) {
		return
	}

	negative := argument.Negative
	signed := IsSubType(targetType, &SignedFixedPointType{})

	if (!negative && !signed) || (negative && signed) {
		checker.hint(
			&ReplacementHint{
				Expression: argument,
				Range:      invocationRange,
			},
		)
	}
}

func init() {

	metaType := &MetaType{}
	typeName := metaType.String()

	// check type is not accidentally redeclared
	if _, ok := BaseValues[typeName]; ok {
		panic(errors.NewUnreachableError())
	}

	BaseValues[typeName] = baseFunction{
		name: typeName,
		invokableType: &FunctionType{
			TypeParameters:       []*TypeParameter{{Name: "T"}},
			ReturnTypeAnnotation: &TypeAnnotation{Type: metaType},
		},
	}
}

// CompositeType

type CompositeType struct {
	Location   ast.Location
	Identifier string
	Kind       common.CompositeKind
	// an internal set of field `ExplicitInterfaceConformances`
	explicitInterfaceConformanceSet     InterfaceSet
	ExplicitInterfaceConformances       []*InterfaceType
	ImplicitTypeRequirementConformances []*CompositeType
	Members                             map[string]*Member
	Fields                              []string
	// TODO: add support for overloaded initializers
	ConstructorParameters []*Parameter
	nestedTypes           map[string]Type
	ContainerType         Type
}

func (t *CompositeType) ExplicitInterfaceConformanceSet() InterfaceSet {
	if t.explicitInterfaceConformanceSet == nil {
		// TODO: also include conformances' conformances recursively
		//   once interface can have conformances

		t.explicitInterfaceConformanceSet = make(InterfaceSet, len(t.ExplicitInterfaceConformances))
		for _, conformance := range t.ExplicitInterfaceConformances {
			t.explicitInterfaceConformanceSet.Add(conformance)
		}
	}

	return t.explicitInterfaceConformanceSet
}

func (t *CompositeType) AddImplicitTypeRequirementConformance(typeRequirement *CompositeType) {
	t.ImplicitTypeRequirementConformances =
		append(t.ImplicitTypeRequirementConformances, typeRequirement)
}

func (*CompositeType) IsType() {}

func (t *CompositeType) String() string {
	return t.Identifier
}

func (t *CompositeType) QualifiedString() string {
	return t.QualifiedIdentifier()
}

func (t *CompositeType) GetContainerType() Type {
	return t.ContainerType
}

func (t *CompositeType) GetCompositeKind() common.CompositeKind {
	return t.Kind
}

func (t *CompositeType) GetLocation() ast.Location {
	return t.Location
}

func (t *CompositeType) QualifiedIdentifier() string {
	return qualifiedIdentifier(t.Identifier, t.ContainerType)
}

func (t *CompositeType) ID() TypeID {
	return TypeID(fmt.Sprintf("%s.%s", t.Location.ID(), t.QualifiedIdentifier()))
}

func (t *CompositeType) Equal(other Type) bool {
	otherStructure, ok := other.(*CompositeType)
	if !ok {
		return false
	}

	return otherStructure.Kind == t.Kind &&
		otherStructure.ID() == t.ID()
}

func (t *CompositeType) GetMembers() map[string]MemberResolver {
	// TODO: optimize
	members := make(map[string]MemberResolver, len(t.Members))
	for name, loopMember := range t.Members {
		// NOTE: don't capture loop variable
		member := loopMember
		members[name] = MemberResolver{
			Kind: member.DeclarationKind,
			Resolve: func(_ string, _ ast.Range, _ func(error)) *Member {
				return member
			},
		}
	}

	// Check conformances.
	// If this composite type results from a normal composite declaration,
	// it must have members declared for all interfaces it conforms to.
	// However, if this composite type is a type requirement,
	// it acts like an interface and does not have to declare members.

	for conformance := range t.ExplicitInterfaceConformanceSet() {
		for name, resolver := range conformance.GetMembers() {
			if _, ok := members[name]; !ok {
				members[name] = resolver
			}
		}
	}

	return withBuiltinMembers(t, members)
}

func (t *CompositeType) IsResourceType() bool {
	return t.Kind == common.CompositeKindResource
}

func (*CompositeType) IsInvalidType() bool {
	return false
}

func (t *CompositeType) IsStorable(results map[*Member]bool) bool {

	// If this composite type has a member which is non-storable,
	// then the composite type is not storable.

	for _, member := range t.Members {
		if !member.IsStorable(results) {
			return false
		}
	}

	return true
}

func (*CompositeType) IsEquatable() bool {
	// TODO:
	return false
}

func (*CompositeType) TypeAnnotationState() TypeAnnotationState {
	return TypeAnnotationStateValid
}

func (t *CompositeType) RewriteWithRestrictedTypes() (result Type, rewritten bool) {
	return t, false
}

func (t *CompositeType) InterfaceType() *InterfaceType {
	return &InterfaceType{
		Location:              t.Location,
		Identifier:            t.Identifier,
		CompositeKind:         t.Kind,
		Members:               t.Members,
		Fields:                t.Fields,
		InitializerParameters: t.ConstructorParameters,
		ContainerType:         t.ContainerType,
		nestedTypes:           t.nestedTypes,
	}
}

func (t *CompositeType) TypeRequirements() []*CompositeType {

	var typeRequirements []*CompositeType

	if containerComposite, ok := t.ContainerType.(*CompositeType); ok {
		for _, conformance := range containerComposite.ExplicitInterfaceConformances {
			ty := conformance.nestedTypes[t.Identifier]
			typeRequirement, ok := ty.(*CompositeType)
			if !ok {
				continue
			}

			typeRequirements = append(typeRequirements, typeRequirement)
		}
	}

	return typeRequirements
}

func (*CompositeType) Unify(_ Type, _ map[*TypeParameter]Type, _ func(err error), _ ast.Range) bool {
	// TODO:
	return false
}

func (t *CompositeType) Resolve(_ map[*TypeParameter]Type) Type {
	return t
}

func (t *CompositeType) NestedTypes() map[string]Type {
	return t.nestedTypes
}

// AuthAccountType represents the authorized access to an account.
// Access to an AuthAccount means having full access to its storage, public keys, and code.
// Only signed transactions can get the AuthAccount for an account.

type AuthAccountType struct{}

func (*AuthAccountType) IsType() {}

func (*AuthAccountType) String() string {
	return "AuthAccount"
}

func (*AuthAccountType) QualifiedString() string {
	return "AuthAccount"
}

func (*AuthAccountType) ID() TypeID {
	return "AuthAccount"
}

func (*AuthAccountType) Equal(other Type) bool {
	_, ok := other.(*AuthAccountType)
	return ok
}

func (*AuthAccountType) IsResourceType() bool {
	return false
}

func (*AuthAccountType) IsInvalidType() bool {
	return false
}

func (*AuthAccountType) IsStorable(_ map[*Member]bool) bool {
	return false
}

func (*AuthAccountType) IsEquatable() bool {
	return false
}

func (*AuthAccountType) TypeAnnotationState() TypeAnnotationState {
	return TypeAnnotationStateValid
}

func (t *AuthAccountType) RewriteWithRestrictedTypes() (result Type, rewritten bool) {
	return t, false
}

var authAccountTypeSetCodeFunctionType = &FunctionType{
	Parameters: []*Parameter{
		{
			Label:      ArgumentLabelNotRequired,
			Identifier: "code",
			TypeAnnotation: NewTypeAnnotation(
				&VariableSizedType{
					Type: &UInt8Type{},
				},
			),
		},
	},
	ReturnTypeAnnotation: NewTypeAnnotation(
		&VoidType{},
	),
	// additional arguments are passed to the contract initializer
	RequiredArgumentCount: (func() *int {
		var count = 1
		return &count
	})(),
}

const authAccountTypeSetCodeFieldDocString = `
Updates the code in the account with the given code
`

const authAccountTypeUnsafeNotInitializingSetCodeFieldDocString = `
**UNSAFE**

Updates the code in the  account with the given code without constructing and initializing the contract
`

var authAccountTypeAddPublicKeyFunctionType = &FunctionType{
	Parameters: []*Parameter{
		{
			Label:      ArgumentLabelNotRequired,
			Identifier: "key",
			TypeAnnotation: NewTypeAnnotation(
				&VariableSizedType{
					Type: &UInt8Type{},
				},
			),
		},
	},
	ReturnTypeAnnotation: NewTypeAnnotation(
		&VoidType{},
	),
}

const authAccountTypeAddPublicKeyFunctionDocString = `
Adds the given byte representation of a public key to the account's keys
`

var authAccountTypeRemovePublicKeyFunctionType = &FunctionType{
	Parameters: []*Parameter{
		{
			Label:      ArgumentLabelNotRequired,
			Identifier: "index",
			TypeAnnotation: NewTypeAnnotation(
				&IntType{},
			),
		},
	},
	ReturnTypeAnnotation: NewTypeAnnotation(
		&VoidType{},
	),
}

const authAccountTypeRemovePublicKeyFunctionDocString = `
Removes the public key at the given index from the account's keys
`

var authAccountTypeSaveFunctionType = func() *FunctionType {

	typeParameter := &TypeParameter{
		Name:      "T",
		TypeBound: &StorableType{},
	}

	return &FunctionType{
		TypeParameters: []*TypeParameter{
			typeParameter,
		},
		Parameters: []*Parameter{
			{
				Label:      ArgumentLabelNotRequired,
				Identifier: "value",
				TypeAnnotation: NewTypeAnnotation(
					&GenericType{
						TypeParameter: typeParameter,
					},
				),
			},
			{
				Label:          "to",
				Identifier:     "path",
				TypeAnnotation: NewTypeAnnotation(&PathType{}),
			},
		},
		ReturnTypeAnnotation: NewTypeAnnotation(&VoidType{}),
	}
}()

const authAccountTypeSaveFunctionDocString = `
Saves the given object into the account's storage at the given path.
Resources are moved into storage, and structures are copied.

If there is already an object stored under the given path, the program aborts.

The path must be a storage path, i.e., only the domain ` + "`storage`" + ` is allowed
`

var authAccountTypeLoadFunctionType = func() *FunctionType {

	typeParameter := &TypeParameter{
		Name:      "T",
		TypeBound: &StorableType{},
	}

	return &FunctionType{
		TypeParameters: []*TypeParameter{
			typeParameter,
		},
		Parameters: []*Parameter{
			{
				Label:          "from",
				Identifier:     "path",
				TypeAnnotation: NewTypeAnnotation(&PathType{}),
			},
		},
		ReturnTypeAnnotation: NewTypeAnnotation(
			&OptionalType{
				Type: &GenericType{
					TypeParameter: typeParameter,
				},
			},
		),
	}
}()

const authAccountTypeLoadFunctionDocString = `
Loads an object from the account's storage which is stored under the given path, or nil if no object is stored under the given path.

If there is an object stored, the stored resource or structure is moved out of storage and returned as an optional.

When the function returns, the storage no longer contains an object under the given path.

The given type must be a supertype of the type of the loaded object.
If it is not, the function returns nil.
The given type must not necessarily be exactly the same as the type of the loaded object.

The path must be a storage path, i.e., only the domain ` + "`storage`" + ` is allowed
`

var authAccountTypeCopyFunctionType = func() *FunctionType {

	typeParameter := &TypeParameter{
		Name:      "T",
		TypeBound: &AnyStructType{},
	}

	return &FunctionType{
		TypeParameters: []*TypeParameter{
			typeParameter,
		},
		Parameters: []*Parameter{
			{
				Label:          "from",
				Identifier:     "path",
				TypeAnnotation: NewTypeAnnotation(&PathType{}),
			},
		},
		ReturnTypeAnnotation: NewTypeAnnotation(
			&OptionalType{
				Type: &GenericType{
					TypeParameter: typeParameter,
				},
			},
		),
	}
}()

const authAccountTypeCopyFunctionDocString = `
Returns a copy of a structure stored in account storage under the given path, without removing it from storage, or nil if no object is stored under the given path.

If there is a structure stored, it is copied.
The structure stays stored in storage after the function returns.

The given type must be a supertype of the type of the copied structure.
If it is not, the function returns nil.
The given type must not necessarily be exactly the same as the type of the copied structure.

The path must be a storage path, i.e., only the domain ` + "`storage`" + ` is allowed
`

var authAccountTypeBorrowFunctionType = func() *FunctionType {

	typeParameter := &TypeParameter{
		TypeBound: &ReferenceType{
			Type: &AnyType{},
		},
		Name: "T",
	}

	return &FunctionType{
		TypeParameters: []*TypeParameter{
			typeParameter,
		},
		Parameters: []*Parameter{
			{
				Label:          "from",
				Identifier:     "path",
				TypeAnnotation: NewTypeAnnotation(&PathType{}),
			},
		},
		ReturnTypeAnnotation: NewTypeAnnotation(
			&OptionalType{
				Type: &GenericType{
					TypeParameter: typeParameter,
				},
			},
		),
	}
}()

const authAccountTypeBorrowFunctionDocString = `
Returns a reference to an object in storage without removing it from storage.

If no object is stored under the given path, the function returns nil.
If there is an object stored, a reference is returned as an optional.

The given type must be a reference type.
It must be possible to create the given reference type for the borrowed object.
If it is not, the function returns nil.

The given type must not necessarily be exactly the same as the type of the borrowed object.

The path must be a storage path, i.e., only the domain ` + "`storage`" + ` is allowed
`

var authAccountTypeLinkFunctionType = func() *FunctionType {

	typeParameter := &TypeParameter{
		TypeBound: &ReferenceType{
			Type: &AnyType{},
		},
		Name: "T",
	}

	return &FunctionType{
		TypeParameters: []*TypeParameter{
			typeParameter,
		},
		Parameters: []*Parameter{
			{
				Label:          ArgumentLabelNotRequired,
				Identifier:     "newCapabilityPath",
				TypeAnnotation: NewTypeAnnotation(&PathType{}),
			},
			{
				Identifier:     "target",
				TypeAnnotation: NewTypeAnnotation(&PathType{}),
			},
		},
		ReturnTypeAnnotation: NewTypeAnnotation(
			&OptionalType{
				Type: &CapabilityType{
					BorrowType: &GenericType{
						TypeParameter: typeParameter,
					},
				},
			},
		),
	}
}()

const authAccountTypeLinkFunctionDocString = `
Creates a capability at the given public or private path which targets the given public, private, or storage path.
The target path leads to the object that will provide the functionality defined by this capability.

The given type defines how the capability can be borrowed, i.e., how the stored value can be accessed.

Returns nil if a link for the given capability path already exists, or the newly created capability if not.

It is not necessary for the target path to lead to a valid object; the target path could be empty, or could lead to an object which does not provide the necessary type interface:
The link function does **not** check if the target path is valid/exists at the time the capability is created and does **not** check if the target value conforms to the given type.
The link is latent. The target value might be stored after the link is created, and the target value might be moved out after the link has been created.
`

var authAccountTypeUnlinkFunctionType = &FunctionType{
	Parameters: []*Parameter{
		{
			Label:          ArgumentLabelNotRequired,
			Identifier:     "capabilityPath",
			TypeAnnotation: NewTypeAnnotation(&PathType{}),
		},
	},
	ReturnTypeAnnotation: NewTypeAnnotation(&VoidType{}),
}

const authAccountTypeUnlinkFunctionDocString = `
Removes the capability at the given public or private path
`

var accountTypeGetCapabilityFunctionType = func() *FunctionType {

	typeParameter := &TypeParameter{
		TypeBound: &ReferenceType{
			Type: &AnyType{},
		},
		Name:     "T",
		Optional: true,
	}

	return &FunctionType{
		TypeParameters: []*TypeParameter{
			typeParameter,
		},
		Parameters: []*Parameter{
			{
				Label:          ArgumentLabelNotRequired,
				Identifier:     "capabilityPath",
				TypeAnnotation: NewTypeAnnotation(&PathType{}),
			},
		},
		ReturnTypeAnnotation: NewTypeAnnotation(
			&OptionalType{
				Type: &CapabilityType{
					BorrowType: &GenericType{
						TypeParameter: typeParameter,
					},
				},
			},
		),
	}
}()

const authAccountTypeGetCapabilityFunctionDocString = `
Returns the capability at the given private or public path, or nil if it does not exist
`

var accountTypeGetLinkTargetFunctionType = &FunctionType{
	Parameters: []*Parameter{
		{
			Label:          ArgumentLabelNotRequired,
			Identifier:     "capabilityPath",
			TypeAnnotation: NewTypeAnnotation(&PathType{}),
		},
	},
	ReturnTypeAnnotation: NewTypeAnnotation(
		&OptionalType{
			Type: &PathType{},
		},
	),
}

const accountTypeGetLinkTargetFunctionDocString = `
Returns the target path of the capability at the given public or private path, or nil if there exists no capability at the given path.
`

const accountTypeAddressFieldDocString = `
The address of the account
`

func (t *AuthAccountType) GetMembers() map[string]MemberResolver {
	return withBuiltinMembers(t, map[string]MemberResolver{
		"address": {
			Kind: common.DeclarationKindField,
			Resolve: func(identifier string, _ ast.Range, _ func(error)) *Member {
				return NewPublicConstantFieldMember(
					t,
					identifier,
					&AddressType{},
					accountTypeAddressFieldDocString,
				)
			},
		},
		"setCode": {
			Kind: common.DeclarationKindFunction,
			Resolve: func(identifier string, _ ast.Range, _ func(error)) *Member {
				return NewPublicFunctionMember(
					t,
					identifier,
					authAccountTypeSetCodeFunctionType,
					authAccountTypeSetCodeFieldDocString,
				)
			},
		},
		"unsafeNotInitializingSetCode": {
			Kind: common.DeclarationKindFunction,
			Resolve: func(identifier string, _ ast.Range, _ func(error)) *Member {
				return NewPublicFunctionMember(
					t,
					identifier,
					authAccountTypeSetCodeFunctionType,
					authAccountTypeUnsafeNotInitializingSetCodeFieldDocString,
				)
			},
		},
		"addPublicKey": {
			Kind: common.DeclarationKindFunction,
			Resolve: func(identifier string, _ ast.Range, _ func(error)) *Member {
				return NewPublicFunctionMember(
					t,
					identifier,
					authAccountTypeAddPublicKeyFunctionType,
					authAccountTypeAddPublicKeyFunctionDocString,
				)
			},
		},
		"removePublicKey": {
			Kind: common.DeclarationKindFunction,
			Resolve: func(identifier string, _ ast.Range, _ func(error)) *Member {
				return NewPublicFunctionMember(
					t,
					identifier,
					authAccountTypeRemovePublicKeyFunctionType,
					authAccountTypeRemovePublicKeyFunctionDocString,
				)
			},
		},
		"save": {
			Kind: common.DeclarationKindFunction,
			Resolve: func(identifier string, _ ast.Range, _ func(error)) *Member {
				return NewPublicFunctionMember(
					t,
					identifier,
					authAccountTypeSaveFunctionType,
					authAccountTypeSaveFunctionDocString,
				)
			},
		},
		"load": {
			Kind: common.DeclarationKindFunction,
			Resolve: func(identifier string, _ ast.Range, _ func(error)) *Member {
				return NewPublicFunctionMember(
					t,
					identifier,
					authAccountTypeLoadFunctionType,
					authAccountTypeLoadFunctionDocString,
				)
			},
		},
		"copy": {
			Kind: common.DeclarationKindFunction,
			Resolve: func(identifier string, _ ast.Range, _ func(error)) *Member {
				return NewPublicFunctionMember(
					t,
					identifier,
					authAccountTypeCopyFunctionType,
					authAccountTypeCopyFunctionDocString,
				)
			},
		},
		"borrow": {
			Kind: common.DeclarationKindFunction,
			Resolve: func(identifier string, _ ast.Range, _ func(error)) *Member {
				return NewPublicFunctionMember(
					t,
					identifier,
					authAccountTypeBorrowFunctionType,
					authAccountTypeBorrowFunctionDocString,
				)
			},
		},
		"link": {
			Kind: common.DeclarationKindFunction,
			Resolve: func(identifier string, _ ast.Range, _ func(error)) *Member {
				return NewPublicFunctionMember(
					t,
					identifier,
					authAccountTypeLinkFunctionType,
					authAccountTypeLinkFunctionDocString,
				)
			},
		},
		"unlink": {
			Kind: common.DeclarationKindFunction,
			Resolve: func(identifier string, _ ast.Range, _ func(error)) *Member {
				return NewPublicFunctionMember(
					t,
					identifier,
					authAccountTypeUnlinkFunctionType,
					authAccountTypeUnlinkFunctionDocString,
				)
			},
		},
		"getCapability": {
			Kind: common.DeclarationKindFunction,
			Resolve: func(identifier string, _ ast.Range, _ func(error)) *Member {
				return NewPublicFunctionMember(
					t,
					identifier,
					accountTypeGetCapabilityFunctionType,
					authAccountTypeGetCapabilityFunctionDocString,
				)
			},
		},
		"getLinkTarget": {
			Kind: common.DeclarationKindFunction,
			Resolve: func(identifier string, _ ast.Range, _ func(error)) *Member {
				return NewPublicFunctionMember(
					t,
					identifier,
					accountTypeGetLinkTargetFunctionType,
					accountTypeGetLinkTargetFunctionDocString,
				)
			},
		},
	})
}

func (*AuthAccountType) Unify(_ Type, _ map[*TypeParameter]Type, _ func(err error), _ ast.Range) bool {
	return false
}

func (t *AuthAccountType) Resolve(_ map[*TypeParameter]Type) Type {
	return t
}

// PublicAccountType represents the publicly accessible portion of an account.

type PublicAccountType struct{}

func (*PublicAccountType) IsType() {}

func (*PublicAccountType) String() string {
	return "PublicAccount"
}

func (*PublicAccountType) QualifiedString() string {
	return "PublicAccount"
}

func (*PublicAccountType) ID() TypeID {
	return "PublicAccount"
}

func (*PublicAccountType) Equal(other Type) bool {
	_, ok := other.(*PublicAccountType)
	return ok
}

func (*PublicAccountType) IsResourceType() bool {
	return false
}

func (*PublicAccountType) IsInvalidType() bool {
	return false
}

func (*PublicAccountType) IsStorable(_ map[*Member]bool) bool {
	return false
}

func (*PublicAccountType) IsEquatable() bool {
	return false
}

func (*PublicAccountType) TypeAnnotationState() TypeAnnotationState {
	return TypeAnnotationStateValid
}

func (t *PublicAccountType) RewriteWithRestrictedTypes() (result Type, rewritten bool) {
	return t, false
}

const publicAccountTypeGetLinkTargetFunctionDocString = `
Returns the capability at the given public path, or nil if it does not exist
`

func (t *PublicAccountType) GetMembers() map[string]MemberResolver {
	return withBuiltinMembers(t, map[string]MemberResolver{
		"address": {
			Kind: common.DeclarationKindField,
			Resolve: func(identifier string, _ ast.Range, _ func(error)) *Member {
				return NewPublicConstantFieldMember(
					t,
					identifier,
					&AddressType{},
					accountTypeAddressFieldDocString,
				)
			},
		},
		"getCapability": {
			Kind: common.DeclarationKindFunction,
			Resolve: func(identifier string, _ ast.Range, _ func(error)) *Member {
				return NewPublicFunctionMember(
					t,
					identifier,
					accountTypeGetCapabilityFunctionType,
					publicAccountTypeGetLinkTargetFunctionDocString,
				)
			},
		},
		"getLinkTarget": {
			Kind: common.DeclarationKindFunction,
			Resolve: func(identifier string, _ ast.Range, _ func(error)) *Member {
				return NewPublicFunctionMember(
					t,
					identifier,
					accountTypeGetLinkTargetFunctionType,
					accountTypeGetLinkTargetFunctionDocString,
				)
			},
		},
	})
}

func (*PublicAccountType) Unify(_ Type, _ map[*TypeParameter]Type, _ func(err error), _ ast.Range) bool {
	return false
}

func (t *PublicAccountType) Resolve(_ map[*TypeParameter]Type) Type {
	return t
}

// Member

type Member struct {
	ContainerType   Type
	Access          ast.Access
	Identifier      ast.Identifier
	TypeAnnotation  *TypeAnnotation
	DeclarationKind common.DeclarationKind
	VariableKind    ast.VariableKind
	ArgumentLabels  []string
	// Predeclared fields can be considered initialized
	Predeclared bool
	// IgnoreInSerialization fields are ignored in serialization
	IgnoreInSerialization bool
	DocString             string
}

func NewPublicFunctionMember(
	containerType Type,
	identifier string,
	invokableType InvokableType,
	docString string,
) *Member {

	return &Member{
		ContainerType:   containerType,
		Access:          ast.AccessPublic,
		Identifier:      ast.Identifier{Identifier: identifier},
		DeclarationKind: common.DeclarationKindFunction,
		VariableKind:    ast.VariableKindConstant,
		TypeAnnotation:  &TypeAnnotation{Type: invokableType},
		ArgumentLabels:  invokableType.ArgumentLabels(),
		DocString:       docString,
	}
}

func NewPublicConstantFieldMember(
	containerType Type,
	identifier string,
	fieldType Type,
	docString string,
) *Member {
	return &Member{
		ContainerType:   containerType,
		Access:          ast.AccessPublic,
		Identifier:      ast.Identifier{Identifier: identifier},
		DeclarationKind: common.DeclarationKindField,
		VariableKind:    ast.VariableKindConstant,
		TypeAnnotation:  NewTypeAnnotation(fieldType),
		DocString:       docString,
	}
}

// IsStorable returns whether a member is a storable field
func (m *Member) IsStorable(results map[*Member]bool) (result bool) {

	// Prevent a potential stack overflow due to cyclic declarations
	// by keeping track of the result for each member

	// If a result for the member is available, return it,
	// instead of checking the type

	var ok bool
	if result, ok = results[m]; ok {
		return result
	}

	// Temporarily assume the member is storable while it's type
	// is checked for storability. If a recursive call occurs,
	// the check for an existing result will prevent infinite recursion

	results[m] = true

	result = func() bool {
		// Skip checking predeclared members

		if m.Predeclared {
			return true
		}

		if m.DeclarationKind == common.DeclarationKindField {

			// Fields are not storable
			// if their type is non-storable

			fieldType := m.TypeAnnotation.Type

			if !fieldType.IsInvalidType() &&
				!fieldType.IsStorable(results) {

				return false
			}
		}

		return true
	}()

	results[m] = result
	return result
}

// InterfaceType

type InterfaceType struct {
	Location      ast.Location
	Identifier    string
	CompositeKind common.CompositeKind
	Members       map[string]*Member
	Fields        []string
	// TODO: add support for overloaded initializers
	InitializerParameters []*Parameter
	ContainerType         Type
	nestedTypes           map[string]Type
}

func (*InterfaceType) IsType() {}

func (t *InterfaceType) String() string {
	return t.Identifier
}

func (t *InterfaceType) QualifiedString() string {
	return t.QualifiedIdentifier()
}

func (t *InterfaceType) GetContainerType() Type {
	return t.ContainerType
}

func (t *InterfaceType) GetCompositeKind() common.CompositeKind {
	return t.CompositeKind
}

func (t *InterfaceType) GetLocation() ast.Location {
	return t.Location
}

func (t *InterfaceType) QualifiedIdentifier() string {
	return qualifiedIdentifier(t.Identifier, t.ContainerType)
}

func (t *InterfaceType) ID() TypeID {
	return TypeID(fmt.Sprintf("%s.%s", t.Location.ID(), t.QualifiedIdentifier()))
}

func (t *InterfaceType) Equal(other Type) bool {
	otherInterface, ok := other.(*InterfaceType)
	if !ok {
		return false
	}

	return otherInterface.CompositeKind == t.CompositeKind &&
		otherInterface.ID() == t.ID()
}

func (t *InterfaceType) GetMembers() map[string]MemberResolver {
	// TODO: optimize
	members := make(map[string]MemberResolver, len(t.Members))
	for name, loopMember := range t.Members {
		// NOTE: don't capture loop variable
		member := loopMember
		members[name] = MemberResolver{
			Kind: member.DeclarationKind,
			Resolve: func(_ string, _ ast.Range, _ func(error)) *Member {
				return member
			},
		}
	}
	return withBuiltinMembers(t, members)
}

func (t *InterfaceType) IsResourceType() bool {
	return t.CompositeKind == common.CompositeKindResource
}

func (t *InterfaceType) IsInvalidType() bool {
	return false
}

func (t *InterfaceType) IsStorable(results map[*Member]bool) bool {

	// If this interface type has a member which is non-storable,
	// then the interface type is not storable.

	for _, member := range t.Members {
		if !member.IsStorable(results) {
			return false
		}
	}

	return true
}

func (*InterfaceType) IsEquatable() bool {
	// TODO:
	return false
}

func (*InterfaceType) TypeAnnotationState() TypeAnnotationState {
	return TypeAnnotationStateValid
}

func (t *InterfaceType) RewriteWithRestrictedTypes() (Type, bool) {
	switch t.CompositeKind {
	case common.CompositeKindResource:
		return &RestrictedType{
			Type:         &AnyResourceType{},
			Restrictions: []*InterfaceType{t},
		}, true

	case common.CompositeKindStructure:
		return &RestrictedType{
			Type:         &AnyStructType{},
			Restrictions: []*InterfaceType{t},
		}, true

	default:
		return t, false
	}
}

func (*InterfaceType) Unify(_ Type, _ map[*TypeParameter]Type, _ func(err error), _ ast.Range) bool {
	// TODO:
	return false
}

func (t *InterfaceType) Resolve(_ map[*TypeParameter]Type) Type {
	return t
}

func (t *InterfaceType) NestedTypes() map[string]Type {
	return t.nestedTypes
}

// DictionaryType consists of the key and value type
// for all key-value pairs in the dictionary:
// All keys have to be a subtype of the key type,
// and all values have to be a subtype of the value type.

type DictionaryType struct {
	KeyType   Type
	ValueType Type
}

func (*DictionaryType) IsType() {}

func (t *DictionaryType) String() string {
	return fmt.Sprintf(
		"{%s: %s}",
		t.KeyType,
		t.ValueType,
	)
}

func (t *DictionaryType) QualifiedString() string {
	return fmt.Sprintf(
		"{%s: %s}",
		t.KeyType.QualifiedString(),
		t.ValueType.QualifiedString(),
	)
}

func (t *DictionaryType) ID() TypeID {
	return TypeID(fmt.Sprintf(
		"{%s:%s}",
		t.KeyType.ID(),
		t.ValueType.ID(),
	))
}

func (t *DictionaryType) Equal(other Type) bool {
	otherDictionary, ok := other.(*DictionaryType)
	if !ok {
		return false
	}

	return otherDictionary.KeyType.Equal(t.KeyType) &&
		otherDictionary.ValueType.Equal(t.ValueType)
}

func (t *DictionaryType) IsResourceType() bool {
	return t.KeyType.IsResourceType() ||
		t.ValueType.IsResourceType()
}

func (t *DictionaryType) IsInvalidType() bool {
	return t.KeyType.IsInvalidType() ||
		t.ValueType.IsInvalidType()
}

func (t *DictionaryType) IsStorable(results map[*Member]bool) bool {
	return t.KeyType.IsStorable(results) &&
		t.ValueType.IsStorable(results)
}

func (*DictionaryType) IsEquatable() bool {
	// TODO:
	return false
}

func (t *DictionaryType) TypeAnnotationState() TypeAnnotationState {
	keyTypeAnnotationState := t.KeyType.TypeAnnotationState()
	if keyTypeAnnotationState != TypeAnnotationStateValid {
		return keyTypeAnnotationState
	}

	valueTypeAnnotationState := t.ValueType.TypeAnnotationState()
	if valueTypeAnnotationState != TypeAnnotationStateValid {
		return valueTypeAnnotationState
	}

	return TypeAnnotationStateValid
}

func (t *DictionaryType) RewriteWithRestrictedTypes() (Type, bool) {
	rewrittenKeyType, keyTypeRewritten := t.KeyType.RewriteWithRestrictedTypes()
	rewrittenValueType, valueTypeRewritten := t.ValueType.RewriteWithRestrictedTypes()
	rewritten := keyTypeRewritten || valueTypeRewritten
	if rewritten {
		return &DictionaryType{
			KeyType:   rewrittenKeyType,
			ValueType: rewrittenValueType,
		}, true
	} else {
		return t, false
	}
}

const dictionaryTypeLengthFieldDocString = `
The number of entries in the dictionary
`

const dictionaryTypeKeysFieldDocString = `
An array containing all keys of the dictionary
`

const dictionaryTypeValuesFieldDocString = `
An array containing all values of the dictionary
`

const dictionaryTypeInsertFunctionDocString = `
Inserts the given value into the dictionary under the given key.

Returns the previous value as an optional if the dictionary contained the key, or nil if the dictionary did not contain the key
`

const dictionaryTypeRemoveFunctionDocString = `
Removes the value for the given key from the dictionary.

Returns the value as an optional if the dictionary contained the key, or nil if the dictionary did not contain the key
`

func (t *DictionaryType) GetMembers() map[string]MemberResolver {
	return withBuiltinMembers(t, map[string]MemberResolver{
		"length": {
			Kind: common.DeclarationKindField,
			Resolve: func(identifier string, _ ast.Range, _ func(error)) *Member {
				return NewPublicConstantFieldMember(
					t,
					identifier,
					&IntType{},
					dictionaryTypeLengthFieldDocString,
				)
			},
		},
		"keys": {
			Kind: common.DeclarationKindField,
			Resolve: func(identifier string, targetRange ast.Range, report func(error)) *Member {
				// TODO: maybe allow for resource key type

				if t.KeyType.IsResourceType() {
					report(
						&InvalidResourceDictionaryMemberError{
							Name:            identifier,
							DeclarationKind: common.DeclarationKindField,
							Range:           targetRange,
						},
					)
				}

				return NewPublicConstantFieldMember(
					t,
					identifier,
					&VariableSizedType{Type: t.KeyType},
					dictionaryTypeKeysFieldDocString,
				)
			},
		},
		"values": {
			Kind: common.DeclarationKindField,
			Resolve: func(identifier string, targetRange ast.Range, report func(error)) *Member {
				// TODO: maybe allow for resource value type

				if t.ValueType.IsResourceType() {
					report(
						&InvalidResourceDictionaryMemberError{
							Name:            identifier,
							DeclarationKind: common.DeclarationKindField,
							Range:           targetRange,
						},
					)
				}

				return NewPublicConstantFieldMember(
					t,
					identifier,
					&VariableSizedType{Type: t.ValueType},
					dictionaryTypeValuesFieldDocString,
				)
			},
		},
		"insert": {
			Kind: common.DeclarationKindFunction,
			Resolve: func(identifier string, _ ast.Range, _ func(error)) *Member {
				return NewPublicFunctionMember(t,
					identifier,
					&FunctionType{
						Parameters: []*Parameter{
							{
								Identifier:     "key",
								TypeAnnotation: NewTypeAnnotation(t.KeyType),
							},
							{
								Label:          ArgumentLabelNotRequired,
								Identifier:     "value",
								TypeAnnotation: NewTypeAnnotation(t.ValueType),
							},
						},
						ReturnTypeAnnotation: NewTypeAnnotation(
							&OptionalType{
								Type: t.ValueType,
							},
						),
					},
					dictionaryTypeInsertFunctionDocString,
				)
			},
		},
		"remove": {
			Kind: common.DeclarationKindFunction,
			Resolve: func(identifier string, _ ast.Range, _ func(error)) *Member {
				return NewPublicFunctionMember(t,
					identifier,
					&FunctionType{
						Parameters: []*Parameter{
							{
								Identifier:     "key",
								TypeAnnotation: NewTypeAnnotation(t.KeyType),
							},
						},
						ReturnTypeAnnotation: NewTypeAnnotation(
							&OptionalType{
								Type: t.ValueType,
							},
						),
					},
					dictionaryTypeRemoveFunctionDocString,
				)
			},
		},
	})
}

func (*DictionaryType) isValueIndexableType() bool {
	return true
}

func (t *DictionaryType) ElementType(_ bool) Type {
	return &OptionalType{Type: t.ValueType}
}

func (*DictionaryType) AllowsValueIndexingAssignment() bool {
	return true
}

func (t *DictionaryType) IndexingType() Type {
	return t.KeyType
}

type DictionaryEntryType struct {
	KeyType   Type
	ValueType Type
}

func (t *DictionaryType) Unify(
	other Type,
	typeParameters map[*TypeParameter]Type,
	report func(err error),
	outerRange ast.Range,
) bool {
	otherDictionary, ok := other.(*DictionaryType)
	if !ok {
		return false
	}

	keyUnified := t.KeyType.Unify(otherDictionary.KeyType, typeParameters, report, outerRange)
	valueUnified := t.ValueType.Unify(otherDictionary.ValueType, typeParameters, report, outerRange)
	return keyUnified || valueUnified
}

func (t *DictionaryType) Resolve(typeParameters map[*TypeParameter]Type) Type {
	newKeyType := t.KeyType.Resolve(typeParameters)
	if newKeyType == nil {
		return nil
	}

	newValueType := t.ValueType.Resolve(typeParameters)
	if newValueType == nil {
		return nil
	}

	return &DictionaryType{
		KeyType:   newKeyType,
		ValueType: newValueType,
	}
}

// ReferenceType represents the reference to a value
type ReferenceType struct {
	Authorized bool
	Type       Type
}

func (*ReferenceType) IsType() {}

func (t *ReferenceType) string(typeFormatter func(Type) string) string {
	if t.Type == nil {
		return "reference"
	}
	var builder strings.Builder
	if t.Authorized {
		builder.WriteString("auth ")
	}
	builder.WriteRune('&')
	builder.WriteString(typeFormatter(t.Type))
	return builder.String()
}

func (t *ReferenceType) String() string {
	return t.string(func(ty Type) string {
		return ty.String()
	})
}

func (t *ReferenceType) QualifiedString() string {
	return t.string(func(ty Type) string {
		return ty.QualifiedString()
	})
}

func (t *ReferenceType) ID() TypeID {
	return TypeID(
		t.string(func(ty Type) string {
			return string(ty.ID())
		}),
	)
}

func (t *ReferenceType) Equal(other Type) bool {
	otherReference, ok := other.(*ReferenceType)
	if !ok {
		return false
	}

	if t.Authorized != otherReference.Authorized {
		return false
	}

	return t.Type.Equal(otherReference.Type)
}

func (t *ReferenceType) IsResourceType() bool {
	return false
}

func (t *ReferenceType) IsInvalidType() bool {
	return t.Type.IsInvalidType()
}

func (t *ReferenceType) IsStorable(_ map[*Member]bool) bool {
	return false
}

func (*ReferenceType) IsEquatable() bool {
	return true
}

func (*ReferenceType) TypeAnnotationState() TypeAnnotationState {
	return TypeAnnotationStateValid
}

func (t *ReferenceType) RewriteWithRestrictedTypes() (Type, bool) {
	rewrittenType, rewritten := t.Type.RewriteWithRestrictedTypes()
	if rewritten {
		return &ReferenceType{
			Authorized: t.Authorized,
			Type:       rewrittenType,
		}, true
	} else {
		return t, false
	}
}

func (t *ReferenceType) GetMembers() map[string]MemberResolver {
	return t.Type.GetMembers()
}

func (t *ReferenceType) isValueIndexableType() bool {
	referencedType, ok := t.Type.(ValueIndexableType)
	if !ok {
		return false
	}
	return referencedType.isValueIndexableType()
}

func (t *ReferenceType) AllowsValueIndexingAssignment() bool {
	referencedType, ok := t.Type.(ValueIndexableType)
	if !ok {
		return false
	}
	return referencedType.AllowsValueIndexingAssignment()
}

func (t *ReferenceType) ElementType(isAssignment bool) Type {
	referencedType, ok := t.Type.(ValueIndexableType)
	if !ok {
		return nil
	}
	return referencedType.ElementType(isAssignment)
}

func (t *ReferenceType) IndexingType() Type {
	referencedType, ok := t.Type.(ValueIndexableType)
	if !ok {
		return nil
	}
	return referencedType.IndexingType()
}

func (*ReferenceType) Unify(_ Type, _ map[*TypeParameter]Type, _ func(err error), _ ast.Range) bool {
	// TODO:
	return false
}

func (t *ReferenceType) Resolve(_ map[*TypeParameter]Type) Type {
	// TODO:
	return t
}

// AddressType represents the address type
type AddressType struct{}

func (*AddressType) IsType() {}

func (*AddressType) String() string {
	return "Address"
}

func (*AddressType) QualifiedString() string {
	return "Address"
}

func (*AddressType) ID() TypeID {
	return "Address"
}

func (*AddressType) Equal(other Type) bool {
	_, ok := other.(*AddressType)
	return ok
}

func (*AddressType) IsResourceType() bool {
	return false
}

func (*AddressType) IsInvalidType() bool {
	return false
}

func (*AddressType) IsStorable(_ map[*Member]bool) bool {
	return true
}

func (*AddressType) IsEquatable() bool {
	return true
}

func (*AddressType) TypeAnnotationState() TypeAnnotationState {
	return TypeAnnotationStateValid
}

func (t *AddressType) RewriteWithRestrictedTypes() (Type, bool) {
	return t, false
}

var AddressTypeMinIntBig = new(big.Int)
var AddressTypeMaxIntBig = new(big.Int).SetUint64(math.MaxUint64)

func (*AddressType) MinInt() *big.Int {
	return AddressTypeMinIntBig
}

func (*AddressType) MaxInt() *big.Int {
	return AddressTypeMaxIntBig
}

func (*AddressType) Unify(_ Type, _ map[*TypeParameter]Type, _ func(err error), _ ast.Range) bool {
	return false
}

func (t *AddressType) Resolve(_ map[*TypeParameter]Type) Type {
	return t
}

const AddressTypeToBytesFunctionName = `toBytes`

var arrayTypeToBytesFunctionType = &FunctionType{
	ReturnTypeAnnotation: NewTypeAnnotation(
		&VariableSizedType{
			Type: &UInt8Type{},
		},
	),
}

const arrayTypeToBytesFunctionDocString = `
Returns an array containing the byte representation of the address
`

func (t *AddressType) GetMembers() map[string]MemberResolver {
	return withBuiltinMembers(t, map[string]MemberResolver{
		AddressTypeToBytesFunctionName: {
			Resolve: func(identifier string, _ ast.Range, _ func(error)) *Member {
				return NewPublicFunctionMember(
					t,
					identifier,
					arrayTypeToBytesFunctionType,
					arrayTypeToBytesFunctionDocString,
				)
			},
		},
	})
}

// IsSubType determines if the given subtype is a subtype
// of the given supertype.
//
// Types are subtypes of themselves.
//
func IsSubType(subType Type, superType Type) bool {

	if subType.Equal(superType) {
		return true
	}

	if _, ok := subType.(*NeverType); ok {
		return true
	}

	switch typedSuperType := superType.(type) {
	case *AnyType:
		return true

	case *AnyStructType:
		if subType.IsResourceType() {
			return false
		}
		if _, ok := subType.(*AnyType); ok {
			return false
		}
		return true

	case *AnyResourceType:
		return subType.IsResourceType()

	case *NumberType:
		switch subType.(type) {
		case *NumberType, *SignedNumberType:
			return true
		}

		return IsSubType(subType, &IntegerType{}) ||
			IsSubType(subType, &FixedPointType{})

	case *SignedNumberType:
		if _, ok := subType.(*SignedNumberType); ok {
			return true
		}

		return IsSubType(subType, &SignedIntegerType{}) ||
			IsSubType(subType, &SignedFixedPointType{})

	case *IntegerType:
		switch subType.(type) {
		case *IntegerType, *SignedIntegerType,
			*IntType, *UIntType,
			*Int8Type, *Int16Type, *Int32Type, *Int64Type, *Int128Type, *Int256Type,
			*UInt8Type, *UInt16Type, *UInt32Type, *UInt64Type, *UInt128Type, *UInt256Type,
			*Word8Type, *Word16Type, *Word32Type, *Word64Type:

			return true

		default:
			return false
		}

	case *SignedIntegerType:
		switch subType.(type) {
		case *SignedIntegerType,
			*IntType,
			*Int8Type, *Int16Type, *Int32Type, *Int64Type, *Int128Type, *Int256Type:

			return true

		default:
			return false
		}

	case *FixedPointType:
		switch subType.(type) {
		case *FixedPointType, *SignedFixedPointType,
			*Fix64Type, *UFix64Type:

			return true

		default:
			return false
		}

	case *SignedFixedPointType:
		switch subType.(type) {
		case *SignedNumberType, *Fix64Type:

			return true

		default:
			return false
		}

	case *OptionalType:
		optionalSubType, ok := subType.(*OptionalType)
		if !ok {
			// T <: U? if T <: U
			return IsSubType(subType, typedSuperType.Type)
		}
		// Optionals are covariant: T? <: U? if T <: U
		return IsSubType(optionalSubType.Type, typedSuperType.Type)

	case *DictionaryType:
		typedSubType, ok := subType.(*DictionaryType)
		if !ok {
			return false
		}

		return IsSubType(typedSubType.KeyType, typedSuperType.KeyType) &&
			IsSubType(typedSubType.ValueType, typedSuperType.ValueType)

	case *VariableSizedType:
		typedSubType, ok := subType.(*VariableSizedType)
		if !ok {
			return false
		}

		return IsSubType(
			typedSubType.ElementType(false),
			typedSuperType.ElementType(false),
		)

	case *ConstantSizedType:
		typedSubType, ok := subType.(*ConstantSizedType)
		if !ok {
			return false
		}

		if typedSubType.Size != typedSuperType.Size {
			return false
		}

		return IsSubType(
			typedSubType.ElementType(false),
			typedSuperType.ElementType(false),
		)

	case *ReferenceType:
		// References types are only subtypes of reference types

		typedSubType, ok := subType.(*ReferenceType)
		if !ok {
			return false
		}

		// An authorized reference type `auth &T`
		// is a subtype of a reference type `&U` (authorized or non-authorized),
		// if `T` is a subtype of `U`

		if typedSubType.Authorized {
			return IsSubType(typedSubType.Type, typedSuperType.Type)
		}

		// An unauthorized reference type is not a subtype of an authorized reference type.
		// Not even dynamically.
		//
		// The holder of the reference may not gain more permissions.

		if typedSuperType.Authorized {
			return false
		}

		switch typedInnerSuperType := typedSuperType.Type.(type) {
		case *RestrictedType:

			switch restrictedSuperType := typedInnerSuperType.Type.(type) {
			case *AnyResourceType, *AnyStructType, *AnyType:

				switch typedInnerSubType := typedSubType.Type.(type) {
				case *RestrictedType:
					// An unauthorized reference to a restricted type `&T{Us}`
					// is a subtype of a reference to a restricted type
					// `&AnyResource{Vs}` / `&AnyStruct{Vs}` / `&Any{Vs}`:
					// if the `T` is a subset of the supertype's restricted type,
					// and `Vs` is a subset of `Us`.
					//
					// The holder of the reference may only further restrict the reference.
					//
					// The requirement for `T` to conform to `Vs` is implied by the subset requirement.

					return IsSubType(typedInnerSubType.Type, restrictedSuperType) &&
						typedInnerSuperType.RestrictionSet().
							IsSubsetOf(typedInnerSubType.RestrictionSet())

				case *CompositeType:
					// An unauthorized reference to an unrestricted type `&T`
					// is a subtype of a reference to a restricted type
					// `&AnyResource{Us}` / `&AnyStruct{Us}` / `&Any{Us}`:
					// When `T != AnyResource && T != AnyStruct && T != Any`:
					// if `T` conforms to `Us`.
					//
					// The holder of the reference may only restrict the reference.

					// TODO: once interfaces can conform to interfaces, include
					return IsSubType(typedInnerSubType, restrictedSuperType) &&
						typedInnerSuperType.RestrictionSet().
							IsSubsetOf(typedInnerSubType.ExplicitInterfaceConformanceSet())

				case *AnyResourceType, *AnyStructType, *AnyType:
					// An unauthorized reference to an unrestricted type `&T`
					// is a subtype of a reference to a restricted type
					// `&AnyResource{Us}` / `&AnyStruct{Us}` / `&Any{Us}`:
					// When `T == AnyResource || T == AnyStruct || T == Any`: never.
					//
					// The holder of the reference may not gain more permissions or knowledge.

					return false
				}

			default:

				switch typedInnerSubType := typedSubType.Type.(type) {
				case *RestrictedType:

					// An unauthorized reference to a restricted type `&T{Us}`
					// is a subtype of a reference to a restricted type `&V{Ws}:`

					switch typedInnerSubType.Type.(type) {
					case *CompositeType:
						// When `T != AnyResource && T != AnyStruct && T != Any`:
						// if `T == V` and `Ws` is a subset of `Us`.
						//
						// The holder of the reference may not gain more permissions or knowledge
						// and may only further restrict the reference to the composite.

						return typedInnerSubType.Type == typedInnerSuperType.Type &&
							typedInnerSuperType.RestrictionSet().
								IsSubsetOf(typedInnerSubType.RestrictionSet())

					case *AnyResourceType, *AnyStructType, *AnyType:
						// When `T == AnyResource || T == AnyStruct || T == Any`: never.

						return false
					}

				case *CompositeType:
					// An unauthorized reference to an unrestricted type `&T`
					// is a subtype of a reference to a restricted type `&U{Vs}`:
					// When `T != AnyResource && T != AnyStruct && T != Any`: if `T == U`.
					//
					// The holder of the reference may only further restrict the reference.

					return typedInnerSubType == typedInnerSuperType.Type

				case *AnyResourceType, *AnyStructType, *AnyType:
					// An unauthorized reference to an unrestricted type `&T`
					// is a subtype of a reference to a restricted type `&U{Vs}`:
					// When `T == AnyResource || T == AnyStruct || T == Any`: never.
					//
					// The holder of the reference may not gain more permissions or knowledge.

					return false
				}
			}

		case *CompositeType:
			// An unauthorized reference is not a subtype of a reference to a composite type `&V`
			// (e.g. reference to a restricted type `&T{Us}`, or reference to an interface type `&T`)
			//
			// The holder of the reference may not gain more permissions or knowledge.

			return false

		case *AnyType:

			// An unauthorized reference to a restricted type `&T{Us}`
			// or to a unrestricted type `&T`
			// is a subtype of the type `&Any`: always.

			return true

		case *AnyResourceType:

			// An unauthorized reference to a restricted type `&T{Us}`
			// or to a unrestricted type `&T`
			// is a subtype of the type `&AnyResource`:
			// if `T == AnyResource` or `T` is a resource-kinded composite.

			switch typedInnerSubType := typedSubType.Type.(type) {
			case *RestrictedType:
				switch typedInnerInnerSubType := typedInnerSubType.Type.(type) {
				case *AnyResourceType:
					return true

				case *CompositeType:
					return typedInnerInnerSubType.Kind == common.CompositeKindResource

				default:
					return false
				}

			case *CompositeType:
				return typedInnerSubType.Kind == common.CompositeKindResource
			}

		case *AnyStructType:
			// `&T <: &AnyStruct` iff `T <: AnyStruct`
			return IsSubType(typedSubType.Type, typedSuperType.Type)
		}

	case *FunctionType:
		typedSubType, ok := subType.(*FunctionType)
		if !ok {
			return false
		}

		if len(typedSubType.Parameters) != len(typedSuperType.Parameters) {
			return false
		}

		// Functions are contravariant in their parameter types

		for i, subParameter := range typedSubType.Parameters {
			superParameter := typedSuperType.Parameters[i]
			if !IsSubType(
				superParameter.TypeAnnotation.Type,
				subParameter.TypeAnnotation.Type,
			) {
				return false
			}
		}

		// Functions are covariant in their return type

		if typedSubType.ReturnTypeAnnotation != nil &&
			typedSuperType.ReturnTypeAnnotation != nil {

			return IsSubType(
				typedSubType.ReturnTypeAnnotation.Type,
				typedSuperType.ReturnTypeAnnotation.Type,
			)
		}

		if typedSubType.ReturnTypeAnnotation == nil &&
			typedSuperType.ReturnTypeAnnotation == nil {

			return true
		}

	case *RestrictedType:

		switch restrictedSuperType := typedSuperType.Type.(type) {
		case *AnyResourceType, *AnyStructType, *AnyType:

			switch typedSubType := subType.(type) {
			case *RestrictedType:

				// A restricted type `T{Us}`
				// is a subtype of a restricted type `AnyResource{Vs}` / `AnyStruct{Vs}` / `Any{Vs}`:

				switch restrictedSubtype := typedSubType.Type.(type) {
				case *AnyResourceType, *AnyStructType, *AnyType:
					// When `T == AnyResource || T == AnyStruct || T == Any`:
					// if the restricted type of the subtype
					// is a subtype of the restricted supertype,
					// and `Vs` is a subset of `Us`.

					return IsSubType(restrictedSubtype, restrictedSuperType) &&
						typedSuperType.RestrictionSet().
							IsSubsetOf(typedSubType.RestrictionSet())

				case *CompositeType:
					// When `T != AnyResource && T != AnyStruct && T != Any`:
					// if the restricted type of the subtype
					// is a subtype of the restricted supertype,
					// and `T` conforms to `Vs`.
					// `Us` and `Vs` do *not* have to be subsets.

					// TODO: once interfaces can conform to interfaces, include
					return IsSubType(restrictedSubtype, restrictedSuperType) &&
						typedSuperType.RestrictionSet().
							IsSubsetOf(restrictedSubtype.ExplicitInterfaceConformanceSet())
				}

			case *AnyResourceType:
				// `AnyResource` is a subtype of a restricted type
				// - `AnyResource{Us}`: not statically;
				// - `AnyStruct{Us}`: never.
				// - `Any{Us}`: not statically;

				return false

			case *AnyStructType:
				// `AnyStruct` is a subtype of a restricted type
				// - `AnyStruct{Us}`: not statically.
				// - `AnyResource{Us}`: never;
				// - `Any{Us}`: not statically.

				return false

			case *AnyType:
				// `Any` is a subtype of a restricted type
				// - `Any{Us}: not statically.`
				// - `AnyStruct{Us}`: never;
				// - `AnyResource{Us}`: never;

				return false

			case *CompositeType:
				// An unrestricted type `T`
				// is a subtype of a restricted type `AnyResource{Us}` / `AnyStruct{Us}` / `Any{Us}`:
				// if `T` is a subtype of the restricted supertype,
				// and `T` conforms to `Us`.

				return IsSubType(typedSubType, typedSuperType.Type) &&
					typedSuperType.RestrictionSet().
						IsSubsetOf(typedSubType.ExplicitInterfaceConformanceSet())
			}

		default:

			switch typedSubType := subType.(type) {
			case *RestrictedType:

				// A restricted type `T{Us}`
				// is a subtype of a restricted type `V{Ws}`:

				switch restrictedSubType := typedSubType.Type.(type) {
				case *AnyResourceType, *AnyStructType, *AnyType:
					// When `T == AnyResource || T == AnyStruct || T == Any`:
					// not statically.
					return false

				case *CompositeType:
					// When `T != AnyResource && T != AnyStructType && T != Any`: if `T == V`.
					//
					// `Us` and `Ws` do *not* have to be subsets:
					// The owner may freely restrict and unrestrict.

					return restrictedSubType == typedSuperType.Type
				}

			case *CompositeType:
				// An unrestricted type `T`
				// is a subtype of a restricted type `U{Vs}`: if `T == U`.
				//
				// The owner may freely restrict.

				return typedSubType == typedSuperType.Type

			case *AnyResourceType, *AnyStructType, *AnyType:
				// An unrestricted type `T`
				// is a subtype of a restricted type `AnyResource{Vs}` / `AnyStruct{Vs}` / `Any{Vs}`:
				// not statically.

				return false
			}
		}

	case *CompositeType:

		// NOTE: type equality case (composite type `T` is subtype of composite type `U`)
		// is already handled at beginning of function

		switch typedSubType := subType.(type) {
		case *RestrictedType:

			// A restricted type `T{Us}`
			// is a subtype of an unrestricted type `V`:

			switch restrictedSubType := typedSubType.Type.(type) {
			case *AnyResourceType, *AnyStructType, *AnyType:
				// When `T == AnyResource || T == AnyStruct || T == Any`: not statically.
				return false

			case *CompositeType:
				// When `T != AnyResource && T != AnyStruct`: if `T == V`.
				//
				// The owner may freely unrestrict.

				return restrictedSubType == typedSuperType
			}

		case *CompositeType:
			// The supertype composite type might be a type requirement.
			// Check if the subtype composite type implicitly conforms to it.

			for _, conformance := range typedSubType.ImplicitTypeRequirementConformances {
				if conformance == typedSuperType {
					return true
				}
			}
		}

	case *InterfaceType:

		switch typedSubType := subType.(type) {
		case *CompositeType:

			// Resources are not subtypes of resource interfaces.
			// (Use `AnyResource` / `AnyStruct` / `Any` with restriction instead).

			if typedSuperType.CompositeKind == common.CompositeKindResource ||
				typedSuperType.CompositeKind == common.CompositeKindStructure {

				return false
			}

			// A composite type `T` is a subtype of a interface type `V`:
			// if `T` conforms to `V`, and `V` and `T` are of the same kind

			if typedSubType.Kind != typedSuperType.CompositeKind {
				return false
			}

			// TODO: once interfaces can conform to interfaces, include
			if _, ok := typedSubType.ExplicitInterfaceConformanceSet()[typedSuperType]; ok {
				return true
			}

			return false

		case *InterfaceType:
			// TODO: Once interfaces can conform to interfaces, check conformances here
			return false
		}

	case ParameterizedType:
		if superTypeBaseType := typedSuperType.BaseType(); superTypeBaseType != nil {

			// T<Us> <: V<Ws>
			// if T <: V  && |Us| == |Ws| && U_i <: W_i

			if typedSubType, ok := subType.(ParameterizedType); ok {
				if subTypeBaseType := typedSubType.BaseType(); subTypeBaseType != nil {

					if !IsSubType(subTypeBaseType, superTypeBaseType) {
						return false
					}

					subTypeTypeArguments := typedSubType.TypeArguments()
					superTypeTypeArguments := typedSuperType.TypeArguments()

					if len(subTypeTypeArguments) != len(superTypeTypeArguments) {
						return false
					}

					for i, superTypeTypeArgument := range superTypeTypeArguments {
						subTypeTypeArgument := subTypeTypeArguments[i]
						if !IsSubType(subTypeTypeArgument, superTypeTypeArgument) {
							return false
						}
					}

					return true
				}
			}
		}

	case *StorableType:
		storableResults := map[*Member]bool{}
		return subType.IsStorable(storableResults)
	}

	// TODO: enforce type arguments, remove this rule

	// T<Us> <: V
	// if T <: V

	if typedSubType, ok := subType.(ParameterizedType); ok {
		if baseType := typedSubType.BaseType(); baseType != nil {
			return IsSubType(baseType, superType)
		}
	}

	return false
}

// UnwrapOptionalType returns the type if it is not an optional type,
// or the inner-most type if it is (optional types are repeatedly unwrapped)
//
func UnwrapOptionalType(ty Type) Type {
	for {
		optionalType, ok := ty.(*OptionalType)
		if !ok {
			return ty
		}
		ty = optionalType.Type
	}
}

func AreCompatibleEquatableTypes(leftType, rightType Type) bool {
	unwrappedLeftType := UnwrapOptionalType(leftType)
	unwrappedRightType := UnwrapOptionalType(rightType)

	leftIsEquatable := unwrappedLeftType.IsEquatable()
	rightIsEquatable := unwrappedRightType.IsEquatable()

	if unwrappedLeftType.Equal(unwrappedRightType) &&
		leftIsEquatable && rightIsEquatable {

		return true
	}

	// The types are equatable if this is a comparison with `nil`,
	// which has type `Never?`

	if IsNilType(leftType) || IsNilType(rightType) {
		return true
	}

	return false
}

// IsNilType returns true if the given type is the type of `nil`, i.e. `Never?`.
//
func IsNilType(ty Type) bool {
	optionalType, ok := ty.(*OptionalType)
	if !ok {
		return false
	}

	if _, ok := optionalType.Type.(*NeverType); !ok {
		return false
	}

	return true
}

type TransactionType struct {
	Members           map[string]*Member
	Fields            []string
	PrepareParameters []*Parameter
	Parameters        []*Parameter
}

func (t *TransactionType) EntryPointFunctionType() *FunctionType {
	return &FunctionType{
		Parameters:           append(t.Parameters, t.PrepareParameters...),
		ReturnTypeAnnotation: NewTypeAnnotation(&VoidType{}),
	}
}

func (t *TransactionType) PrepareFunctionType() *SpecialFunctionType {
	return &SpecialFunctionType{
		FunctionType: &FunctionType{
			Parameters:           t.PrepareParameters,
			ReturnTypeAnnotation: NewTypeAnnotation(&VoidType{}),
		},
	}
}

func (*TransactionType) ExecuteFunctionType() *SpecialFunctionType {
	return &SpecialFunctionType{
		FunctionType: &FunctionType{
			Parameters:           []*Parameter{},
			ReturnTypeAnnotation: NewTypeAnnotation(&VoidType{}),
		},
	}
}

func (*TransactionType) IsType() {}

func (*TransactionType) String() string {
	return "Transaction"
}

func (*TransactionType) QualifiedString() string {
	return "Transaction"
}

func (*TransactionType) ID() TypeID {
	return "Transaction"
}

func (*TransactionType) Equal(other Type) bool {
	_, ok := other.(*TransactionType)
	return ok
}

func (*TransactionType) IsResourceType() bool {
	return false
}

func (*TransactionType) IsInvalidType() bool {
	return false
}

func (*TransactionType) IsStorable(_ map[*Member]bool) bool {
	return false
}

func (*TransactionType) IsEquatable() bool {
	return false
}

func (*TransactionType) TypeAnnotationState() TypeAnnotationState {
	return TypeAnnotationStateValid
}

func (t *TransactionType) RewriteWithRestrictedTypes() (Type, bool) {
	return t, false
}

func (t *TransactionType) GetMembers() map[string]MemberResolver {
	// TODO: optimize
	members := make(map[string]MemberResolver, len(t.Members))
	for name, loopMember := range t.Members {
		// NOTE: don't capture loop variable
		member := loopMember
		members[name] = MemberResolver{
			Kind: member.DeclarationKind,
			Resolve: func(identifier string, _ ast.Range, _ func(error)) *Member {
				return member
			},
		}
	}
	return withBuiltinMembers(t, members)
}

func (*TransactionType) Unify(_ Type, _ map[*TypeParameter]Type, _ func(err error), _ ast.Range) bool {
	return false
}

func (t *TransactionType) Resolve(_ map[*TypeParameter]Type) Type {
	return t
}

// InterfaceSet

type InterfaceSet map[*InterfaceType]struct{}

func (s InterfaceSet) IsSubsetOf(other InterfaceSet) bool {
	for interfaceType := range s {
		if _, ok := other[interfaceType]; !ok {
			return false
		}
	}

	return true
}

func (s InterfaceSet) Includes(interfaceType *InterfaceType) bool {
	_, ok := s[interfaceType]
	return ok
}

func (s InterfaceSet) Add(interfaceType *InterfaceType) {
	s[interfaceType] = struct{}{}
}

// RestrictedType
//
// No restrictions implies the type is fully restricted,
// i.e. no members of the underlying resource type are available.
//
type RestrictedType struct {
	Type         Type
	Restrictions []*InterfaceType
	// an internal set of field `Restrictions`
	restrictionSet InterfaceSet
}

func (t *RestrictedType) RestrictionSet() InterfaceSet {
	if t.restrictionSet == nil {
		t.restrictionSet = make(InterfaceSet, len(t.Restrictions))
		for _, restriction := range t.Restrictions {
			t.restrictionSet[restriction] = struct{}{}
		}
	}
	return t.restrictionSet
}

func (*RestrictedType) IsType() {}

func (t *RestrictedType) string(separator string, typeFormatter func(Type) string) string {
	var result strings.Builder
	result.WriteString(typeFormatter(t.Type))
	result.WriteRune('{')
	for i, restriction := range t.Restrictions {
		if i > 0 {
			result.WriteRune(',')
			result.WriteString(separator)
		}
		result.WriteString(typeFormatter(restriction))
	}
	result.WriteRune('}')
	return result.String()
}

func (t *RestrictedType) String() string {
	return t.string(" ", func(ty Type) string {
		return ty.String()
	})
}

func (t *RestrictedType) QualifiedString() string {
	return t.string(" ", func(ty Type) string {
		return ty.QualifiedString()
	})
}

func (t *RestrictedType) ID() TypeID {
	return TypeID(
		t.string("", func(ty Type) string {
			return string(ty.ID())
		}),
	)
}

func (t *RestrictedType) Equal(other Type) bool {
	otherRestrictedType, ok := other.(*RestrictedType)
	if !ok {
		return false
	}

	if !otherRestrictedType.Type.Equal(t.Type) {
		return false
	}

	// Check that the set of restrictions are equal; order does not matter

	restrictionSet := t.RestrictionSet()
	otherRestrictionSet := otherRestrictedType.RestrictionSet()

	count := len(restrictionSet)
	if count != len(otherRestrictionSet) {
		return false
	}

	return restrictionSet.IsSubsetOf(otherRestrictionSet)
}

func (t *RestrictedType) IsResourceType() bool {
	if t.Type == nil {
		return false
	}
	return t.Type.IsResourceType()
}

func (t *RestrictedType) IsInvalidType() bool {
	if t.Type != nil && t.Type.IsInvalidType() {
		return true
	}

	for _, restriction := range t.Restrictions {
		if restriction.IsInvalidType() {
			return true
		}
	}

	return false
}

func (t *RestrictedType) IsStorable(results map[*Member]bool) bool {
	if t.Type != nil && !t.Type.IsStorable(results) {
		return false
	}

	for _, restriction := range t.Restrictions {
		if !restriction.IsStorable(results) {
			return false
		}
	}

	return true
}

func (*RestrictedType) IsEquatable() bool {
	// TODO:
	return false
}

func (*RestrictedType) TypeAnnotationState() TypeAnnotationState {
	return TypeAnnotationStateValid
}

func (t *RestrictedType) RewriteWithRestrictedTypes() (Type, bool) {
	// Even though the restrictions should be resource interfaces,
	// they are not on the "first level", i.e. not the restricted type
	return t, false
}

func (t *RestrictedType) GetMembers() map[string]MemberResolver {

	members := map[string]MemberResolver{}

	// Return the members of all restrictions.
	// The invariant that restrictions may not have overlapping members is not checked here,
	// but implicitly when the resource declaration's conformances are checked.

	for _, restriction := range t.Restrictions {
		for name, resolver := range restriction.GetMembers() {
			if _, ok := members[name]; !ok {
				members[name] = resolver
			}
		}
	}

	// Also include members of the restricted type for convenience,
	// to help check the rest of the program and improve the developer experience,
	// *but* also report an error that this access is invalid when the entry is resolved.
	//
	// The restricted type may be `AnyResource`, in which case there are no members.

	for name, loopResolver := range t.Type.GetMembers() {

		if _, ok := members[name]; ok {
			continue
		}

		// NOTE: don't capture loop variable
		resolver := loopResolver

		members[name] = MemberResolver{
			Kind: resolver.Kind,
			Resolve: func(identifier string, targetRange ast.Range, report func(error)) *Member {
				member := resolver.Resolve(identifier, targetRange, report)

				report(
					&InvalidRestrictedTypeMemberAccessError{
						Name:  identifier,
						Range: targetRange,
					},
				)

				return member
			},
		}
	}

	return members
}

func (*RestrictedType) Unify(_ Type, _ map[*TypeParameter]Type, _ func(err error), _ ast.Range) bool {
	// TODO: how do we unify the restriction sets?
	return false
}

func (t *RestrictedType) Resolve(_ map[*TypeParameter]Type) Type {
	// TODO:
	return t
}

// PathType

type PathType struct{}

func (*PathType) IsType() {}

func (*PathType) String() string {
	return "Path"
}

func (*PathType) QualifiedString() string {
	return "Path"
}

func (*PathType) ID() TypeID {
	return "Path"
}

func (*PathType) Equal(other Type) bool {
	_, ok := other.(*PathType)
	return ok
}

func (*PathType) IsResourceType() bool {
	return false
}

func (*PathType) IsInvalidType() bool {
	return false
}

func (*PathType) IsStorable(_ map[*Member]bool) bool {
	return true
}

func (*PathType) IsEquatable() bool {
	// TODO:
	return false
}

func (*PathType) TypeAnnotationState() TypeAnnotationState {
	return TypeAnnotationStateValid
}

func (t *PathType) RewriteWithRestrictedTypes() (Type, bool) {
	return t, false
}

func (*PathType) Unify(_ Type, _ map[*TypeParameter]Type, _ func(err error), _ ast.Range) bool {
	return false
}

func (t *PathType) Resolve(_ map[*TypeParameter]Type) Type {
	return t
}

func (t *PathType) GetMembers() map[string]MemberResolver {
	return withBuiltinMembers(t, nil)
}

// CapabilityType

type CapabilityType struct {
	BorrowType Type
}

func (*CapabilityType) IsType() {}

func (t *CapabilityType) string(typeFormatter func(Type) string) string {
	var builder strings.Builder
	builder.WriteString("Capability")
	if t.BorrowType != nil {
		builder.WriteRune('<')
		builder.WriteString(typeFormatter(t.BorrowType))
		builder.WriteRune('>')
	}
	return builder.String()
}

func (t *CapabilityType) String() string {
	return t.string(func(t Type) string {
		return t.String()
	})
}

func (t *CapabilityType) QualifiedString() string {
	return t.string(func(t Type) string {
		return t.QualifiedString()
	})
}

func (t *CapabilityType) ID() TypeID {
	return TypeID(t.string(func(t Type) string {
		return string(t.ID())
	}))
}

func (t *CapabilityType) Equal(other Type) bool {
	otherCapability, ok := other.(*CapabilityType)
	if !ok {
		return false
	}
	if otherCapability.BorrowType == nil {
		return t.BorrowType == nil
	}
	return otherCapability.BorrowType.Equal(t.BorrowType)
}

func (*CapabilityType) IsResourceType() bool {
	return false
}

func (t *CapabilityType) IsInvalidType() bool {
	if t.BorrowType == nil {
		return false
	}
	return t.BorrowType.IsInvalidType()
}

func (t *CapabilityType) TypeAnnotationState() TypeAnnotationState {
	if t.BorrowType == nil {
		return TypeAnnotationStateValid
	}
	return t.BorrowType.TypeAnnotationState()
}

func (*CapabilityType) IsStorable(_ map[*Member]bool) bool {
	return true
}

func (*CapabilityType) IsEquatable() bool {
	// TODO:
	return false
}

func (t *CapabilityType) RewriteWithRestrictedTypes() (Type, bool) {
	if t.BorrowType == nil {
		return t, false
	}
	rewrittenType, rewritten := t.BorrowType.RewriteWithRestrictedTypes()
	if rewritten {
		return &CapabilityType{
			BorrowType: rewrittenType,
		}, true
	} else {
		return t, false
	}
}

func (t *CapabilityType) Unify(
	other Type,
	typeParameters map[*TypeParameter]Type,
	report func(err error),
	outerRange ast.Range,
) bool {
	otherCap, ok := other.(*CapabilityType)
	if !ok {
		return false
	}

	if t.BorrowType == nil {
		return false
	}

	return t.BorrowType.Unify(otherCap.BorrowType, typeParameters, report, outerRange)
}

func (t *CapabilityType) Resolve(typeParameters map[*TypeParameter]Type) Type {
	var resolvedBorrowType Type
	if t.BorrowType != nil {
		resolvedBorrowType = t.BorrowType.Resolve(typeParameters)
	}

	return &CapabilityType{
		BorrowType: resolvedBorrowType,
	}
}

var capabilityTypeParameter = &TypeParameter{
	Name: "T",
	TypeBound: &ReferenceType{
		Type: &AnyType{},
	},
}

func (t *CapabilityType) TypeParameters() []*TypeParameter {
	return []*TypeParameter{
		capabilityTypeParameter,
	}
}

func (t *CapabilityType) Instantiate(typeArguments []Type, _ func(err error)) Type {
	borrowType := typeArguments[0]
	return &CapabilityType{
		BorrowType: borrowType,
	}
}

func (t *CapabilityType) BaseType() Type {
	if t.BorrowType == nil {
		return nil
	}
	return &CapabilityType{}
}

func (t *CapabilityType) TypeArguments() []Type {
	borrowType := t.BorrowType
	if borrowType == nil {
		borrowType = &ReferenceType{
			Type: &AnyType{},
		}
	}
	return []Type{
		borrowType,
	}
}

func capabilityTypeBorrowFunctionType(borrowType Type) *FunctionType {

	var typeParameters []*TypeParameter

	if borrowType == nil {
		typeParameter := capabilityTypeParameter

		typeParameters = []*TypeParameter{
			typeParameter,
		}

		borrowType = &GenericType{
			TypeParameter: typeParameter,
		}
	}

	return &FunctionType{
		TypeParameters: typeParameters,
		ReturnTypeAnnotation: NewTypeAnnotation(
			&OptionalType{
				Type: borrowType,
			},
		),
	}
}

func capabilityTypeCheckFunctionType(borrowType Type) *FunctionType {

	var typeParameters []*TypeParameter

	if borrowType == nil {
		typeParameters = []*TypeParameter{
			capabilityTypeParameter,
		}
	}

	return &FunctionType{
		TypeParameters:       typeParameters,
		ReturnTypeAnnotation: NewTypeAnnotation(&BoolType{}),
	}
}

const capabilityTypeBorrowFunctionDocString = `
Returns a reference to the object targeted by the capability, provided it can be borrowed using the given type
`

const capabilityTypeCheckFunctionDocString = `
Returns true if the capability currently targets an object that satisfies the given type, i.e. could be borrowed using the given type
`

func (t *CapabilityType) GetMembers() map[string]MemberResolver {
	return withBuiltinMembers(t, map[string]MemberResolver{
		"borrow": {
			Kind: common.DeclarationKindFunction,
			Resolve: func(identifier string, _ ast.Range, _ func(error)) *Member {
				return NewPublicFunctionMember(
					t,
					identifier,
					capabilityTypeBorrowFunctionType(t.BorrowType),
					capabilityTypeBorrowFunctionDocString,
				)
			},
		},
		"check": {
			Kind: common.DeclarationKindFunction,
			Resolve: func(identifier string, _ ast.Range, _ func(error)) *Member {
				return NewPublicFunctionMember(
					t,
					identifier,
					capabilityTypeCheckFunctionType(t.BorrowType),
					capabilityTypeCheckFunctionDocString,
				)
			},
		},
	})
}

// StorableType is the supertype of all types which are storable.
//
// It is only used as e.g. a type bound, but is not accessible
// to user programs, i.e. can't be used in type annotations
// for e.g. parameters, return types, fields, etc.
//
type StorableType struct{}

func (*StorableType) IsType() {}

func (*StorableType) String() string {
	return "Storable"
}

func (*StorableType) QualifiedString() string {
	return "Storable"
}

func (*StorableType) ID() TypeID {
	return "Storable"
}

func (*StorableType) Equal(other Type) bool {
	_, ok := other.(*StorableType)
	return ok
}

func (*StorableType) IsResourceType() bool {

	// NOTE: Subtypes may be either resource types or not.
	//
	// Returning false here is safe, because this type is
	// only used as e.g. a type bound, but is not accessible
	// to user programs, i.e. can't be used in type annotations
	// for e.g. parameters, return types, fields, etc.

	return false
}

func (*StorableType) IsInvalidType() bool {
	return false
}

func (*StorableType) IsStorable(_ map[*Member]bool) bool {
	return true
}

func (*StorableType) IsEquatable() bool {
	return false
}

func (*StorableType) TypeAnnotationState() TypeAnnotationState {
	return TypeAnnotationStateValid
}

func (t *StorableType) RewriteWithRestrictedTypes() (Type, bool) {
	return t, false
}

func (*StorableType) Unify(_ Type, _ map[*TypeParameter]Type, _ func(err error), _ ast.Range) bool {
	return false
}

func (t *StorableType) Resolve(_ map[*TypeParameter]Type) Type {
	return t
}

func (t *StorableType) GetMembers() map[string]MemberResolver {
	return nil
}
