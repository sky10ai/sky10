package adapters

import (
	"fmt"

	"github.com/sky10/sky10/pkg/messengers/adapters/imapsmtp"
	"github.com/sky10/sky10/pkg/messengers/adapters/shared"
)

type Definition = shared.Definition

var builtins = []Definition{
	imapsmtp.Definition,
}

// Builtins returns the built-in adapter definitions in stable name order.
func Builtins() []Definition {
	return append([]Definition(nil), builtins...)
}

// Lookup finds one built-in adapter by name.
func Lookup(name string) (Definition, bool) {
	for _, definition := range builtins {
		if definition.Name == name {
			return definition, true
		}
	}
	return Definition{}, false
}

// Names returns the sorted built-in adapter names.
func Names() []string {
	items := Builtins()
	names := make([]string, 0, len(items))
	for _, item := range items {
		names = append(names, item.Name)
	}
	return names
}

// MustLookup returns one built-in adapter or panics when it is missing.
func MustLookup(name string) Definition {
	definition, ok := Lookup(name)
	if !ok {
		panic(fmt.Sprintf("messaging adapter %q is not registered", name))
	}
	return definition
}
