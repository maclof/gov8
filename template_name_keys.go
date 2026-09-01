//go:build windows && amd64

package gov8

import "fmt"

// SetName is the Name-keyed counterpart of Set. key may be a String or a
// Symbol and is retained by V8 with the template.
func (t *ObjectTemplate) SetName(key Value, value Value) error {
	return t.SetNameWithAttr(key, value, AttrNone)
}

// SetNameWithAttr is Template::Set with a String or Symbol key and explicit
// property attributes.
func (t *ObjectTemplate) SetNameWithAttr(key Value, value Value, attr PropertyAttribute) error {
	data, err := value.Data()
	if err != nil {
		return err
	}
	return t.SetDataNameWithAttr(key, data, attr)
}

// SetDataNameWithAttr installs supported V8 Data under a String or Symbol
// key. Safe data is limited to primitives and nested Function/ObjectTemplate
// values, matching SetDataWithAttr's fatal-boundary protection.
func (t *ObjectTemplate) SetDataNameWithAttr(key Value, data Data, attr PropertyAttribute) error {
	if t == nil {
		return fmt.Errorf("gov8: nil object template")
	}
	if err := t.check(); err != nil {
		return err
	}
	return setTemplateDataNameWithAttr("ObjectTemplate.SetDataNameWithAttr", t.iso, t.sc, t.h, 0, key, data, attr)
}

// SetName is the Name-keyed counterpart of Set for properties installed on
// the function object produced by this template.
func (t *FunctionTemplate) SetName(key Value, value Value) error {
	return t.SetNameWithAttr(key, value, AttrNone)
}

// SetNameWithAttr is the FunctionTemplate flavor of Name-keyed Template::Set.
func (t *FunctionTemplate) SetNameWithAttr(key Value, value Value, attr PropertyAttribute) error {
	data, err := value.Data()
	if err != nil {
		return err
	}
	return t.SetDataNameWithAttr(key, data, attr)
}

// SetDataNameWithAttr installs supported V8 Data on the function object under
// a String or Symbol key.
func (t *FunctionTemplate) SetDataNameWithAttr(key Value, data Data, attr PropertyAttribute) error {
	if t == nil {
		return fmt.Errorf("gov8: nil function template")
	}
	if err := t.check(); err != nil {
		return err
	}
	return setTemplateDataNameWithAttr("FunctionTemplate.SetDataNameWithAttr", t.iso, t.sc, t.h, 1, key, data, attr)
}

func setTemplateDataNameWithAttr(op string, iso *Isolate, scope *Scope, template uintptr, templateKind uintptr, key Value, data Data, attr PropertyAttribute) error {
	if iso == nil || scope == nil || template == 0 {
		return fmt.Errorf("gov8: invalid template")
	}
	if err := scope.check(); err != nil {
		return err
	}
	if err := templateNameKey(iso, key); err != nil {
		return err
	}
	if err := data.check(); err != nil {
		return err
	}
	if data.iso != iso {
		return foreignIsolate("data")
	}
	isFunctionTemplate, err := data.IsFunctionTemplate()
	if err != nil {
		return err
	}
	isObjectTemplate, err := data.IsObjectTemplate()
	if err != nil {
		return err
	}
	isPrimitive, err := data.IsPrimitive()
	if err != nil {
		return err
	}
	if !isFunctionTemplate && !isObjectTemplate && !isPrimitive {
		return fmt.Errorf("gov8: template data must be a primitive Value, FunctionTemplate, or ObjectTemplate; JSReceiver and internal metadata values are unsafe")
	}
	if err := validPropertyAttribute(attr); err != nil {
		return err
	}
	return callErr(op, proc("gov8_ocr_template_set_data_with_attr"),
		iso.handleAssumingCheck(), scope.handle, template, templateKind,
		key.h, data.h, uintptr(attr))
}

// SetIntrinsicDataPropertyName binds a context intrinsic under a String or
// Symbol key on every object produced from the template.
func (t *ObjectTemplate) SetIntrinsicDataPropertyName(key Value, intrinsic Intrinsic, attr PropertyAttribute) error {
	if t == nil {
		return fmt.Errorf("gov8: nil object template")
	}
	if err := t.check(); err != nil {
		return err
	}
	return setTemplateIntrinsicDataPropertyName("ObjectTemplate.SetIntrinsicDataPropertyName", t.iso, t.sc, t.h, key, intrinsic, attr)
}

// SetIntrinsicDataPropertyName is the FunctionTemplate flavor of the shared
// Template base operation.
func (t *FunctionTemplate) SetIntrinsicDataPropertyName(key Value, intrinsic Intrinsic, attr PropertyAttribute) error {
	if t == nil {
		return fmt.Errorf("gov8: nil function template")
	}
	if err := t.check(); err != nil {
		return err
	}
	return setTemplateIntrinsicDataPropertyName("FunctionTemplate.SetIntrinsicDataPropertyName", t.iso, t.sc, t.h, key, intrinsic, attr)
}

func setTemplateIntrinsicDataPropertyName(op string, iso *Isolate, scope *Scope, template uintptr, key Value, intrinsic Intrinsic, attr PropertyAttribute) error {
	if err := templateNameKey(iso, key); err != nil {
		return err
	}
	if intrinsic > IntrinsicSetIteratorPrototype {
		return fmt.Errorf("gov8: intrinsic %d out of range", intrinsic)
	}
	if err := validPropertyAttribute(attr); err != nil {
		return err
	}
	return callErr(op, proc("gov8_template_set_intrinsic_data_property"),
		iso.handleAssumingCheck(), scope.handle, template, key.h,
		uintptr(uint8(intrinsic)), uintptr(attr))
}

func templateNameKey(iso *Isolate, key Value) error {
	if err := key.check(); err != nil {
		return err
	}
	if key.iso != iso {
		return foreignIsolate("key")
	}
	isName, err := key.IsName()
	if err != nil {
		return err
	}
	if !isName {
		return errNotAName
	}
	return nil
}
