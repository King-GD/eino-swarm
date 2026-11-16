package speech

import (
	"context"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/LubyRuffy/eino-swarm/internal/provider"
)

// Models lists only explicitly typed voice names, before the user chooses one.
// An unclassified local model can still be entered manually in Settings.
func (s *Service) Models(ctx context.Context, isInput bool) ([]string, error) {
	cfg := s.cfg.Snapshot()
	selected, err := s.endpointTarget(cfg, isInput)
	if err != nil {
		return nil, err
	}
	if s.pool != nil && s.pool.OfficialOnly() {
		return []string{selected.target.Model}, nil
	}
	ctx, cancel := context.WithTimeout(ctx, time.Duration(selected.timeout)*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(selected.target.BaseURL, "/")+"/models", nil)
	if err != nil {
		return nil, failure("speech_not_configured", 400)
	}
	req.Header.Set("Accept", "application/json")
	if selected.target.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+selected.target.APIKey)
	}
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := client.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, failure("speech_catalog_unavailable", 502)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, failure("speech_catalog_unavailable", 502)
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, (1<<20)+1))
	if err != nil || len(raw) > 1<<20 {
		return nil, failure("speech_invalid_catalog", 502)
	}
	cat, err := provider.ParseCatalog(raw)
	if err != nil {
		return nil, failure("speech_invalid_catalog", 502)
	}
	kind := "speech"
	if isInput {
		kind = "transcription"
	}
	names := []string{}
	for _, name := range cat.Names {
		if cat.Types[name] == kind {
			names = append(names, name)
		}
	}
	return names, nil
}
