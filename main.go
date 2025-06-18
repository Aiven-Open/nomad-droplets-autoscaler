package main

import (
	"github.com/google/uuid"
	"github.com/hashicorp/go-hclog"
	"github.com/hashicorp/nomad-autoscaler/plugins"
	"github.com/jsiebens/nomad-droplets-autoscaler/plugin"
)

func main() {
	uuid.EnableRandPool()
	plugins.Serve(factory)
}

func factory(log hclog.Logger) interface{} {
	return plugin.NewDODropletsPlugin(log)
}
