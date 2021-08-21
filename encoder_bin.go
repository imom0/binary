package bin

import (
	"fmt"
	"reflect"

	"go.uber.org/zap"
)

func (e *Encoder) encodeBin(rv reflect.Value, opt *option) (err error) {
	if opt == nil {
		opt = newDefaultOption()
	}
	e.currentFieldOpt = opt

	if traceEnabled {
		zlog.Debug("encode: type",
			zap.Stringer("value_kind", rv.Kind()),
			zap.Reflect("options", opt),
		)
	}

	if opt.isOptional() {
		if rv.IsZero() {
			if traceEnabled {
				zlog.Debug("encode: skipping optional value with", zap.Stringer("type", rv.Kind()))
			}
			e.WriteBool(false)
			return nil
		}
		e.WriteBool(true)
	}

	if isZero(rv) {
		return nil
	}

	if marshaler, ok := rv.Interface().(BinaryMarshaler); ok {
		if traceEnabled {
			zlog.Debug("encode: using MarshalerBinary method to encode type")
		}
		return marshaler.MarshalWithEncoder(e)
	}

	switch rv.Kind() {
	case reflect.String:
		return e.WriteString(rv.String())
	case reflect.Uint8:
		return e.WriteByte(byte(rv.Uint()))
	case reflect.Int8:
		return e.WriteByte(byte(rv.Int()))
	case reflect.Int16:
		return e.WriteInt16(int16(rv.Int()), opt.Order)
	case reflect.Uint16:
		return e.WriteUint16(uint16(rv.Uint()), opt.Order)
	case reflect.Int32:
		return e.WriteInt32(int32(rv.Int()), opt.Order)
	case reflect.Uint32:
		return e.WriteUint32(uint32(rv.Uint()), opt.Order)
	case reflect.Uint64:
		return e.WriteUint64(rv.Uint(), opt.Order)
	case reflect.Int64:
		return e.WriteInt64(rv.Int(), opt.Order)
	case reflect.Float32:
		return e.WriteFloat32(float32(rv.Float()), opt.Order)
	case reflect.Float64:
		return e.WriteFloat64(rv.Float(), opt.Order)
	case reflect.Bool:
		return e.WriteBool(rv.Bool())
	case reflect.Ptr:
		return e.encodeBin(rv.Elem(), opt)
	case reflect.Interface:
		// skip
		return nil
	}

	rv = reflect.Indirect(rv)
	rt := rv.Type()
	switch rt.Kind() {
	case reflect.Array:
		l := rt.Len()
		if traceEnabled {
			defer func(prev *zap.Logger) { zlog = prev }(zlog)
			zlog = zlog.Named("array")
			zlog.Debug("encode: array", zap.Int("length", l), zap.Stringer("type", rv.Kind()))
		}
		for i := 0; i < l; i++ {
			if err = e.encodeBin(rv.Index(i), opt); err != nil {
				return
			}
		}
	case reflect.Slice:
		var l int
		if opt.hasSizeOfSlice() {
			l = opt.getSizeOfSlice()
			if traceEnabled {
				zlog.Debug("encode: slice with sizeof set", zap.Int("size_of", l))
			}
		} else {
			l = rv.Len()
			if err = e.WriteUVarInt(l); err != nil {
				return
			}
		}
		if traceEnabled {
			defer func(prev *zap.Logger) { zlog = prev }(zlog)
			zlog = zlog.Named("slice")
			zlog.Debug("encode: slice", zap.Int("length", l), zap.Stringer("type", rv.Kind()))
		}

		// we would want to skip to the correct head_offset

		for i := 0; i < l; i++ {
			if err = e.encodeBin(rv.Index(i), opt); err != nil {
				return
			}
		}
	case reflect.Struct:
		if err = e.encodeStructBin(rt, rv); err != nil {
			return
		}

	case reflect.Map:
		keyCount := len(rv.MapKeys())

		if traceEnabled {
			zlog.Debug("encode: map",
				zap.Int("key_count", keyCount),
				zap.String("key_type", rt.String()),
				typeField("value_type", rv.Elem()),
			)
			defer func(prev *zap.Logger) { zlog = prev }(zlog)
			zlog = zlog.Named("struct")
		}

		if err = e.WriteUVarInt(keyCount); err != nil {
			return
		}

		for _, mapKey := range rv.MapKeys() {
			if err = e.Encode(mapKey.Interface()); err != nil {
				return
			}

			if err = e.Encode(rv.MapIndex(mapKey).Interface()); err != nil {
				return
			}
		}

	default:
		return fmt.Errorf("encode: unsupported type %q", rt)
	}
	return
}

func (e *Encoder) encodeStructBin(rt reflect.Type, rv reflect.Value) (err error) {
	l := rv.NumField()

	if traceEnabled {
		zlog.Debug("encode: struct", zap.Int("fields", l), zap.Stringer("type", rv.Kind()))
	}

	sizeOfMap := map[string]int{}
	for i := 0; i < l; i++ {
		structField := rt.Field(i)
		fieldTag := parseFieldTag(structField.Tag)

		if fieldTag.Skip {
			if traceEnabled {
				zlog.Debug("encode: skipping struct field with skip flag",
					zap.String("struct_field_name", structField.Name),
				)
			}
			continue
		}

		rv := rv.Field(i)

		if fieldTag.SizeOf != "" {
			if traceEnabled {
				zlog.Debug("encode: struct field has sizeof tag",
					zap.String("sizeof_field_name", fieldTag.SizeOf),
					zap.String("struct_field_name", structField.Name),
				)
			}
			sizeOfMap[fieldTag.SizeOf] = sizeof(structField.Type, rv)
		}

		if !rv.CanInterface() {
			if traceEnabled {
				zlog.Debug("encode:  skipping field: unable to interface field, probably since field is not exported",
					zap.String("sizeof_field_name", fieldTag.SizeOf),
					zap.String("struct_field_name", structField.Name),
				)
			}
			continue
		}

		option := &option{
			OptionalField: fieldTag.Optional,
			Order:         fieldTag.Order,
		}

		if s, ok := sizeOfMap[structField.Name]; ok {
			if traceEnabled {
				zlog.Debug("setting sizeof option", zap.String("of", structField.Name), zap.Int("size", s))
			}
			option.setSizeOfSlice(s)
		}

		if traceEnabled {
			zlog.Debug("encode: struct field",
				zap.Stringer("struct_field_value_type", rv.Kind()),
				zap.String("struct_field_name", structField.Name),
				zap.Reflect("struct_field_tags", fieldTag),
				zap.Reflect("struct_field_option", option),
			)
		}

		if err := e.encodeBin(rv, option); err != nil {
			return err
		}
	}
	return nil
}
