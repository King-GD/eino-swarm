package main

import (
	"bytes"
	"context"

	linkcrypto "github.com/LubyRuffy/pairlink/crypto"
	pstore "github.com/LubyRuffy/pairlink/store"
)

// fixtureBindings uses pairlink Memory. BindingByPeers returns the newest live
// row for a host and device. Revoked-only history is ErrRevoked.
type fixtureBindings struct{ *pstore.Memory }

func newFixtureBindings() *fixtureBindings { return &fixtureBindings{Memory: pstore.NewMemory()} }

func (f *fixture) retirePhoneBindings(ctx context.Context, encodedPub string) error {
	if f.bindings == nil {
		return nil
	}
	pub, err := linkcrypto.ParsePublic(encodedPub)
	if err != nil {
		return err
	}
	rows, err := f.bindings.ListBindings(ctx, f.hostPub)
	if err != nil {
		return err
	}
	for _, row := range rows {
		if bytes.Equal(row.DevicePub, pub) {
			if err := f.bindings.RevokeBinding(ctx, row.ID); err != nil {
				return err
			}
		}
	}
	return nil
}

func (f *fixture) observeBindings(value map[string]any) {
	active, retired := 0, 0
	if f.bindings != nil {
		rows, _ := f.bindings.ListAllBindings(context.Background())
		for _, row := range rows {
			if row.Revoked {
				retired++
			} else {
				active++
			}
		}
	}
	value["active_bindings"], value["retired_bindings"] = active, retired
	value["host_online"], value["mobile_online"] = false, false
	if f.relay != nil {
		value["host_online"] = f.relay.PeerOnline(f.hostPub)
		if f.flow != nil {
			if pub, err := linkcrypto.ParsePublic(f.flow.DevicePub); err == nil {
				value["mobile_online"] = f.relay.PeerOnline(pub)
			}
		}
	}
}
