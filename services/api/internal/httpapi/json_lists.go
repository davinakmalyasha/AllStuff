package httpapi

import "reflect"

// nonNilLists makes nil slices marshal as `[]` instead of `null`.
//
// Every list endpoint in this API is a collection: `comments`, `products`,
// `similar`, `threads`, `reviews`. Go's encoding/json renders a nil slice as
// `null`, so a repo that returns "no rows" — the overwhelmingly common case for
// comments and similar-businesses — produced `{"comments":null}`. The frontend
// reads `.length` on it and throws, and the route's ErrorBoundary replaces the
// whole page with "Something broke". An empty collection is not an error, so it
// must not be encoded as a value that reads as one.
//
// This is enforced centrally rather than at 29 call sites because the contract
// belongs to the encoder, not to each repo: forgetting it in one function would
// reintroduce a crash that only appears once that business has no comments.
//
// Only slices and maps are touched. Pointers are deliberately left alone —
// `logo_url: null` and `deleted_at: null` are meaningful, and a client
// distinguishes "absent" from "empty" for those.
//
// A nil slice nested in an `any` (as every handler response is) is rewritten
// here; `map[string]any{"products": products}` holds a *typed* nil slice, which
// is a non-nil interface wrapping a nil value and therefore invisible to a
// nil-check on the interface itself.
func nonNilLists(v any) any {
	if v == nil {
		return nil
	}
	rv, changed := normalizeValue(reflect.ValueOf(v), map[uintptr]bool{})
	if !changed || !rv.IsValid() {
		return v
	}
	return rv.Interface()
}

// normalizeValue returns a rewritten value and whether anything changed. The
// changed flag is essential: the type alone is not a signal, because replacing a
// nil slice with an empty one preserves the type, and treating "same type" as
// "unchanged" would silently skip the fix.
func normalizeValue(rv reflect.Value, seen map[uintptr]bool) (reflect.Value, bool) {
	switch rv.Kind() {
	case reflect.Interface:
		if rv.IsNil() {
			return rv, false
		}
		inner, changed := normalizeValue(rv.Elem(), seen)
		if !changed {
			return rv, false
		}
		out := reflect.New(rv.Type()).Elem()
		out.Set(inner)
		return out, true

	case reflect.Ptr:
		// Pointers are left as-is: a nil *string must stay null, and a
		// pointer-to-nil-slice is a legitimate "absent" value.
		return rv, false

	case reflect.Slice:
		if rv.IsNil() {
			return reflect.MakeSlice(rv.Type(), 0, 0), true
		}
		var rebuilt reflect.Value
		changed := false
		for i := 0; i < rv.Len(); i++ {
			elem, elemChanged := normalizeValue(rv.Index(i), seen)
			if !elemChanged {
				continue
			}
			if !rebuilt.IsValid() {
				rebuilt = reflect.MakeSlice(rv.Type(), rv.Len(), rv.Len())
				reflect.Copy(rebuilt, rv)
			}
			rebuilt.Index(i).Set(elem)
			changed = true
		}
		if changed {
			return rebuilt, true
		}
		return rv, false

	case reflect.Map:
		if rv.IsNil() {
			return reflect.MakeMap(rv.Type()), true
		}
		changed := false
		out := reflect.MakeMapWithSize(rv.Type(), rv.Len())
		iter := rv.MapRange()
		for iter.Next() {
			val, valChanged := normalizeValue(iter.Value(), seen)
			if !valChanged {
				val = iter.Value()
			} else {
				changed = true
			}
			out.SetMapIndex(iter.Key(), val)
		}
		if changed {
			return out, true
		}
		return rv, false

	case reflect.Struct:
		// Structs with unexported fields (time.Time) cannot be rebuilt
		// element-by-element; JSON only reads their exported fields, which
		// cannot contain a bare slice needing this fix in practice.
		t := rv.Type()
		exported := 0
		for i := 0; i < t.NumField(); i++ {
			if t.Field(i).IsExported() {
				exported++
			}
		}
		if exported == 0 {
			return rv, false
		}
		out := reflect.New(t).Elem()
		out.Set(rv)
		changed := false
		for i := 0; i < t.NumField(); i++ {
			if !t.Field(i).IsExported() {
				continue
			}
			nv, nvChanged := normalizeValue(rv.Field(i), seen)
			if !nvChanged {
				continue
			}
			out.Field(i).Set(nv)
			changed = true
		}
		if changed {
			return out, true
		}
		return rv, false
	}
	return rv, false
}
