package easm

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"
)

// The step needs only the certificate search. When the TLS asset lookup is
// also configured it is asked for the same domains, so services whose search
// rows lack certificate fields still contribute their certificates.
func init() {
	stepFuncs[StepCertificates] = stepDef{roles: []string{RoleCertificateSearch}, fn: (*run).certificates}
}

// certificateSearchFields are the search columns that describe a served
// certificate. Content fields (banner, cert, header, body) are disabled on
// the search tool, so the certificate is read from its cert.* subfields.
const certificateSearchFields = "host,domain,cert.subject.cn,cert.subject.org,cert.issuer.org,cert.not_before,cert.not_after,cert.is_valid,cert.is_match,cert.is_expired"

// certificatePageSize bounds the rows requested per domain and tool.
const certificatePageSize = 50

// certificateFlushEvery publishes progress after this many domains.
const certificateFlushEvery = 3

// certificateRow is one service's certificate observation, from either tool.
type certificateRow struct {
	source    string
	host      string
	domain    string
	cn        string
	issuer    string
	serial    string
	notBefore string
	notAfter  string
	subjects  []string
	names     []string
	identity  string
	// local marks an identity derived from this asset alone: without a
	// certificate identifier, two services are never merged by their names.
	local   bool
	label   string
	valid   *bool
	match   *bool
	expired *bool
}

// certificates looks up the certificates served for each graph domain.
func (r *run) certificates(ctx context.Context) error {
	domains := r.graph.Domains()
	if len(domains) > r.settings.MaxDomains {
		domains = domains[:r.settings.MaxDomains]
	}
	known := r.certificateIndex()
	lookup := r.settings.Tools[RoleAssetLookup] != ""
	for i, domain := range domains {
		if i > 0 && i%certificateFlushEvery == 0 {
			if err := r.flush(ctx); err != nil {
				return err
			}
		}
		rows, err := r.searchCertificates(ctx, domain)
		if err != nil {
			return err
		}
		if lookup {
			tls, err := r.lookupCertificates(ctx, domain)
			switch {
			case errors.Is(err, ErrUnavailable):
				// The lookup is optional; the search alone still runs.
				lookup = false
			case err != nil:
				return err
			}
			rows = append(rows, tls...)
		}
		for _, row := range rows {
			r.addCertificate(row, domain, known)
		}
	}
	return nil
}

// certificateIndex maps issuer and serial to the certificate already on the
// graph, so a row lacking the fingerprint still reaches the same identity.
func (r *run) certificateIndex() map[string]string {
	index := map[string]string{}
	for _, node := range r.graph.nodes {
		if node.Kind == "feature" && node.FeatureKind == "cert" && node.Detail != nil && node.Detail.Issuer != "" && node.Detail.Serial != "" {
			index[issuerSerial(node.Detail.Issuer, node.Detail.Serial)] = node.ID
		}
	}
	return index
}

func issuerSerial(issuer, serial string) string {
	key, _ := json.Marshal([]string{issuer, serial})
	return string(key)
}

func (r *run) searchCertificates(ctx context.Context, domain string) ([]certificateRow, error) {
	var reply struct {
		Fields  json.RawMessage    `json:"fields"`
		Results *[]json.RawMessage `json:"results"`
	}
	arguments := map[string]any{"query": fmt.Sprintf("domain=%q", domain), "fields": certificateSearchFields, "size": certificatePageSize}
	if err := r.call(ctx, RoleCertificateSearch, arguments, &reply); err != nil {
		return nil, err
	}
	// A reply with only totals has no service rows: a count is not a
	// certificate observation.
	if reply.Results == nil {
		return nil, fmt.Errorf("certificate search reply has no results")
	}
	fields := strings.Split(certificateSearchFields, ",")
	if len(reply.Fields) != 0 {
		fields = certSearchFields(reply.Fields)
	}
	var rows []certificateRow
	for _, raw := range *reply.Results {
		if row, ok := certSearchRow(raw, fields); ok {
			rows = append(rows, row)
		}
	}
	return rows, nil
}

