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
	ctx := context.Background()
	v, err := plugin.NewVault(ctx, log)
	if err != nil {
		log.Error("cannot create vault client", "error", err)
	}
	return plugin.NewDODropletsPlugin(ctx, log, v)
}
