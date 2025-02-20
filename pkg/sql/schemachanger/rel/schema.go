// Copyright 2021 The Cockroach Authors.
//
// Use of this software is governed by the Business Source License
// included in the file licenses/BSL.txt.
//
// As of the Change Date specified in that file, in accordance with
// the Business Source License, use of this software will be governed
// by the Apache License, Version 2.0, included in the file
// licenses/APL.txt.

package rel

import (
	"reflect"
	"sort"
	"strings"
	"unsafe"

	"github.com/cockroachdb/errors"
)

// Schema defines a mapping of entities to their attributes and decomposition.
type Schema struct {
	name                     string
	attrs                    []Attr
	attrTypes                []reflect.Type
	attrToOrdinal            map[Attr]ordinal
	entityTypes              []*entityTypeSchema
	entityTypeSchemas        map[reflect.Type]*entityTypeSchema
	typeOrdinal, selfOrdinal ordinal
	stringAttrs              ordinalSet
	rules                    []*RuleDef
	rulesByName              map[string]*RuleDef
}

type entityTypeSchemaSort Schema

func (e entityTypeSchemaSort) Len() int { return len(e.entityTypeSchemas) }
func (e entityTypeSchemaSort) Less(i, j int) bool {
	less, _ := compareTypes(e.entityTypes[i].typ, e.entityTypes[j].typ)
	return less
}
func (e entityTypeSchemaSort) Swap(i, j int) {
	e.entityTypes[i].typID = uintptr(j)
	e.entityTypes[j].typID = uintptr(i)
	e.entityTypes[i], e.entityTypes[j] = e.entityTypes[j], e.entityTypes[i]
}

var _ sort.Interface = (*entityTypeSchemaSort)(nil)

// NewSchema constructs a new schema from mappings.
// The name parameter is just used for debugging and error messages.
func NewSchema(name string, m ...SchemaOption) (_ *Schema, err error) {
	defer func() {
		switch r := recover().(type) {
		case nil:
			return
		case error:
			err = errors.Wrap(r, "failed to construct schema")
		default:
			err = errors.AssertionFailedf("failed to construct schema: %v", r)
		}
	}()
	sc := buildSchema(name, m...)
	return sc, nil
}

// MustSchema is like NewSchema but any errors result in a panic.
func MustSchema(name string, m ...SchemaOption) *Schema {
	return buildSchema(name, m...)
}

type entityTypeSchema struct {
	typ        reflect.Type
	fields     []fieldInfo
	attrFields map[ordinal][]fieldInfo

	// typID is the rank of the type of this entity in the schema.
	typID uintptr
}

type fieldInfo struct {
	path            string
	typ             reflect.Type
	attr            ordinal
	comparableValue func(unsafe.Pointer) interface{}
	value           func(unsafe.Pointer) interface{}
	inline          func(unsafe.Pointer) (uintptr, bool)
	fieldFlags
}

type fieldFlags int8

func (f fieldFlags) isPtr() bool     { return f&pointerField != 0 }
func (f fieldFlags) isScalar() bool  { return f&(intField|stringField|uintField) != 0 }
func (f fieldFlags) isStruct() bool  { return f&structField != 0 }
func (f fieldFlags) isInt() bool     { return f&intField != 0 }
func (f fieldFlags) isUint() bool    { return f&uintField != 0 }
func (f fieldFlags) isIntLike() bool { return f&(intField|uintField) != 0 }
func (f fieldFlags) isString() bool  { return f&stringField != 0 }

const (
	intField fieldFlags = 1 << iota
	uintField
	stringField
	structField
	pointerField
)

func buildSchema(name string, opts ...SchemaOption) *Schema {
	var m schemaMappings
	for _, opt := range opts {
		opt.apply(&m)
	}
	sb := &schemaBuilder{
		Schema: &Schema{
			name:              name,
			attrToOrdinal:     make(map[Attr]ordinal),
			entityTypeSchemas: make(map[reflect.Type]*entityTypeSchema),
			rulesByName:       make(map[string]*RuleDef),
		},
		m: m,
	}

	for _, t := range m.attrTypes {
		sb.maybeAddAttribute(t.a, t.typ)
	}

	// We want to know what all the variable types are.
	for _, tm := range m.entityMappings {
		sb.maybeAddTypeMapping(tm.typ, tm.attrMappings)
	}

	sb.maybeAddAttribute(Self, emptyInterfaceType)
	sb.selfOrdinal = sb.mustGetOrdinal(Self)
	sb.maybeAddAttribute(Type, reflectTypeType)
	sb.typeOrdinal = sb.mustGetOrdinal(Type)
	sort.Sort((*entityTypeSchemaSort)(sb.Schema))

	return sb.Schema
}