// certSearchFields accepts the reply's column list as an array or a comma list.
// Blank or repeated columns make tuple rows unreadable.
func certSearchFields(raw json.RawMessage) []string {
	var fields []string
	if json.Unmarshal(raw, &fields) != nil {
		var text string
		if json.Unmarshal(raw, &text) != nil {
			return nil
		}
		fields = strings.Split(text, ",")
	}
	seen := map[string]bool{}
	for i, field := range fields {
		field = strings.TrimSpace(field)
		if field == "" || seen[field] {
			return nil
		}
		fields[i], seen[field] = field, true
	}
	return fields
}

// certSearchRow reads an object row or a tuple aligned with fields. A row
// without certificate identity is an ordinary asset and yields nothing.
func certSearchRow(raw json.RawMessage, fields []string) (certificateRow, bool) {
	var values map[string]json.RawMessage
	if json.Unmarshal(raw, &values) != nil || values == nil {
		var tuple []json.RawMessage
		if len(fields) == 0 || json.Unmarshal(raw, &tuple) != nil || len(tuple) != len(fields) {
			return certificateRow{}, false
		}
		values = make(map[string]json.RawMessage, len(fields))
		for i, field := range fields {
			values[field] = tuple[i]
		}
	}
	row := certificateRow{source: RoleCertificateSearch}
	rawDomain := certString(values, "domain")
	row.domain = NormalizeDomain(rawDomain)
	if rawDomain != "" && row.domain == "" {
		return certificateRow{}, false
	}
	var hostDomain string
	row.host, hostDomain = certServiceHost(certString(values, "host"))
	if row.domain == "" {
		row.domain = hostDomain
	}
	row.cn = certString(values, "cert.subject.cn")
	row.issuer = certString(values, "cert.issuer.org")
	row.serial = certFirstString(values, "cert.serial", "cert.serial_number", "cert.sn")
	row.notBefore = certString(values, "cert.not_before")
	row.notAfter = certString(values, "cert.not_after")
	if organization := certString(values, "cert.subject.org"); organization != "" {
		row.subjects = []string{organization}
	}
	row.valid = certFlag(certField(values, "cert.is_valid"))
	row.match = certFlag(certField(values, "cert.is_match"))
	row.expired = certFlag(certField(values, "cert.is_expired"))
	return row, row.identify(values)
}

func (r *run) lookupCertificates(ctx context.Context, domain string) ([]certificateRow, error) {
	var reply struct {
		Results *[]json.RawMessage `json:"results"`
	}
	arguments := map[string]any{"domains": []string{domain}, "include": []string{"tls"}, "size": certificatePageSize}
	if err := r.call(ctx, RoleAssetLookup, arguments, &reply); err != nil {
		return nil, err
	}
	if reply.Results == nil {
		return nil, fmt.Errorf("asset lookup reply has no results")
	}
	var rows []certificateRow
	for _, raw := range *reply.Results {
		if row, ok := certLookupRow(raw); ok {
			rows = append(rows, row)
		}
	}
	return rows, nil
}

// certLookupRow reads a TLS asset record. The deployed host comes from the
// asset; the certificate's root-domain hints are kept as names only.
func certLookupRow(raw json.RawMessage) (certificateRow, bool) {
	var asset struct {
		Host   string `json:"host"`
		Domain string `json:"domain"`
		TLS    struct {
			Certificate map[string]json.RawMessage `json:"certificate"`
		} `json:"tls"`
	}
	if json.Unmarshal(raw, &asset) != nil || len(asset.TLS.Certificate) == 0 {
		return certificateRow{}, false
	}
	certificate := asset.TLS.Certificate
	var subjects, issuers, roots []string
	for key, list := range map[string]*[]string{"subject_orgs": &subjects, "issuer_orgs": &issuers, "root_domains": &roots} {
		if value := certificate[key]; len(value) != 0 && json.Unmarshal(value, list) != nil {
			return certificateRow{}, false
		}
	}
	row := certificateRow{source: RoleAssetLookup}
	var hostDomain string
	row.host, hostDomain = certServiceHost(asset.Host)
	if row.domain = NormalizeDomain(asset.Domain); row.domain == "" {
		row.domain = hostDomain
	}
	row.cn = certString(certificate, "subject_cn")
	if row.issuer = certString(certificate, "issuer_cn"); row.issuer == "" {
		row.issuer = strings.Join(issuers, " / ")
	}
	row.serial = certString(certificate, "serial_number")
	row.notBefore = certString(certificate, "not_before")
	row.notAfter = certString(certificate, "not_after")
	for _, subject := range subjects {
		if subject = strings.TrimSpace(subject); subject != "" {
			row.subjects = append(row.subjects, subject)
		}
	}
	for _, root := range roots {
		if root = NormalizeDomain(root); root != "" {
			row.names = append(row.names, root)
		}
	}
	row.valid = certFlag(certificate["is_valid"])
	row.match = certFlag(certificate["is_match"])
	row.expired = certFlag(certificate["is_expired"])
	values := map[string]json.RawMessage{"cert.sha256": certificate["sha256"], "cert.fingerprint": certificate["fingerprint"], "cert.id": certificate["id"]}
	return row, row.identify(values)
}

