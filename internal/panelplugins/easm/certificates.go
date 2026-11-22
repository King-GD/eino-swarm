package easm

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
)

// Certificates come from the aggregation tool: the search tool rejects every
// cert.* return field, while a cert.sn aggregation with detail returns each
// certificate's names, issuer, subject, validity and flags in one bucket.
func init() {
	stepFuncs[StepCertificates] = stepDef{roles: []string{RoleStats}, fn: (*run).certificates}
}

const (
	// certificateBuckets is the certificates requested per domain, newest
	// and most served first as the aggregation orders them by asset count.
	certificateBuckets = 5
	// certificateConcurrency bounds the parallel per-domain aggregations.
	certificateConcurrency = 4
)

type certificateBucket struct {
	Name   json.RawMessage `json:"name"`
	Count  json.Number     `json:"count"`
	Detail struct {
		Domain    []string `json:"domain"`
		IsExpired *bool    `json:"is_expired"`
		IsMatch   *bool    `json:"is_match"`
		IsValid   *bool    `json:"is_valid"`
		NotAfter  string   `json:"not_after"`
		NotBefore string   `json:"not_before"`
		Issuer    struct {
			CN  string   `json:"cn"`
			Org []string `json:"org"`
		} `json:"issuer"`
		Subject struct {
			CN  string   `json:"cn"`
			Org []string `json:"org"`
		} `json:"subject"`
	} `json:"detail"`
}

type certificateReply struct {
	Aggs map[string][]certificateBucket `json:"aggs"`
}

// certificates aggregates the certificates served for each graph domain. The
// lookups run concurrently; their results are applied in domain order so the
// graph does not depend on which reply arrives first.
func (r *run) certificates(ctx context.Context) error {
	domains := r.graph.Domains()
	if len(domains) > r.settings.MaxDomains {
		domains = domains[:r.settings.MaxDomains]
	}
	replies := make([][]certificateBucket, len(domains))
	errs := make([]error, len(domains))
	slots := make(chan struct{}, certificateConcurrency)
	var wg sync.WaitGroup
	for i, domain := range domains {
		wg.Add(1)
		slots <- struct{}{}
		go func() {
			defer wg.Done()
			defer func() { <-slots }()
			replies[i], errs[i] = r.certificateBuckets(ctx, domain)
		}()
	}
	wg.Wait()
	var first error
	for i, domain := range domains {
		if errs[i] != nil {
			if first == nil {
				first = errs[i]
			}
			continue
		}
		for _, bucket := range replies[i] {
			r.addCertificate(domain, bucket)
		}
	}
	return first
}

func (r *run) certificateBuckets(ctx context.Context, domain string) ([]certificateBucket, error) {
	var reply certificateReply
	arguments := map[string]any{"query": fmt.Sprintf("domain=%q", domain), "fields": "cert.sn", "detail": true, "tool": fofaSource, "size": certificateBuckets}
	if err := r.call(ctx, RoleStats, arguments, &reply); err != nil {
		return nil, err
	}
	buckets, ok := reply.Aggs["cert.sn"]
	if !ok && reply.Aggs == nil {
		return nil, fmt.Errorf("certificate aggregation has no aggs")
	}
	return buckets, nil
}

// addCertificate places one certificate under the queried domain. Identity is
// issuer plus serial, so a certificate served on several domains stays one
// node; only a valid, matching, unexpired certificate confirms its edge.
func (r *run) addCertificate(queried string, bucket certificateBucket) {
	serial := certificateSerial(bucket.Name)
	if serial == "" {
		return
	}
	detail := bucket.Detail
	issuer := detail.Issuer.CN
	if issuer == "" && len(detail.Issuer.Org) > 0 {
		issuer = detail.Issuer.Org[0]
	}
	id := "feature:cert:" + serial
	if issuer != "" {
		id += "@" + issuer
	}
	label := strings.TrimSpace(detail.Subject.CN)
	if label == "" {
		label = serial
	}
	node := Detail{Serial: serial, Issuer: issuer, NotBefore: detail.NotBefore, NotAfter: detail.NotAfter, IsValid: detail.IsValid, IsMatch: detail.IsMatch, Names: detail.Domain}
	if len(detail.Subject.Org) > 0 {
		node.SubjectName = detail.Subject.Org[0]
	}
	r.graph.Upsert(Node{ID: id, Kind: "feature", FeatureKind: "cert", Label: label, Detail: &node})
	r.graph.AddBasis(id, Basis{Source: "certificates", Via: queried})
	effective := isTrue(detail.IsValid) && isTrue(detail.IsMatch) && !isTrue(detail.IsExpired)
	source := DomainID(queried)
	r.graph.Link(source, id, effective)
	if parent, ok := r.graph.Parent(id); ok && parent != source && effective {
		// A shared certificate keeps its first parent; this domain records it
		// as an effective feature instead of a second edge.
		if domain := r.graph.Node(source); domain != nil {
			r.graph.Upsert(Node{ID: source, Label: domain.Label, Detail: &Detail{Features: []Feature{{Kind: "cert", Value: label, NodeID: id}}}})
		}
	}
	for _, name := range detail.Domain {
		domain := NormalizeDomain(name)
		if domain == "" || domain == queried || r.graph.Node(DomainID(domain)) != nil {
			continue
		}
		// A domain first seen on a certificate is a clue under the target,
		// never a detached island.
		r.graph.AddDomain(RootID, domain, false, Basis{Source: "certificates", Via: label})
	}
}

func certificateSerial(raw json.RawMessage) string {
	var text string
	if json.Unmarshal(raw, &text) == nil {
		return strings.TrimSpace(text)
	}
	var number json.Number
	if json.Unmarshal(raw, &number) == nil {
		return number.String()
	}
	return ""
}

func isTrue(value *bool) bool { return value != nil && *value }
