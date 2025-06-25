package main

import (
	"context"

	"github.com/Aiven-Open/nomad-droplets-autoscaler/plugin"
	"github.com/google/uuid"
	"github.com/hashicorp/go-hclog"
	"github.com/hashicorp/nomad-autoscaler/plugins"
)

func main() {
	uuid.EnableRandPool()
	plugins.Serve(factory)
}

func factory(log hclog.Logger) interface{} {
	return plugin.NewDODropletsPlugin(context.Background(), log, plugin.Must(plugin.NewVault()))
}
