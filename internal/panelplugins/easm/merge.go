package easm

import (
	"encoding/json"
	"strings"
)

// The merge rules keep the published tree invariants: one subject root, one
// incoming edge per node, and a node that keeps the first parent that reached
// it. A later batch may confirm that edge or enrich the node, never re-parent it
// or downgrade it.

// mergeCompanies folds the batch's companies into the graph in two passes, so a
// child reported before its parent in the same batch still finds it.
func (r *run) mergeCompanies() {
	if len(r.input.Companies) == 0 {
		return
	}
	savedEdges := len(r.graph.edges)
	for _, entry := range r.input.Companies {
		id, name, ok := companyIdentity(entry)
		if !ok {
			continue
		}
		if r.isSubject(entry) {
			// The subject's own registry row describes the root. A separate node
			// would take the subject's name, and every child naming the subject
			// as parent would hang under that copy instead of the root.
			r.graph.AddBasis(RootID, basisOf(entry.Source, entry.Via))
			continue
		}
		r.graph.Upsert(Node{ID: id, Kind: "company", Label: name, Detail: companyDetail(entry, name)})
		r.graph.AddBasis(id, basisOf(entry.Source, entry.Via))
	}
	for _, entry := range r.input.Companies {
		id, _, ok := companyIdentity(entry)
		if !ok || r.isSubject(entry) {
			continue
		}
		// A company already on the graph keeps its parent, so a second tool
		// naming a different one cannot move it.
		parent, known := r.ownerOf(entry.Parent)
		r.graph.Link(parent, id, entry.Confirmed && known)
	}
	r.graph.keepRootedSources(savedEdges)
}

// mergeDomains places each reported domain under its company. Domains are
// bounded by the configured budget for the whole portrait, not per call.
func (r *run) mergeDomains() {
	for _, entry := range r.input.Domains {
		domain := NormalizeDomain(entry.Domain)
		if domain == "" {
			continue
		}
		id := DomainID(domain)
		if r.graph.Node(id) == nil && !r.reserveDomain() {
			continue
		}
		r.graph.Upsert(Node{ID: id, Kind: "domain", Label: domain})
		r.graph.AddBasis(id, basisOf(entry.Source, entry.Via))
		parent, known := r.ownerOf(entry.Parent)
		r.graph.Link(parent, id, entry.Confirmed && known)
	}
}

// mergeCertificates hangs each reported certificate under the domain it was
// observed on. Certificate names do not create domain nodes: the agent decides
// what enters the portrait, the producer only records what it was given.
func (r *run) mergeCertificates() {
	if len(r.input.Certificates) == 0 {
		return
	}
	serials := make(map[string]bool)
	for _, node := range r.graph.nodes {
		if serial := certificateSerial(node); serial != "" {
			serials[serial] = true
		}
	}
	for _, entry := range r.input.Certificates {
		serial := strings.TrimSpace(entry.Serial)
		if serial == "" || serials[serial] {
			continue
		}
		serials[serial] = true
		issuer := strings.TrimSpace(entry.Issuer)
		id := certificateID(serial, issuer)
		label := strings.TrimSpace(entry.SubjectCN)
		if label == "" {
			label = serial
		}
		r.graph.Upsert(Node{ID: id, Kind: "feature", FeatureKind: "cert", Label: label, Detail: certificateDetail(entry, serial, issuer)})
		r.graph.AddBasis(id, basisOf(entry.Source, entry.Via))
		source, placed := r.certificateDomain(entry)
		// Only a valid, matching, unexpired certificate confirms its edge; the
		// certificate's own flags say nothing about who owns the domain.
		effective := isTrue(entry.Valid) && isTrue(entry.Match) && !isTrue(entry.Expired)
		r.graph.Link(source, id, effective && placed)
	}
}

// ownerOf resolves a reported parent name to a node on the graph and counts the
// ones it cannot place. An unknown name hangs the entry under the subject as a
// clue, so nothing ever floats detached and the receipt says what is missing.
func (r *run) ownerOf(name string) (string, bool) {
	owner, known := r.resolveCompany(name)
	if known {
		return owner, true
	}
	r.unresolvedParents++
	return RootID, false
}

// resolveCompany finds the company a parent or owner name refers to. Two
// companies with the same name are two registrations, so neither can be meant.
func (r *run) resolveCompany(name string) (string, bool) {
	name = strings.TrimSpace(name)
	if name == "" {
		// Omitted parent: the entry hangs under the subject by definition.
		return RootID, true
	}
	var owner string
	for _, node := range r.graph.nodes {
		if node.Kind != "company" && node.Kind != "root" {
			continue
		}
		if name != node.Label && (node.Detail == nil || name != node.Detail.RegistryName) {
			continue
		}
		if owner != "" {
			return "", false
		}
		owner = node.ID
	}
	return owner, owner != "" && !r.graph.detached[owner]
}

