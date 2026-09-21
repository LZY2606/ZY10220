package store

import (
	"encoding/json"
	"reflect"
)

func jsonUnmarshal(data []byte, v any) error { return json.Unmarshal(data, v) }

func dumpEqual(a, b *Dump) bool { return reflect.DeepEqual(a, b) }
