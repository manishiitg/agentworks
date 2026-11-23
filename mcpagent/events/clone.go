package events

import "reflect"

// CloneAgentEvent creates a deep copy of an AgentEvent so downstream consumers
// can safely store and serialize it without sharing mutable maps or slices.
func CloneAgentEvent(event *AgentEvent) *AgentEvent {
	if event == nil {
		return nil
	}

	cloned := deepCopyValue(reflect.ValueOf(event))
	if !cloned.IsValid() || cloned.IsNil() {
		return nil
	}

	clonedEvent, ok := cloned.Interface().(*AgentEvent)
	if !ok {
		return nil
	}

	return clonedEvent
}

func deepCopyValue(v reflect.Value) reflect.Value {
	if !v.IsValid() {
		return reflect.Value{}
	}

	switch v.Kind() {
	case reflect.Pointer:
		if v.IsNil() {
			return reflect.Zero(v.Type())
		}
		clonedElem := deepCopyValue(v.Elem())
		dst := reflect.New(v.Type().Elem())
		assignClonedValue(dst.Elem(), clonedElem)
		return dst

	case reflect.Interface:
		if v.IsNil() {
			return reflect.Zero(v.Type())
		}
		cloned := deepCopyValue(v.Elem())
		dst := reflect.New(v.Type()).Elem()
		assignClonedValue(dst, cloned)
		return dst

	case reflect.Struct:
		dst := reflect.New(v.Type()).Elem()
		dst.Set(v)
		for i := 0; i < v.NumField(); i++ {
			field := dst.Field(i)
			if !field.CanSet() {
				continue
			}
			assignClonedValue(field, deepCopyValue(v.Field(i)))
		}
		return dst

	case reflect.Slice:
		if v.IsNil() {
			return reflect.Zero(v.Type())
		}
		dst := reflect.MakeSlice(v.Type(), v.Len(), v.Len())
		for i := 0; i < v.Len(); i++ {
			assignClonedValue(dst.Index(i), deepCopyValue(v.Index(i)))
		}
		return dst

	case reflect.Array:
		dst := reflect.New(v.Type()).Elem()
		for i := 0; i < v.Len(); i++ {
			assignClonedValue(dst.Index(i), deepCopyValue(v.Index(i)))
		}
		return dst

	case reflect.Map:
		if v.IsNil() {
			return reflect.Zero(v.Type())
		}
		dst := reflect.MakeMapWithSize(v.Type(), v.Len())
		iter := v.MapRange()
		for iter.Next() {
			key := deepCopyValue(iter.Key())
			val := deepCopyValue(iter.Value())
			if !key.IsValid() {
				continue
			}
			if !val.IsValid() {
				val = reflect.Zero(v.Type().Elem())
			}
			if val.Type() != v.Type().Elem() {
				if val.Type().AssignableTo(v.Type().Elem()) {
					// No-op, handled below.
				} else if val.Type().ConvertibleTo(v.Type().Elem()) {
					val = val.Convert(v.Type().Elem())
				}
			}
			dst.SetMapIndex(key, val)
		}
		return dst

	default:
		return v
	}
}

func assignClonedValue(dst reflect.Value, src reflect.Value) {
	if !dst.CanSet() {
		return
	}
	if !src.IsValid() {
		dst.SetZero()
		return
	}
	if src.Type().AssignableTo(dst.Type()) {
		dst.Set(src)
		return
	}
	if src.Type().ConvertibleTo(dst.Type()) {
		dst.Set(src.Convert(dst.Type()))
	}
}