// certificateDomain returns the node a certificate hangs under. A domain the
// agent has not pushed yet is added as a pending clue under the subject; a
// malformed or over-budget domain leaves the certificate under the subject.
func (r *run) certificateDomain(entry CertificateInput) (string, bool) {
	domain := NormalizeDomain(entry.Domain)
	if domain == "" {
		return RootID, false
	}
	id := DomainID(domain)
	if r.graph.Node(id) != nil {
		return id, true
	}
	if !r.reserveDomain() {
		return RootID, false
	}
	r.graph.AddDomain(RootID, domain, false, basisOf(entry.Source, entry.Via))
	return id, true
}

// reserveDomain enforces the domain budget and counts each refused entry, so the
// receipt tells the agent how much of its batch the panel dropped.
func (r *run) reserveDomain() bool {
	if r.graph.DomainCount() >= r.settings.MaxDomains {
		r.refusedDomains++
		return false
	}
	return true
}

// isSubject requires the same registration when one is known; a name-only
// record cannot identify itself as a known registration.
func (r *run) isSubject(entry CompanyInput) bool {
	root := r.graph.Root()
	reported := strings.TrimSpace(entry.RegistryID)
	if saved := registryID(root); saved != "" {
		return reported == saved
	}
	return reported == "" && strings.TrimSpace(entry.Name) == root.Label
}

// Only an unambiguous confirmed row for the selected name can establish its
// registration. Other companies in the batch are evidence for other clues.
func selectedRegistry(input Input) string {
	var registry string
	for _, entry := range input.Companies {
		id := strings.TrimSpace(entry.RegistryID)
		if id == "" || !entry.Confirmed || strings.TrimSpace(entry.Source) == "" ||
			strings.TrimSpace(entry.Name) != input.Subject || strings.TrimSpace(entry.Parent) != "" {
			continue
		}
		if registry != "" && registry != id {
			return ""
		}
		registry = id
	}
	return registry
}

// companyIdentity is a company's node identity. A registry ID wins so one
// registration keeps one node across tools; without one the reported name is
// what identifies it.
func companyIdentity(entry CompanyInput) (string, string, bool) {
	name := strings.TrimSpace(entry.Name)
	if name == "" {
		return "", "", false
	}
	if registry := strings.TrimSpace(entry.RegistryID); registry != "" {
		return registryCompanyID(registry), name, true
	}
	return companyNameID(name), name, true
}

func registryCompanyID(id string) string { return "company:reg:" + id }

func companyNameID(name string) string { return "company:name:" + name }

// decimalID accepts a positive decimal identity as a JSON string or an exact
// number. Rounded or fractional values are rejected rather than guessed.
func decimalID(raw json.RawMessage) string {
	if len(raw) == 0 || string(raw) == "null" {
		return ""
	}
	var value string
	if json.Unmarshal(raw, &value) != nil {
		value = string(raw)
	}
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	for _, char := range value {
		if char < '0' || char > '9' {
			return ""
		}
	}
	return value
}

func companyDetail(entry CompanyInput, name string) *Detail {
	detail := &Detail{Relation: strings.TrimSpace(entry.Relation), Share: strings.TrimSpace(entry.Share), RegistryID: strings.TrimSpace(entry.RegistryID)}
	if detail.Relation == "" && detail.Share == "" && detail.RegistryID == "" {
		return nil
	}
	if detail.RegistryID != "" {
		detail.RegistryName = name
	}
	return detail
}

// certificateDetail keeps the certificate as reported. Serial is required, so
// the detail is never empty.
func certificateDetail(entry CertificateInput, serial, issuer string) *Detail {
	return &Detail{
		Serial:      serial,
		Issuer:      issuer,
		NotBefore:   strings.TrimSpace(entry.NotBefore),
		NotAfter:    strings.TrimSpace(entry.NotAfter),
		IsValid:     entry.Valid,
		IsMatch:     entry.Match,
		Names:       entry.Names,
		SubjectName: strings.TrimSpace(entry.SubjectOrg),
	}
}

// Keep the existing ID format for the first stored observation. Deduplication
// uses the serial, independently of the issuer included in this ID.
func certificateID(serial, issuer string) string {
	id := "feature:cert:" + serial
	if issuer != "" {
		id += "@" + issuer
	}
	return id
}

func certificateSerial(node *Node) string {
	if node.Kind != "feature" || node.FeatureKind != "cert" || node.Detail == nil {
		return ""
	}
	return strings.TrimSpace(node.Detail.Serial)
}

func basisOf(source, via string) Basis {
	return Basis{Source: strings.TrimSpace(source), Via: strings.TrimSpace(via)}
}

func isTrue(value *bool) bool { return value != nil && *value }
