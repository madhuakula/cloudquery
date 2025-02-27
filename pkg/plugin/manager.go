package plugin

import (
	"context"
	"fmt"

	"github.com/cloudquery/cloudquery/pkg/config"
	"github.com/cloudquery/cloudquery/pkg/ui"
	"github.com/hashicorp/go-hclog"

	"github.com/cloudquery/cloudquery/pkg/plugin/registry"
	"github.com/cloudquery/cq-provider-sdk/serve"
	"github.com/rs/zerolog/log"
	"github.com/spf13/viper"
)

// Manager handles lifecycle execution of CloudQuery providers
type Manager struct {
	hub       *registry.Hub
	clients   map[string]Plugin
	providers map[string]registry.ProviderDetails
	logger    hclog.Logger
}

func NewManager(logger hclog.Logger, pluginDirectory string, registryURL string, updater ui.Progress) (*Manager, error) {
	// primarily by the SDK's acceptance testing framework.
	unmanagedProviders, err := serve.ParseReattachProviders(viper.GetString("reattach-providers"))
	if err != nil {
		return nil, err
	}
	clients := make(map[string]Plugin)
	for name, cfg := range unmanagedProviders {
		log.Debug().Str("name", name).Str("address", cfg.Addr.String()).Int("pid", cfg.Pid).Msg("reattaching unmanaged plugin")
		plugin, err := newUnmanagedPlugin(name, cfg)
		if err != nil {
			return nil, err
		}
		clients[name] = plugin
	}
	return &Manager{
		clients:   clients,
		logger:    logger,
		providers: make(map[string]registry.ProviderDetails),
		hub: registry.NewRegistryHub(registryURL, func(h *registry.Hub) {
			h.ProgressUpdater = updater
			h.PluginDirectory = pluginDirectory
		}),
	}, nil
}

func (m *Manager) DownloadProviders(ctx context.Context, providers []*config.RequiredProvider, noVerify bool) error {
	m.logger.Info("Downloading required providers")
	for _, rp := range providers {
		m.logger.Info("Downloading provider", "name", rp.Name, "version", rp.Version)
		details, err := m.hub.DownloadProvider(ctx, rp, noVerify)
		if err != nil {
			return err
		}
		m.providers[rp.Name] = details
	}
	return nil
}

func (m *Manager) CreatePlugin(providerName, alias string, env []string) (Plugin, error) {
	p, ok := m.clients[providerName]
	if ok {
		return p, nil
	}
	m.logger.Info("plugin doesn't exist, creating..", "provider", providerName, "name", alias)
	details, ok := m.providers[providerName]
	if !ok {
		return nil, fmt.Errorf("no such provider %s. plugin might be missing from directory or wasn't downloaded", providerName)
	}
	p, err := m.createProvider(&details, alias, env)
	if err != nil {
		return nil, err
	}
	return p, nil
}

// Shutdown closes all clients and cleans the managed clients
func (m *Manager) Shutdown() {
	for _, c := range m.clients {
		c.Close()
	}
	// create fresh map
	m.clients = make(map[string]Plugin)
}

func (m *Manager) KillProvider(providerName string) error {

	client, ok := m.clients[providerName]
	if !ok {
		return fmt.Errorf("client for provider %s does not exist", providerName)
	}
	client.Close()
	delete(m.clients, providerName)
	return nil
}

func (m *Manager) createProvider(details *registry.ProviderDetails, alias string, env []string) (Plugin, error) {
	mPlugin, err := newRemotePlugin(details, alias, env)
	if err != nil {
		return nil, err
	}
	m.clients[mPlugin.Name()] = mPlugin
	return mPlugin, nil
}