// identify sets the certificate identity: a reported fingerprint or id, then
// issuer and serial, then an asset-local observation that needs at least a
// name or serial. It reports false when the row describes no certificate.
func (row *certificateRow) identify(values map[string]json.RawMessage) bool {
	for _, field := range []string{"cert.sha256", "cert.fingerprint", "cert.id"} {
		if value := certScalar(certField(values, field)); value != "" {
			row.identity, row.label = field+":"+value, value
			break
		}
	}
	if row.identity == "" && row.serial != "" && row.issuer != "" {
		row.identity = "issuer-serial:" + issuerSerial(row.issuer, row.serial)
	}
	if row.identity == "" {
		asset := row.domain
		if asset == "" {
			asset = row.host
		}
		if asset == "" || row.cn == "" && row.serial == "" {
			return false
		}
		key, _ := json.Marshal([]string{asset, row.cn, strings.Join(row.subjects, "\n"), row.issuer, row.serial, row.notBefore, row.notAfter})
		row.identity, row.local = "asset-observation:"+string(key), true
	}
	switch {
	case row.cn != "":
		row.label = row.cn
	case row.serial != "":
		row.label = row.serial
	}
	return true
}

// addCertificate records one observation. queried is the graph domain whose
// lookup returned the row; an IP-only observation hangs under it as a
// pending clue because the lookup condition is the only link it has.
func (r *run) addCertificate(row certificateRow, queried string, known map[string]string) {
	domainID := ""
	if row.domain != "" {
		domainID = DomainID(row.domain)
		if r.graph.Node(domainID) == nil {
			// A certificate-only domain is a pending clue of the subject,
			// never a detached island.
			r.graph.AddDomain(RootID, row.domain, false, Basis{Source: row.source, Via: row.label})
		}
	}
	id := fmt.Sprintf("feature:cert:%x", sha256.Sum256([]byte(row.identity)))
	key := ""
	if row.issuer != "" && row.serial != "" {
		key = issuerSerial(row.issuer, row.serial)
		if existing, ok := known[key]; ok && r.graph.Node(id) == nil {
			id = existing
		}
	}
	var features []Feature
	for _, subject := range row.subjects {
		features = append(features, Feature{Kind: "org", Value: subject})
	}
	detail := &Detail{Serial: row.serial, Issuer: row.issuer, NotBefore: row.notBefore, NotAfter: row.notAfter, Features: features, Names: append(certNonEmpty(row.cn), row.names...)}
	if len(row.subjects) > 0 {
		detail.SubjectName = row.subjects[0]
	}
	node := r.graph.Upsert(Node{ID: id, Kind: "feature", FeatureKind: "cert", Label: row.label, Detail: detail})
	if key != "" {
		known[key] = id
	}
	r.graph.AddBasis(id, Basis{Source: row.source})
	certObserve(node.Detail, row)
	if domainID == "" {
		r.graph.Link(DomainID(queried), id, false)
		return
	}
	effective := certEffectiveFor(node.Detail.Observations, row.domain)
	r.graph.Link(domainID, id, effective)
	if effective {
		r.graph.Upsert(Node{ID: domainID, Label: row.domain, Detail: &Detail{Features: []Feature{{Kind: "cert", Value: node.Label, NodeID: id}}}})
	}
}