type schemaBuilder struct {
	*Schema
	m schemaMappings
}

func (sb *schemaBuilder) maybeAddAttribute(a Attr, typ reflect.Type) ordinal {
	// TODO(ajwerner): Validate that t is an okay type for an attribute
	// to be.
	ord, exists := sb.attrToOrdinal[a]
	if !exists {
		ord = ordinal(len(sb.attrs))
		if ord >= maxUserAttribute {
			panic(errors.Errorf("too many attributes"))
		}
		sb.attrs = append(sb.attrs, a)
		sb.attrTypes = append(sb.attrTypes, typ)
		sb.attrToOrdinal[a] = ord
		return ord
	}
	prev := sb.attrTypes[ord]
	if err := checkType(typ, prev); err != nil {
		panic(errors.Wrapf(err, "type mismatch for %v", a))
	}
	return ord
}

// checkType determines whether, either, the typ matches exp or the typ
// implements exp which is an interface type.
func checkType(typ, exp reflect.Type) error {
	switch exp.Kind() {
	case reflect.Interface:
		if !typ.Implements(exp) {
			return errors.Errorf("%v does not implement %v", typ, exp)
		}
	default:
		if typ != exp {
			return errors.Errorf("%v is not %v", typ, exp)
		}
	}
	return nil
}

func (sb *schemaBuilder) maybeAddTypeMapping(t reflect.Type, attributeMappings []attrMapping) {
	isStructPointer := func(tt reflect.Type) bool {
		return tt.Kind() == reflect.Ptr && tt.Elem().Kind() == reflect.Struct
	}

	if !isStructPointer(t) {
		panic(errors.Errorf("%v is not a pointer to a struct", t))
	}
	var fieldInfos []fieldInfo
	for _, am := range attributeMappings {
		for _, sel := range am.selectors {
			fieldInfos = append(fieldInfos,
				sb.addTypeAttrMapping(am.a, t, sel))
		}
	}
	sort.Slice(fieldInfos, func(i, j int) bool {
		return fieldInfos[i].attr < fieldInfos[j].attr
	})
	attributeFields := make(map[ordinal][]fieldInfo)

	for i := 0; i < len(fieldInfos); {
		cur := fieldInfos[i].attr
		j := i + 1
		for ; j < len(fieldInfos); j++ {
			if fieldInfos[j].attr != cur {
				break
			}
		}
		attributeFields[cur] = fieldInfos[i:j]
		i = j
	}
	ts := &entityTypeSchema{
		typ:        t,
		fields:     fieldInfos,
		attrFields: attributeFields,
		typID:      uintptr(len(sb.entityTypes)),
	}
	sb.entityTypeSchemas[t] = ts
	sb.entityTypes = append(sb.entityTypes, ts)
}

func makeFieldFlags(t reflect.Type) (fieldFlags, bool) {
	var f fieldFlags
	if t.Kind() == reflect.Ptr {
		f |= pointerField
		t = t.Elem()
	}
	kind := t.Kind()
	switch {
	case kind == reflect.Struct && f.isPtr():
		f |= structField
	case kind == reflect.String:
		f |= stringField
	case isIntKind(kind):
		f |= intField
	case isUintKind(kind):
		f |= uintField
	default:
		return 0, false
	}
	return f, true
}

