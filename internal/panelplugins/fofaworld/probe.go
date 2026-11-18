package fofaworld

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"net"
	"net/http"
	"strconv"
	"time"
)

func probe(ctx context.Context, a Asset, timeout time.Duration) Result {
	started := time.Now()
	result := Result{Status: "no_response"}
	dialer := &net.Dialer{Timeout: timeout}
	transport := &http.Transport{
		// The recorded IP is the target even if the logical hostname has moved.
		// Proxy is deliberately nil, so environment proxy settings cannot move
		// these explicitly local requests to an unrelated gateway.
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			return dialer.DialContext(ctx, "tcp", net.JoinHostPort(a.IP, strconv.Itoa(a.Port)))
		},
		DisableKeepAlives: true, DisableCompression: true,
		TLSHandshakeTimeout: timeout, ResponseHeaderTimeout: timeout,
	}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: timeout,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, a.URL, nil)
	if err == nil {
		var response *http.Response
		response, err = client.Do(request)
		if err == nil {
			result.Status, result.StatusCode = "responded", response.StatusCode
			// We need response headers only. Do not save or drain arbitrary bodies.
			if closeErr := response.Body.Close(); closeErr != nil {
				result.Error = "body_close"
			}
		}
	}
	if err != nil {
		result.Error = probeError(ctx, err)
		if ctx.Err() != nil {
			result.Status = "interrupted"
		}
	}
	result.ElapsedMS = time.Since(started).Milliseconds()
	result.FinishedAt = time.Now().UTC().Format(time.RFC3339Nano)
	return result
}

func probeError(ctx context.Context, err error) string {
	if ctx.Err() != nil {
		return "cancelled"
	}
	var timeout net.Error
	if errors.As(err, &timeout) && timeout.Timeout() {
		return "timeout"
	}
	var certificate *tls.CertificateVerificationError
	var unknown x509.UnknownAuthorityError
	var host x509.HostnameError
	if errors.As(err, &certificate) || errors.As(err, &unknown) || errors.As(err, &host) {
		return "tls"
	}
	return "request_failed"
}
