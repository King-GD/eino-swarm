package engine

import (
	"encoding/json"

	jsonpatch "github.com/evanphx/json-patch"
)

type easmCertificateServiceObservation struct {
	Host      string `json:"host"`
	Source    string `json:"source,omitempty"`
	IsValid   *bool  `json:"is_valid,omitempty"`
	IsMatch   *bool  `json:"is_match,omitempty"`
	IsExpired *bool  `json:"is_expired,omitempty"`
}

// Replace only this source/service's latest observation. A missing flag stays
// unknown and cannot borrow another service's value for a certificate summary.
func easmCertificateObserve(detail, row map[string]json.RawMessage, domain string) (changed, valid bool) {
	var observations []easmCertificateServiceObservation
	if raw := detail["observations"]; len(raw) != 0 && json.Unmarshal(raw, &observations) != nil {
		return false, false
	}
	for _, observation := range observations {
		if observation.Host == "" {
			return false, false
		}
	}
	before, _ := json.Marshal(detail)
	host, _ := easmAssetCertificateHost(easmCertificateString(row, "host"))
	if host == "" {
		host = domain
	}
	if raw, specified := row["_zwai_certificate_host"]; specified {
		_ = json.Unmarshal(raw, &host)
	}
	if host != "" {
		observed := easmCertificateServiceObservation{
			Host: host, Source: easmCertificateString(row, "_zwai_certificate_source"),
			IsValid:   easmCertificateFlag(easmCertificateValue(row, "cert.is_valid")),
			IsMatch:   easmCertificateFlag(easmCertificateValue(row, "cert.is_match")),
			IsExpired: easmCertificateFlag(easmCertificateValue(row, "cert.is_expired")),
		}
		position := len(observations)
		for i, previous := range observations {
			if previous.Host == observed.Host && previous.Source == observed.Source {
				position = i
				break
			}
		}
		if position == len(observations) {
			observations = append(observations, observed)
		} else {
			observations[position] = observed
		}
		detail["observations"], _ = json.Marshal(observations)
	}
	for _, key := range []string{"is_valid", "is_match"} {
		var consensus *bool
		for _, observation := range observations {
			value := observation.IsValid
			if key == "is_match" {
				value = observation.IsMatch
			}
			if value == nil || consensus != nil && *consensus != *value {
				consensus = nil
				break
			}
			consensus = value
		}
		delete(detail, key)
		if consensus != nil {
			detail[key], _ = json.Marshal(*consensus)
		}
	}
	after, _ := json.Marshal(detail)
	return !jsonpatch.Equal(before, after), true
}