func (sb *schemaBuilder) addTypeAttrMapping(a Attr, t reflect.Type, sel string) fieldInfo {
	offset, cur := getOffsetAndTypeFromSelector(t, sel)

	flags, ok := makeFieldFlags(cur)
	if !ok {
		panic(errors.Errorf(
			"selector %q of %v has unsupported type %v",
			sel, t, cur,
		))
	}
	typ := cur
	if flags.isPtr() && flags.isScalar() {
		typ = cur.Elem()
	}
	ord := sb.maybeAddAttribute(a, typ)

	f := fieldInfo{
		fieldFlags: flags,
		path:       sel,
		attr:       ord,
		typ:        typ,
	}
	makeValueGetter := func(t reflect.Type, offset uintptr) func(u unsafe.Pointer) reflect.Value {
		return func(u unsafe.Pointer) reflect.Value {
			return reflect.NewAt(t, unsafe.Pointer(uintptr(u)+offset))
		}
	}
	getPtrValue := func(vg func(pointer unsafe.Pointer) reflect.Value) func(u unsafe.Pointer) interface{} {
		return func(u unsafe.Pointer) interface{} {
			got := vg(u)
			if got.Elem().IsNil() {
				return nil
			}
			return got.Elem().Interface()
		}
	}
	{
		vg := makeValueGetter(cur, offset)
		if f.isPtr() && f.isStruct() {
			f.value = getPtrValue(vg)
		} else if f.isPtr() && f.isScalar() {
			f.value = func(u unsafe.Pointer) interface{} {
				got := vg(u)
				ge := got.Elem()
				if ge.IsNil() {
					return nil
				}
				return ge.Elem().Interface()
			}
		} else {
			f.value = func(u unsafe.Pointer) interface{} {
				return vg(u).Elem().Interface()
			}
		}
		switch {
		case f.isPtr() && f.isInt():
			f.inline = func(u unsafe.Pointer) (uintptr, bool) {
				got := vg(u)
				if got.Elem().IsNil() {
					return 0, false
				}
				return uintptr(got.Elem().Elem().Int()), true
			}
		case f.isPtr() && f.isUint():
			f.inline = func(u unsafe.Pointer) (uintptr, bool) {
				got := vg(u)
				if got.Elem().IsNil() {
					return 0, false
				}
				return uintptr(got.Elem().Elem().Uint()), true
			}
		case f.isInt():
			f.inline = func(u unsafe.Pointer) (uintptr, bool) {
				return uintptr(vg(u).Elem().Int()), true
			}
		case f.isUint():
			f.inline = func(u unsafe.Pointer) (uintptr, bool) {
				return uintptr(vg(u).Elem().Uint()), true
			}
		case f.isString(), f.isStruct():
			f.inline = func(u unsafe.Pointer) (uintptr, bool) {
				return 0, false
			}
		}
	}
	{
		if f.isStruct() {
			f.comparableValue = getPtrValue(makeValueGetter(cur, offset))
		} else {
			compType := getComparableType(typ)
			if f.isPtr() && f.isScalar() {
				compType = reflect.PtrTo(compType)
			}
			vg := makeValueGetter(compType, offset)
			if f.isPtr() && f.isScalar() {
				f.comparableValue = getPtrValue(vg)
			} else {
				f.comparableValue = func(u unsafe.Pointer) interface{} {
					return vg(u).Interface()
				}
			}
		}
	}
	return f
}

// getOffsetAndTypeFromSelector takes an entity (struct pointer) type and a
// selector string and finds its offset within the struct. Note that this
// allows one to select fields in struct members of the current struct but
// not in referenced structs.
func getOffsetAndTypeFromSelector(
	structPointer reflect.Type, selector string,
) (uintptr, reflect.Type) {
	names := strings.Split(selector, ".")
	var offset uintptr
	cur := structPointer.Elem()
	for _, n := range names {
		sf, ok := cur.FieldByName(n)
		if !ok {
			panic(errors.Errorf("%v.%s is not a field", structPointer, selector))
		}
		offset += sf.Offset
		cur = sf.Type
	}
	return offset, cur
}

func (sc *Schema) mustGetOrdinal(attribute Attr) ordinal {
	ord, err := sc.getOrdinal(attribute)
	if err != nil {
		panic(err)
	}
	return ord
}

func (sc *Schema) getOrdinal(attribute Attr) (ordinal, error) {
	ord, ok := sc.attrToOrdinal[attribute]
	if !ok {
		return 0, errors.Errorf("unknown attribute %s in schema %s", attribute, sc.name)
	}
	return ord, nil
}