// certObserve replaces this source's observation of the same service and domain
// and recomputes the top-level flags, which are kept only when every
// observation reports the same known value.
func certObserve(detail *Detail, row certificateRow) {
	host := row.host
	if host == "" {
		host = row.domain
	}
	observed := Observation{Host: host, Domain: row.domain, Source: row.source, IsValid: row.valid, IsMatch: row.match, IsExpired: row.expired}
	replaced := false
	for i, previous := range detail.Observations {
		if previous.Host == observed.Host && previous.Domain == observed.Domain && previous.Source == observed.Source {
			detail.Observations[i], replaced = observed, true
		}
	}
	if !replaced {
		detail.Observations = append(detail.Observations, observed)
	}
	detail.IsValid = certConsensus(detail.Observations, func(o Observation) *bool { return o.IsValid })
	detail.IsMatch = certConsensus(detail.Observations, func(o Observation) *bool { return o.IsMatch })
}

func certConsensus(observations []Observation, value func(Observation) *bool) *bool {
	var agreed *bool
	for _, observation := range observations {
		current := value(observation)
		if current == nil || agreed != nil && *agreed != *current {
			return nil
		}
		agreed = current
	}
	return agreed
}

// certEffectiveFor reports whether some service of domain serves the certificate
// as valid, matching and unexpired. Explicit false flags keep it pending.
func certEffectiveFor(observations []Observation, domain string) bool {
	for _, o := range observations {
		if o.Domain == domain && o.IsValid != nil && *o.IsValid && o.IsMatch != nil && *o.IsMatch && (o.IsExpired == nil || !*o.IsExpired) {
			return true
		}
	}
	return false
}

// certServiceHost returns the service as scheme://host:port (or host:port) and
// the normalized domain of its host name, "" for an IP.
func certServiceHost(value string) (string, string) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", ""
	}
	if !strings.Contains(value, "://") {
		value = "//" + value
	}
	parsed, err := url.Parse(value)
	if err != nil || parsed.Hostname() == "" {
		return "", ""
	}
	host := parsed.Host
	if parsed.Scheme != "" {
		host = parsed.Scheme + "://" + host
	}
	return host, NormalizeDomain(parsed.Hostname())
}

// certField reads a dotted field as a flat key or a nested object path.
func certField(values map[string]json.RawMessage, field string) json.RawMessage {
	if value, ok := values[field]; ok {
		return value
	}
	head, rest, ok := strings.Cut(field, ".")
	if !ok {
		return nil
	}
	var nested map[string]json.RawMessage
	if json.Unmarshal(values[head], &nested) != nil {
		return nil
	}
	return certField(nested, rest)
}

func certString(values map[string]json.RawMessage, field string) string {
	var value string
	_ = json.Unmarshal(certField(values, field), &value)
	return strings.TrimSpace(value)
}

func certFirstString(values map[string]json.RawMessage, fields ...string) string {
	for _, field := range fields {
		if value := certString(values, field); value != "" {
			return value
		}
	}
	return ""
}

// certScalar accepts an identifier as a string or an exact JSON number.
func certScalar(raw json.RawMessage) string {
	var text string
	if json.Unmarshal(raw, &text) == nil {
		return strings.TrimSpace(text)
	}
	var number json.Number
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if len(raw) != 0 && decoder.Decode(&number) == nil {
		return number.String()
	}
	return ""
}

// certFlag reads a boolean reported as JSON or as the strings "true"/"false".
func certFlag(raw json.RawMessage) *bool {
	var value bool
	if json.Unmarshal(raw, &value) == nil && string(raw) != "null" {
		return &value
	}
	var text string
	if json.Unmarshal(raw, &text) == nil && (text == "true" || text == "false") {
		value = text == "true"
		return &value
	}
	return nil
}

func certNonEmpty(value string) []string {
	if value == "" {
		return nil
	}
	return []string{value}
}
