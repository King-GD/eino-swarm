package remote

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/LubyRuffy/eino-swarm/internal/provider"
)

// accountCatalogRef is one server catalog LOCAL can list beside personal
// providers. The credential stays on the signed-in account; this is not a
// personal provider.
type accountCatalogRef struct {
	sourceID, label, apiBaseURL, catalogURL string
	group                                   string
}

const (
	catalogOmit = iota
	catalogReady
	catalogKeep
)

// withSwitchableCatalogs attaches the server catalogs a signed-in LOCAL
// session switches among. A denied or empty catalog is omitted. A transient
// failure keeps the catalogs already published and does not sign the account out.
func (a *AccountModels) withSwitchableCatalogs(ctx context.Context, boot accountBootstrap, token string, o accountOutcome) accountOutcome {
	if o.source == nil {
		return o
	}
	sources, keep := a.switchableCatalogs(ctx, boot, token)
	o.local, o.keepLocal = sources, keep
	return o
}

func (a *AccountModels) switchableCatalogs(ctx context.Context, boot accountBootstrap, token string) ([]*provider.AccountSource, bool) {
	refs := make([]accountCatalogRef, 0, 2)
	if boot.clientCatalogURL != "" {
		refs = append(refs, accountCatalogRef{sourceID: boot.sourceID, label: boot.label, apiBaseURL: boot.apiBaseURL, catalogURL: boot.clientCatalogURL, group: "third_party"})
	}
	if boot.gatewayCatalog != nil {
		refs = append(refs, *boot.gatewayCatalog)
	}
	var sources []*provider.AccountSource
	for _, ref := range refs {
		src, status := a.oneCatalog(ctx, token, ref)
		switch status {
		case catalogReady:
			sources = append(sources, src)
		case catalogKeep:
			return nil, true
		}
	}
	return sources, false
}

func (a *AccountModels) oneCatalog(ctx context.Context, token string, ref accountCatalogRef) (*provider.AccountSource, int) {
	raw, err := a.get(ctx, ref.catalogURL, token)
	if err != nil {
		if catalogStatus(err) == catalogKeep {
			a.log.Warn("account catalog refresh failed; keeping the last switchable models")
		}
		return nil, catalogStatus(err)
	}
	cat, err := provider.ParseCatalog(raw)
	if err != nil {
		a.log.Warn("account catalog was not published", "reason", AccountReasonUnsafe)
		return nil, catalogOmit
	}
	cat = boundCatalog(cat)
	if len(cat.Names) == 0 {
		return nil, catalogOmit
	}
	src, err := provider.NewAccountSource(ref.sourceID, ref.label, ref.apiBaseURL, token, cat)
	if err != nil {
		a.log.Warn("account catalog was not published", "reason", AccountReasonUnsafe)
		return nil, catalogOmit
	}
	src.SetModelPolicy(true, nil)
	src.SetCatalogGroup(ref.group)
	return src, catalogReady
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
		group:      "original",
		apiBaseURL: strings.TrimRight(body.APIBaseURL, "/"), catalogURL: body.CatalogURL,
	}
	return nil
}
