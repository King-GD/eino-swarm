package remote

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/LubyRuffy/eino-swarm/internal/provider"
)

// accountCatalogRef is one server catalog LOCAL can list beside the desktop
// catalog. The credential stays on the signed-in account.
type accountCatalogRef struct {
	sourceID, label, apiBaseURL, catalogURL string
	sameAsPreset                            bool
}

const (
	catalogOmit = iota
	catalogReady
	catalogKeep
)

// attachSwitchableCatalogs adds the authorized third-party preset and original
// gateway catalogs while LOCAL is signed in. A denied or empty catalog is
// omitted. A transient failure retains only that still-authorized source.
func (a *AccountModels) attachSwitchableCatalogs(ctx context.Context, boot accountBootstrap, token string, o accountOutcome) accountOutcome {
	if o.state.State != AccountReady && o.state.State != AccountNoAccess && !o.transient {
		return o
	}
	sources := a.switchableCatalogs(ctx, boot, token)
	o.localUpdated = true
	var extra []*provider.AccountSource
	for _, item := range sources {
		if item.src == nil {
			continue
		}
		if o.source == nil && item.preset && !o.transient {
			kinds := item.src.Kinds()
			o.source = item.src
			o.state.State = AccountReady
			o.state.Reason = ""
			o.state.Label = boot.label
			o.state.Models = item.models
			o.state.SourceID = item.src.ID()
			o.state.Kinds = &kinds
		}
		extra = append(extra, item.src)
	}
	o.local = extra
	if len(extra) > 0 && o.state.State == AccountNoAccess {
		o.state.State = AccountReady
		o.state.Reason = ""
	}
	return o
}

func (a *AccountModels) switchableCatalogs(ctx context.Context, boot accountBootstrap, token string) []switchableSource {
	var refs []accountCatalogRef
	if boot.access && boot.clientCatalogURL != "" {
		refs = append(refs, accountCatalogRef{sourceID: boot.sourceID, label: boot.label, apiBaseURL: boot.apiBaseURL, catalogURL: boot.clientCatalogURL, sameAsPreset: true})
	}
	if boot.gatewayCatalog != nil {
		refs = append(refs, *boot.gatewayCatalog)
	}
	var sources []switchableSource
	for _, ref := range refs {
		item, status := a.oneCatalog(ctx, token, ref)
		switch status {
		case catalogReady:
			sources = append(sources, item)
		case catalogKeep:
			a.log.Warn("account catalog refresh failed; retaining only its authorized cached source")
			for _, cached := range a.pool.LocalAccountSources() {
				if cached.MatchesCatalogConnection(ref.sourceID, ref.apiBaseURL, token) {
					sources = append(sources, switchableSource{src: cached, models: cached.ModelCount(), preset: ref.sameAsPreset})
					break
				}
			}
		}
	}
	return sources
}

type switchableSource struct {
	src    *provider.AccountSource
	models int
	preset bool
}

func (a *AccountModels) oneCatalog(ctx context.Context, token string, ref accountCatalogRef) (switchableSource, int) {
	raw, err := a.get(ctx, ref.catalogURL, token)
	if err != nil {
		return switchableSource{}, catalogStatus(err)
	}
	cat, err := provider.ParseCatalog(raw)
	if err != nil {
		a.log.Warn("account catalog was not published", "reason", AccountReasonUnsafe)
		return switchableSource{}, catalogOmit
	}
	cat = boundCatalog(cat)
	if len(cat.Names) == 0 {
		return switchableSource{}, catalogOmit
	}
	src, err := provider.NewAccountSource(ref.sourceID, ref.label, ref.apiBaseURL, token, cat)
	if err != nil {
		a.log.Warn("account catalog was not published", "reason", AccountReasonUnsafe)
		return switchableSource{}, catalogOmit
	}
	return switchableSource{src: src, models: len(cat.Names), preset: ref.sameAsPreset}, catalogReady
}

func catalogStatus(err error) int {
	var status *accountStatusError
	switch {
	case errors.As(err, &status):
		switch status.code {
		case http.StatusUnauthorized, http.StatusForbidden, http.StatusNotFound:
			return catalogOmit
		default:
			return catalogKeep
		}
	case errors.Is(err, errAccountResponse):
		return catalogOmit
	default:
		return catalogKeep
	}
}

func (b *accountBootstrap) parseSwitchableCatalogs(clientCatalogURL string, gatewayRaw json.RawMessage, origin string) error {
	clientCatalogURL = strings.TrimSpace(clientCatalogURL)
	if clientCatalogURL != "" {
		if !sameOriginURL(clientCatalogURL, origin) {
			return errAccountResponse
		}
		b.clientCatalogURL = clientCatalogURL
	}
	if len(gatewayRaw) == 0 || string(gatewayRaw) == "null" {
		return nil
	}
	var body struct {
		SourceID         string `json:"source_id"`
		SourceKind       string `json:"source_kind"`
		Label            string `json:"label"`
		Execution        string `json:"execution"`
		APIBaseURL       string `json:"api_base_url"`
		CatalogURL       string `json:"catalog_url"`
		APIAccessEnabled bool   `json:"api_access_enabled"`
	}
	if json.Unmarshal(gatewayRaw, &body) != nil || !accountSourceID.MatchString(body.SourceID) || body.SourceID == b.sourceID || body.SourceKind != "gateway" || body.Execution != "desktop" {
		return errAccountResponse
	}
	label := strings.TrimSpace(body.Label)
	if len(label) > accountMaxLabelLen || strings.IndexFunc(label, func(r rune) bool { return r < 0x20 || r == 0x7f }) >= 0 {
		return errAccountResponse
	}
	if !sameOriginURL(body.APIBaseURL, origin) || !sameOriginURL(body.CatalogURL, origin) {
		return errAccountResponse
	}
	if !body.APIAccessEnabled {
		return nil
	}
	b.gatewayCatalog = &accountCatalogRef{
		sourceID: body.SourceID, label: label,
		apiBaseURL: strings.TrimRight(body.APIBaseURL, "/"), catalogURL: body.CatalogURL,
	}
	return nil
}
