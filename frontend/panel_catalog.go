package frontend

import _ "embed"

//go:embed src/task-panels/catalog.json
var taskPanelCatalog []byte

//go:embed src/task-panels/easm-v1.json
var easmV1Definition []byte

//go:embed src/task-panels/easm-v2.json
var easmV2Definition []byte

// TaskPanelCatalog returns the same plugin definitions used by the UI build.
// A caller cannot mutate the embedded catalog for another engine.
func TaskPanelCatalog() []byte { return append([]byte(nil), taskPanelCatalog...) }

// EASMV1Definition is the first published EASM contract, shared with the UI.
func EASMV1Definition() []byte { return append([]byte(nil), easmV1Definition...) }

// EASMV2Definition is the second published EASM contract, kept so older
// clients and saved panels still resolve to the schema they were written for.
func EASMV2Definition() []byte { return append([]byte(nil), easmV2Definition...) }
