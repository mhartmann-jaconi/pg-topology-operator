package cluster

import (
	"github.com/spf13/viper"
	"vcs01.rsint.net/comvidence/sources/platform/tools/pg-topology-operator/pkg/config"
	"vcs01.rsint.net/comvidence/sources/platform/tools/pg-topology-operator/pkg/events"
)

const (
	leaderLabel = "lead"
	regionLabel = "region"
	zoneLabel   = "zone"
)

func setup() {
	viper.Set(config.LabelLeader, leaderLabel)
	viper.Set(config.LabelRegion, regionLabel)
	viper.Set(config.LabelZone, zoneLabel)
}

func cleanup() {
	ClearState()
	events.SetListenersForRegionChange() // reset global listeners to prevent cross-test leakage
	viper.Reset()
}
