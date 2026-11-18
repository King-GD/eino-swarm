package config

import "strings"

// SearchConfig is conversation search. Keyword indexing (FTS5) is always on.
// Selecting an embedding model enables semantic search; clearing it disables it.
type SearchConfig struct {
	// Embedding is a legacy compatibility projection, derived from EmbeddingModel.
	// It no longer controls admission; clear the model to disable search.
	Embedding bool `yaml:"embedding" json:"embedding"`
	// EmbeddingProvider is which endpoint /embeddings is sent to. Empty
	// follows models.default. A deleted id is cleared on load.
	EmbeddingProvider string `yaml:"embedding_provider" json:"embedding_provider"`
	// EmbeddingModel is the name that endpoint expects for embeddings.
	// Empty means keyword search only.
	// Never defaulted to a baked-in name.
	EmbeddingModel string `yaml:"embedding_model" json:"embedding_model"`
}

// SemanticEnabled depends only on whether an embedding model is configured.
func (s SearchConfig) SemanticEnabled() bool {
	return strings.TrimSpace(s.EmbeddingModel) != ""
}

// ResolveProvider is the endpoint id embeddings use. Empty follows the
// configured default, the same way the namer does.
func (s SearchConfig) ResolveProvider(defaultID string) string {
	if p := strings.TrimSpace(s.EmbeddingProvider); p != "" {
		return p
	}
	return strings.TrimSpace(defaultID)
}

func (c *Config) normalizeSearch() {
	c.Search.EmbeddingProvider = strings.TrimSpace(c.Search.EmbeddingProvider)
	c.Search.EmbeddingModel = strings.TrimSpace(c.Search.EmbeddingModel)
	c.Search.Embedding = c.Search.SemanticEnabled()
	if c.Search.EmbeddingProvider != "" && c.providerIndex(c.Search.EmbeddingProvider) < 0 {
		// A deleted endpoint must not keep embedding against a ghost id.
		c.Search.EmbeddingProvider = ""
	}
}
