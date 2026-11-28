package bouncer

import (
	"context"

	"github.com/4evrd/evrd/internal/store"
)

// saveModuleSetting saves a setting a running module changed itself
// (crypt's keys, set from an IRC client). The running connection already
// has it, so it isn't reconfigured.
func (b *Bouncer) saveModuleSetting(networkID int64, module, key, value string) error {
	ctx := context.Background()
	n, err := b.store.NetworkByID(ctx, networkID)
	if err != nil {
		return err
	}
	mods := make(map[string]store.ModuleConfig, len(n.Modules))
	for k, v := range n.Modules {
		mods[k] = v
	}
	mc := mods[module]
	settings := map[string]string{}
	for k, v := range mc.Settings {
		settings[k] = v
	}
	settings[key] = value
	mc.Settings = settings
	mods[module] = mc
	n.Modules = mods
	return b.store.UpdateNetwork(ctx, n)
}
