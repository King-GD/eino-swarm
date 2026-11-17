package frontend

import _ "embed"

//go:embed src/task-panels/catalog.json
var taskPanelCatalog []byte

// TaskPanelCatalog returns the same plugin definitions used by the UI build.
// A caller cannot mutate the embedded catalog for another engine.
func TaskPanelCatalog() []byte { return append([]byte(nil), taskPanelCatalog...) }
