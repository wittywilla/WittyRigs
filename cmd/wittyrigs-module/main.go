// Command wittyrigs-module is WittyRigs as a helper process: the only way a server reaches it. It
// only looks for Sunshine on this machine. See WittyModule for the protocol.
package main

import (
	"encoding/json"
	"fmt"

	"github.com/wittywilla/wittymodule"
	"github.com/wittywilla/wittyrigs"
)

func main() {
	wittymodule.Main(wittyrigs.ManifestJSON, func(method string, _ json.RawMessage) (any, []byte, error) {
		if method == "find" {
			return struct {
				Path    string `json:"path"`
				License string `json:"license"`
				Source  string `json:"source"`
			}{wittyrigs.Find(), wittyrigs.License, wittyrigs.Source}, nil, nil
		}
		return nil, nil, &wittymodule.CallError{Code: "unknown-method", Message: fmt.Sprintf("this version has no %q", method)}
	})
}
