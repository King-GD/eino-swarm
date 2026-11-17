package fofaworld

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"math"
	"net"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
)

func decodeAsset(line []byte) (Asset, error) {
	var raw map[string]json.RawMessage
	if json.Unmarshal(line, &raw) != nil || raw == nil {
		return Asset{}, failure("invalid_output")
	}
	fields := make(map[string]string, len(raw))
	for key, value := range raw {
		var s string
		if json.Unmarshal(value, &s) != nil {
			var n json.Number
			if json.Unmarshal(value, &n) != nil || string(value) == "null" {
				return Asset{}, failure("invalid_output")
			}
			s = n.String()
		}
		fields[key] = s
	}
	ip, err := netip.ParseAddr(strings.TrimSpace(fields["ip"]))
	if err != nil || ip.Zone() != "" {
		return Asset{}, failure("invalid_asset")
	}
	port, err := strconv.Atoi(strings.TrimSpace(fields["port"]))
	if err != nil || port < 1 || port > 65535 {
		return Asset{}, failure("invalid_asset")
	}
	a := Asset{IP: ip.Unmap().String(), Port: port,
		Protocol: strings.ToLower(strings.TrimSpace(fields["protocol"])),
		Host:     fields["host"], Country: strings.ToUpper(strings.TrimSpace(fields["country"])),
		Region: fields["region"], City: fields["city"], Status: "pending"}
	lon, lonErr := strconv.ParseFloat(fields["longitude"], 64)
	lat, latErr := strconv.ParseFloat(fields["latitude"], 64)
	if lonErr == nil && latErr == nil && !math.IsNaN(lon) && !math.IsNaN(lat) && math.Abs(lon) <= 180 && math.Abs(lat) <= 90 {
		a.Longitude, a.Latitude = &lon, &lat
	}
	origin := strings.ToLower(a.Host)
	if a.Protocol == "http" || a.Protocol == "https" {
		u, err := assetURL(a)
		if err != nil {
			return Asset{}, err
		}
		a.URL, origin = u.String(), canonicalOrigin(u)
	} else {
		a.Status = "skipped"
	}
	hash := sha256.Sum256([]byte(a.IP + "\x00" + strconv.Itoa(a.Port) + "\x00" + a.Protocol + "\x00" + origin))
	a.ID = hex.EncodeToString(hash[:])
	return a, nil
}

func assetURL(a Asset) (*url.URL, error) {
	host := strings.TrimSpace(a.Host)
	if host == "" {
		host = net.JoinHostPort(a.IP, strconv.Itoa(a.Port))
	}
	if !strings.Contains(host, "://") {
		host = a.Protocol + "://" + host
	}
	u, err := url.Parse(host)
	if err != nil || u.Hostname() == "" || u.User != nil || u.Scheme != a.Protocol || strings.ContainsAny(u.Host, "\\\r\n\t ") {
		return nil, failure("invalid_asset")
	}
	// FOFA records identify an origin. Do not carry arbitrary paths, credentials
	// or query parameters into a local liveness request.
	u.Path, u.RawPath, u.RawQuery, u.Fragment = "/", "", "", ""
	u.Host = strings.ToLower(u.Host)
	return u, nil
}

// HTTP's default ports are origin semantics, not a configured target. Keep the
// original URL for Host/SNI while deduplicating equivalent source spellings.
func canonicalOrigin(u *url.URL) string {
	host, port := strings.ToLower(u.Hostname()), u.Port()
	if u.Scheme == "http" && port == "80" || u.Scheme == "https" && port == "443" {
		port = ""
	}
	if port != "" {
		host = net.JoinHostPort(host, port)
	} else if strings.Contains(host, ":") {
		host = "[" + host + "]"
	}
	return u.Scheme + "://" + host
}
